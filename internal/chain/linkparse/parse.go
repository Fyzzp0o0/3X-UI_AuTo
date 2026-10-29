// Package linkparse turns pasted proxy lists into normalized proxies. Lines are
// parsed independently so one bad row never aborts an import.
package linkparse

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/util/netsafe"
)

// Supported protocols. Only socks5 is generated end-to-end in M0; http is
// parsed so an import preview can tell the operator what was found.
const (
	ProtoSocks5 = "socks5"
	ProtoHTTP   = "http"
)

// Error codes are stable so the UI can translate them without parsing prose.
const (
	ErrSyntax      = "syntax"
	ErrPort        = "port"
	ErrHost        = "host"
	ErrPrivate     = "private"
	ErrUnsupported = "unsupported"
)

// Proxy is one normalized upstream egress endpoint.
type Proxy struct {
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	Label    string `json:"label,omitempty"`
}

// Options controls import-time policy. AllowPrivate is an explicit opt-in for
// self-hosted upstreams; it never defaults on because it doubles as SSRF cover.
type Options struct {
	AllowPrivate bool
}

// Result is one input line's outcome. Line is masked (password replaced) and
// safe to log, echo to the API or store as raw_line.
type Result struct {
	LineNo int    `json:"lineNo"`
	Line   string `json:"line"`
	Proxy  Proxy  `json:"proxy"`
	Err    string `json:"error,omitempty"`
}

// OK reports whether the line produced a usable proxy.
func (r Result) OK() bool { return r.Err == "" && r.Proxy.Host != "" }

// ParseError carries a stable code plus a detail with no credential material.
type ParseError struct {
	Code   string
	Detail string
}

func (e *ParseError) Error() string { return e.Code + ": " + e.Detail }

func perr(code, detail string) error { return &ParseError{Code: code, Detail: detail} }

// ParseLines parses a whole paste. Blank lines and shell/YAML comments are
// skipped rather than reported, so a copied document does not produce noise.
func ParseLines(input string, opt Options) []Result {
	lines := strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n")
	out := make([]Result, 0, len(lines))
	for i, raw := range lines {
		line := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") ||
			strings.HasPrefix(line, ";") || strings.HasPrefix(line, "---") {
			continue
		}
		// YAML list dashes are optional decoration; strip one leading dash.
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))

		p, err := parseLine(line, opt)
		res := Result{LineNo: i + 1, Line: mask(line, p.Password), Proxy: p}
		if err != nil {
			var pe *ParseError
			if errors.As(err, &pe) {
				res.Err = pe.Code
			} else {
				res.Err = ErrSyntax
			}
		}
		out = append(out, res)
	}
	return out
}

// ValidateProxy checks one already-parsed proxy without touching the network.
func ValidateProxy(p Proxy, opt Options) error {
	switch p.Protocol {
	case ProtoSocks5, ProtoHTTP:
	default:
		return perr(ErrUnsupported, "protocol "+p.Protocol)
	}
	if _, err := parsePort(strconv.Itoa(p.Port)); err != nil {
		return err
	}
	return validateHost(p.Host, opt)
}

// Fingerprint identifies a proxy for dedupe. The password is deliberately not
// part of it: editing a password must not create a second row.
func Fingerprint(p Proxy) string {
	sum := sha256.Sum256([]byte(strings.ToLower(p.Protocol) + "|" +
		strings.ToLower(p.Host) + "|" + strconv.Itoa(p.Port) + "|" + p.Username))
	return hex.EncodeToString(sum[:])
}

// Dedupe keeps the first occurrence of every fingerprint. The second return is
// the number of dropped duplicates.
func Dedupe(results []Result) ([]Result, int) {
	seen := make(map[string]struct{}, len(results))
	unique := make([]Result, 0, len(results))
	dropped := 0
	for _, r := range results {
		if !r.OK() {
			unique = append(unique, r)
			continue
		}
		fp := Fingerprint(r.Proxy)
		if _, dup := seen[fp]; dup {
			dropped++
			continue
		}
		seen[fp] = struct{}{}
		unique = append(unique, r)
	}
	return unique, dropped
}

func parseLine(line string, opt Options) (Proxy, error) {
	line, label := stripLabel(line)

	if strings.Contains(line, "://") {
		return parseURI(line, label, opt)
	}
	if p, ok, err := parseMap(line, opt); ok {
		if p.Label == "" {
			p.Label = label
		}
		return p, err
	}
	if decoded, ok := decodeBase64Line(line); ok {
		return parseLine(decoded, opt)
	}
	return parseCSVish(line, label, opt)
}

// stripLabel pulls a trailing " #remark" (or tab form) off non-URI lines. A
// lone '#' is left alone because it may belong to a password.
func stripLabel(line string) (string, string) {
	for _, sep := range []string{" #", "\t#"} {
		if i := strings.Index(line, sep); i > 0 {
			return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+len(sep):])
		}
	}
	return line, ""
}

func parseURI(line, label string, opt Options) (Proxy, error) {
	u, err := url.Parse(line)
	if err != nil {
		return Proxy{}, perr(ErrSyntax, "not a valid URL")
	}
	proto, ok := schemeToProtocol(u.Scheme)
	if !ok {
		return Proxy{}, perr(ErrUnsupported, "scheme "+u.Scheme)
	}
	host := u.Hostname()
	portStr := u.Port()
	if portStr == "" {
		return Proxy{}, perr(ErrSyntax, "missing port")
	}
	port, err := parsePort(portStr)
	if err != nil {
		return Proxy{}, err
	}
	if err := validateHost(host, opt); err != nil {
		return Proxy{}, err
	}
	p := Proxy{Protocol: proto, Host: host, Port: port, Label: label}
	if u.User != nil {
		p.Username = u.User.Username()
		p.Password, _ = u.User.Password()
	}
	if p.Label == "" {
		p.Label = u.Fragment
	}
	return p, nil
}

func schemeToProtocol(scheme string) (string, bool) {
	switch strings.ToLower(scheme) {
	case "socks5", "socks5h", "socks":
		return ProtoSocks5, true
	case "http", "https":
		return ProtoHTTP, true
	}
	return "", false
}

var (
	mapTypeRe = regexp.MustCompile(`(?i)\btype\s*:\s*"?([a-z0-9]+)`)
	mapHostRe = regexp.MustCompile(`(?i)\bserver\s*:\s*"?([^,"'}\s]+)`)
	mapPortRe = regexp.MustCompile(`(?i)\bport\s*:\s*(\d+)`)
	mapUserRe = regexp.MustCompile(`(?i)\busername\s*:\s*"?([^,"'}\s]+)`)
	mapPassRe = regexp.MustCompile(`(?i)\bpassword\s*:\s*"?([^,"'}\s]+)`)
	mapNameRe = regexp.MustCompile(`(?i)\bname\s*:\s*"?([^,"'}]+)`)
)

// parseMap accepts a single-line Clash/YAML map entry. Multi-line YAML is out of
// scope for M0; the UI tells the operator to use one entry per line.
func parseMap(line string, opt Options) (Proxy, bool, error) {
	if !strings.Contains(line, "server") || !strings.Contains(line, "port") {
		return Proxy{}, false, nil
	}
	hostM := mapHostRe.FindStringSubmatch(line)
	portM := mapPortRe.FindStringSubmatch(line)
	if hostM == nil || portM == nil {
		return Proxy{}, true, perr(ErrSyntax, "map entry needs server and port")
	}
	port, err := parsePort(portM[1])
	if err != nil {
		return Proxy{}, true, err
	}
	if err := validateHost(hostM[1], opt); err != nil {
		return Proxy{}, true, err
	}
	p := Proxy{Protocol: ProtoSocks5, Host: hostM[1], Port: port}
	if m := mapTypeRe.FindStringSubmatch(line); m != nil {
		switch strings.ToLower(m[1]) {
		case "socks5", "socks":
			p.Protocol = ProtoSocks5
		case "http", "https":
			p.Protocol = ProtoHTTP
		default:
			return Proxy{}, true, perr(ErrUnsupported, "map type "+m[1])
		}
	}
	if m := mapUserRe.FindStringSubmatch(line); m != nil {
		p.Username = m[1]
	}
	if m := mapPassRe.FindStringSubmatch(line); m != nil {
		p.Password = m[1]
	}
	if m := mapNameRe.FindStringSubmatch(line); m != nil {
		p.Label = strings.TrimSpace(m[1])
	}
	return p, true, nil
}

func decodeBase64Line(line string) (string, bool) {
	if len(line) < 16 || strings.ContainsAny(line, ":@ []\t") {
		return "", false
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding} {
		decoded, err := enc.DecodeString(line)
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(decoded))
		if strings.Contains(s, "://") || (strings.Count(s, ":") >= 1 && !strings.ContainsAny(s, "\n\r")) {
			return s, true
		}
	}
	return "", false
}

func parseCSVish(line, label string, opt Options) (Proxy, error) {
	line = strings.NewReplacer(";", ":", ",", ":", "\t", ":", "|", ":").Replace(line)

	if at := strings.LastIndex(line, "@"); at > 0 && !strings.HasPrefix(line, "[") {
		creds, hostPort := line[:at], line[at+1:]
		host, port, err := splitHostPort(hostPort)
		if err != nil {
			return Proxy{}, err
		}
		p := Proxy{Protocol: ProtoSocks5, Host: host, Port: port, Label: label}
		if user, pass, ok := strings.Cut(creds, ":"); ok {
			p.Username, p.Password = user, pass
		} else {
			p.Username = creds
		}
		if err := validateHost(p.Host, opt); err != nil {
			return Proxy{}, err
		}
		return p, nil
	}

	if strings.HasPrefix(line, "[") {
		end := strings.Index(line, "]")
		if end < 0 {
			return Proxy{}, perr(ErrSyntax, "unterminated IPv6 bracket")
		}
		host := line[1:end]
		rest := strings.TrimPrefix(line[end+1:], ":")
		parts := strings.Split(rest, ":")
		if len(parts) < 1 || parts[0] == "" {
			return Proxy{}, perr(ErrSyntax, "missing port")
		}
		port, err := parsePort(parts[0])
		if err != nil {
			return Proxy{}, err
		}
		if err := validateHost(host, opt); err != nil {
			return Proxy{}, err
		}
		p := Proxy{Protocol: ProtoSocks5, Host: host, Port: port, Label: label}
		if len(parts) >= 2 {
			p.Username = parts[1]
		}
		if len(parts) >= 3 {
			p.Password = strings.Join(parts[2:], ":")
		}
		return p, nil
	}

	if !strings.Contains(line, ":") {
		fields := strings.Fields(line)
		if len(fields) == 2 || len(fields) == 4 {
			host, port, err := splitHostPort(fields[0] + ":" + fields[1])
			if err != nil {
				return Proxy{}, err
			}
			if err := validateHost(host, opt); err != nil {
				return Proxy{}, err
			}
			p := Proxy{Protocol: ProtoSocks5, Host: host, Port: port, Label: label}
			if len(fields) == 4 {
				p.Username, p.Password = fields[2], fields[3]
			}
			return p, nil
		}
		return Proxy{}, perr(ErrSyntax, "unrecognized line")
	}

	parts := strings.Split(line, ":")
	if len(parts) < 2 {
		return Proxy{}, perr(ErrSyntax, "unrecognized line")
	}
	// Longer splits are only ambiguous for bracket-less IPv6, where field 2 is
	// not a port. Anything else is host:port:user:<password with colons>.
	if len(parts) > 4 {
		if _, err := parsePort(parts[1]); err != nil {
			return Proxy{}, perr(ErrSyntax, "IPv6 needs [addr]:port or host:port:user:pass")
		}
	}
	host, port, err := splitHostPort(parts[0] + ":" + parts[1])
	if err != nil {
		return Proxy{}, err
	}
	if err := validateHost(host, opt); err != nil {
		return Proxy{}, err
	}
	p := Proxy{Protocol: ProtoSocks5, Host: host, Port: port, Label: label}
	if len(parts) >= 3 {
		p.Username = parts[2]
	}
	if len(parts) >= 4 {
		p.Password = strings.Join(parts[3:], ":")
	}
	return p, nil
}

func splitHostPort(hostPort string) (string, int, error) {
	host, portStr, ok := strings.Cut(hostPort, ":")
	if !ok {
		return "", 0, perr(ErrSyntax, "missing port")
	}
	port, err := parsePort(portStr)
	if err != nil {
		return "", 0, err
	}
	if host == "" {
		return "", 0, perr(ErrHost, "empty host")
	}
	return host, port, nil
}

func parsePort(s string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || port < 1 || port > 65535 {
		return 0, perr(ErrPort, "port must be 1-65535")
	}
	return port, nil
}

var hostLabelRe = regexp.MustCompile(`^[a-zA-Z0-9_]([a-zA-Z0-9_-]{0,61}[a-zA-Z0-9_])?$`)

// validateHost accepts IP literals and DNS names. IP literals are checked
// against netsafe unless the operator opted into private upstreams.
func validateHost(host string, opt Options) error {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" || len(host) > 253 || strings.ContainsAny(host, " /\\") {
		return perr(ErrHost, "invalid host")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if !opt.AllowPrivate && netsafe.IsBlockedIP(addr.AsSlice()) {
			return perr(ErrPrivate, "private or reserved address")
		}
		return nil
	}
	if strings.EqualFold(host, "localhost") {
		if !opt.AllowPrivate {
			return perr(ErrPrivate, "private or reserved address")
		}
		return nil
	}
	for _, label := range strings.Split(host, ".") {
		if !hostLabelRe.MatchString(label) {
			return perr(ErrHost, "invalid host")
		}
	}
	return nil
}

// mask removes the password from a display/log line.
func mask(line, password string) string {
	if password == "" {
		return line
	}
	return strings.ReplaceAll(line, password, "***")
}

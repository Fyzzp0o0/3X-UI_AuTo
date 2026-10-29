// Package xraygen builds the Xray outbounds and routing rules for one chain
// node. Output is deterministic so an unchanged database never looks like a
// config change to the hot-diff engine.
package xraygen

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/chain/linkparse"
)

// Tag namespace and the two tags every stock config carries.
const (
	Namespace     = "cf"
	TagDirect     = "direct"
	TagBlocked    = "blocked"
	MaxChainDepth = 3
	MaxTagLength  = 64
)

var (
	tagRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]*$`)
	emailRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]*$`)
)

// PoolPrefix is the per-pool tag namespace, e.g. "cf:p3".
func PoolPrefix(poolID int) string { return Namespace + ":p" + strconv.Itoa(poolID) }

// NodeTag is the landing outbound tag, e.g. "cf:p3:n017".
func NodeTag(poolID, seq int) string {
	return PoolPrefix(poolID) + ":n" + fmt.Sprintf("%03d", seq)
}

// FrontTag is a shared front-hop tag, e.g. "cf:p3:f:hk".
func FrontTag(poolID int, key string) string {
	return PoolPrefix(poolID) + ":f:" + SanitizeKey(key)
}

// EmailFor is the client email and routing key for a generated node.
func EmailFor(poolID, seq int) string {
	return "p" + strconv.Itoa(poolID) + "-n" + fmt.Sprintf("%03d", seq) + "@chain"
}

// SanitizeKey lowercases an operator label into a tag-safe fragment.
func SanitizeKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(" ", "-", "_", "-", "/", "-", ":", "-", ".", "-").Replace(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "x"
	}
	if len(out) > 24 {
		out = out[:24]
	}
	return out
}

// ValidateTag rejects tags the panel or the core would mishandle. Reserved
// stock tags are refused so a generated node can never hijack the default path.
func ValidateTag(tag string) error {
	if tag == "" {
		return errors.New("empty tag")
	}
	if len(tag) > MaxTagLength {
		return errors.New("tag too long")
	}
	if tag == TagDirect || tag == TagBlocked || tag == "api" {
		return errors.New("tag is reserved: " + tag)
	}
	if !tagRe.MatchString(tag) {
		return errors.New("tag has invalid characters: " + tag)
	}
	return nil
}

// ValidateEmail mirrors the panel's client-email rule: the routing key must not
// contain separators, whitespace or control characters.
func ValidateEmail(email string) error {
	if email == "" {
		return errors.New("empty email")
	}
	if len(email) > 256 {
		return errors.New("email too long")
	}
	if !emailRe.MatchString(email) {
		return errors.New("email has an invalid character")
	}
	return nil
}

// SocksOutbound is one upstream egress hop.
type SocksOutbound struct {
	Tag         string
	Address     string
	Port        int
	Username    string
	Password    string
	DialerProxy string
}

type socksSettings struct {
	Servers []socksServer `json:"servers"`
}

type socksServer struct {
	Address string      `json:"address"`
	Port    int         `json:"port"`
	Users   []socksUser `json:"users,omitempty"`
}

type socksUser struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

type outboundJSON struct {
	Tag            string         `json:"tag"`
	Protocol       string         `json:"protocol"`
	Settings       socksSettings  `json:"settings"`
	StreamSettings *streamSetting `json:"streamSettings,omitempty"`
}

type streamSetting struct {
	Sockopt sockopt `json:"sockopt"`
}

type sockopt struct {
	DialerProxy string `json:"dialerProxy"`
}

// JSON renders the outbound. Credentials are only present in the returned
// bytes; callers must not log or persist them outside the runtime config.
func (o SocksOutbound) JSON() (json.RawMessage, error) {
	if err := ValidateTag(o.Tag); err != nil {
		return nil, err
	}
	if o.Address == "" || strings.ContainsAny(o.Address, `"\/`) {
		return nil, errors.New("invalid outbound address")
	}
	if o.Port < 1 || o.Port > 65535 {
		return nil, errors.New("invalid outbound port")
	}
	if o.DialerProxy != "" {
		if err := ValidateTag(o.DialerProxy); err != nil {
			return nil, fmt.Errorf("dialerProxy: %w", err)
		}
		if o.DialerProxy == o.Tag {
			return nil, errors.New("dialerProxy points at itself")
		}
	}
	server := socksServer{Address: o.Address, Port: o.Port}
	if o.Username != "" {
		server.Users = []socksUser{{User: o.Username, Pass: o.Password}}
	}
	ob := outboundJSON{Tag: o.Tag, Protocol: "socks", Settings: socksSettings{Servers: []socksServer{server}}}
	if o.DialerProxy != "" {
		ob.StreamSettings = &streamSetting{Sockopt: sockopt{DialerProxy: o.DialerProxy}}
	}
	raw, err := json.Marshal(ob)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// Hop is one chain link. External hops reference an outbound the operator
// already has in the Xray template; the injector only validates they exist.
type Hop struct {
	Tag      string
	Proxy    linkparse.Proxy
	External bool
}

// BuildChain validates the topology and returns the outbounds in wire order:
// landing first, then each front hop, with dialerProxy pointing at the next.
func BuildChain(hops []Hop) ([]json.RawMessage, error) {
	if len(hops) == 0 {
		return nil, errors.New("empty chain")
	}
	if len(hops) > MaxChainDepth {
		return nil, fmt.Errorf("chain depth %d exceeds %d", len(hops), MaxChainDepth)
	}
	seen := make(map[string]struct{}, len(hops))
	for _, h := range hops {
		if err := ValidateTag(h.Tag); err != nil {
			return nil, err
		}
		if _, dup := seen[h.Tag]; dup {
			return nil, errors.New("duplicate hop tag: " + h.Tag)
		}
		seen[h.Tag] = struct{}{}
	}
	out := make([]json.RawMessage, 0, len(hops))
	for i, h := range hops {
		if h.External {
			continue
		}
		if h.Proxy.Protocol != "" && h.Proxy.Protocol != linkparse.ProtoSocks5 {
			return nil, fmt.Errorf("hop %s: protocol %s is not supported yet", h.Tag, h.Proxy.Protocol)
		}
		dialer := ""
		if i+1 < len(hops) {
			dialer = hops[i+1].Tag
		}
		raw, err := SocksOutbound{
			Tag: h.Tag, Address: h.Proxy.Host, Port: h.Proxy.Port,
			Username: h.Proxy.Username, Password: h.Proxy.Password, DialerProxy: dialer,
		}.JSON()
		if err != nil {
			return nil, fmt.Errorf("hop %s: %w", h.Tag, err)
		}
		out = append(out, raw)
	}
	return out, nil
}

// Rule is a minimal field routing rule. Only the keys this module emits are
// modelled, so unrelated operator keys can never be rewritten by accident.
type Rule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	User        []string `json:"user,omitempty"`
	OutboundTag string   `json:"outboundTag"`
}

// UserRule routes one client email to its landing outbound.
func UserRule(email, outboundTag string) (json.RawMessage, error) {
	if err := ValidateEmail(email); err != nil {
		return nil, err
	}
	if err := ValidateTag(outboundTag); err != nil {
		return nil, err
	}
	return json.Marshal(Rule{Type: "field", User: []string{email}, OutboundTag: outboundTag})
}

// InboundUserRule pins both the inbound tag and the email; mode dedicated uses
// it. Both conditions are ANDed by the core, which keeps the match exact.
func InboundUserRule(inboundTag, email, outboundTag string) (json.RawMessage, error) {
	if err := ValidateTag(inboundTag); err != nil {
		return nil, err
	}
	raw, err := UserRule(email, outboundTag)
	if err != nil {
		return nil, err
	}
	var r Rule
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	r.InboundTag = []string{inboundTag}
	return json.Marshal(r)
}

// BlackholeRule is the fail-closed gate used before a node is verified. The
// blocked tag is reserved, so this rule is built without ValidateTag.
func BlackholeRule(email string) (json.RawMessage, error) {
	return InboundBlackholeRule("", email)
}

// InboundBlackholeRule is BlackholeRule pinned to one inbound, what dedicated
// mode uses while a node is still provisioning.
func InboundBlackholeRule(inboundTag, email string) (json.RawMessage, error) {
	if err := ValidateEmail(email); err != nil {
		return nil, err
	}
	r := Rule{Type: "field", User: []string{email}, OutboundTag: TagBlocked}
	if inboundTag != "" {
		if err := ValidateTag(inboundTag); err != nil {
			return nil, err
		}
		r.InboundTag = []string{inboundTag}
	}
	return json.Marshal(r)
}

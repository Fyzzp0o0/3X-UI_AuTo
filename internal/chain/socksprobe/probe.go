// Package socksprobe health-checks an upstream SOCKS5 proxy without spawning
// Xray: a TCP dial, a RFC1928 handshake, and optionally a real HTTP request
// tunneled through it.
package socksprobe

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/util/netsafe"
)

// Stages reported in Result.Stage, so the UI can say where a proxy failed.
const (
	StageTCP   = "tcp"
	StageAuth  = "auth"
	StageSOCKS = "socks"
	StageHTTP  = "http"
)

// Failure classes. They never carry credentials, so they are safe to store in
// last_error and to return over the API.
const (
	ErrBlocked = "blocked"
	ErrTimeout = "timeout"
	ErrRefused = "refused"
	ErrAuth    = "auth"
	ErrConnect = "connect"
	ErrHTTP    = "http"
)

// Auth is upstream SOCKS5 username/password authentication (RFC1929).
type Auth struct {
	Username string
	Password string
}

// Options controls one probe. Timeout defaults to 5s.
type Options struct {
	Timeout      time.Duration
	AllowPrivate bool
}

func (o Options) timeout() time.Duration {
	if o.Timeout <= 0 {
		return 5 * time.Second
	}
	return o.Timeout
}

// Result is one health-check outcome. LatencyMs covers the stage that succeeded.
type Result struct {
	OK        bool   `json:"ok"`
	Stage     string `json:"stage"`
	LatencyMs int64  `json:"latencyMs"`
	Err       string `json:"error,omitempty"`
	ExitIP    string `json:"exitIp,omitempty"`
}

// ProbeTCP only proves the address accepts connections. A SOCKS proxy that is
// listening but rejecting auth still passes this, so it gates L2, not health.
func ProbeTCP(ctx context.Context, host string, port int, opt Options) Result {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, opt.timeout())
	defer cancel()
	conn, err := dialProxy(ctx, host, port, opt)
	if err != nil {
		return Result{Stage: StageTCP, Err: classify(err)}
	}
	_ = conn.Close()
	return Result{OK: true, Stage: StageTCP, LatencyMs: time.Since(start).Milliseconds()}
}

// Dial opens a tunnel to target through the SOCKS5 proxy. The returned
// connection has no deadline; callers set their own.
func Dial(ctx context.Context, host string, port int, auth Auth, target string, opt Options) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, opt.timeout())
	defer cancel()
	conn, err := dialProxy(ctx, host, port, opt)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := handshake(conn, auth); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := connect(conn, target); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// ProbeSOCKS performs the full handshake and CONNECT. target defaults to
// example.com:443; the reply proves the proxy can reach the public internet.
func ProbeSOCKS(ctx context.Context, host string, port int, auth Auth, target string, opt Options) Result {
	if target == "" {
		target = "example.com:443"
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, opt.timeout())
	defer cancel()

	conn, err := dialProxy(ctx, host, port, opt)
	if err != nil {
		return Result{Stage: StageTCP, Err: classify(err)}
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := handshake(conn, auth); err != nil {
		return Result{Stage: stageOf(err), Err: classify(err)}
	}
	if err := connect(conn, target); err != nil {
		return Result{Stage: stageOf(err), Err: classify(err)}
	}
	return Result{OK: true, Stage: StageSOCKS, LatencyMs: time.Since(start).Milliseconds()}
}

// HTTPGet issues one real HTTP(S) request through the proxy, capped at
// maxBody bytes, and extracts the exit IP from a Cloudflare trace body.
func HTTPGet(ctx context.Context, host string, port int, auth Auth, rawURL string, opt Options) Result {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, opt.timeout())
	defer cancel()

	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Result{Stage: StageHTTP, Err: ErrHTTP}
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dialProxy(ctx, host, port, opt)
			if err != nil {
				return nil, err
			}
			if deadline, ok := ctx.Deadline(); ok {
				_ = conn.SetDeadline(deadline)
			}
			if err := handshake(conn, auth); err != nil {
				_ = conn.Close()
				return nil, err
			}
			if err := connect(conn, addr); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return conn, nil
		},
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Transport: transport,
		Timeout:   opt.timeout(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Result{Stage: StageHTTP, Err: ErrHTTP}
	}
	resp, err := client.Do(req)
	if err != nil {
		stage := StageHTTP
		if errors.Is(err, netsafe.ErrPrivateAddressBlocked) || errors.Is(err, context.DeadlineExceeded) {
			stage = StageTCP
		}
		return Result{Stage: stage, Err: classify(err)}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 500 {
		return Result{Stage: StageHTTP, Err: ErrHTTP}
	}
	return Result{
		OK:        true,
		Stage:     StageHTTP,
		LatencyMs: time.Since(start).Milliseconds(),
		ExitIP:    parseTraceIP(string(body)),
	}
}

func parseTraceIP(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "ip="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func dialProxy(ctx context.Context, host string, port int, opt Options) (net.Conn, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	if opt.AllowPrivate {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}
	return netsafe.SSRFGuardedDialContext(ctx, "tcp", addr)
}

// handshake runs greeting + optional RFC1929 auth, leaving the conn ready for
// a CONNECT request.
func handshake(conn net.Conn, auth Auth) error {
	methods := []byte{0x00}
	if auth.Username != "" {
		methods = []byte{0x00, 0x02}
	}
	greeting := append([]byte{0x05, byte(len(methods))}, methods...)
	if _, err := conn.Write(greeting); err != nil {
		return fmt.Errorf("socks: greeting: %w", err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return fmt.Errorf("socks: greeting reply: %w", err)
	}
	if reply[0] != 0x05 {
		return errors.New("socks: bad version")
	}
	switch reply[1] {
	case 0x00:
		return nil
	case 0x02:
		if auth.Username == "" {
			return fmt.Errorf("socks: %w", errAuthRequired)
		}
		return authRFC1929(conn, auth)
	default:
		return fmt.Errorf("socks: %w", errNoAcceptableMethod)
	}
}

func authRFC1929(conn net.Conn, auth Auth) error {
	if len(auth.Username) > 255 || len(auth.Password) > 255 {
		return fmt.Errorf("socks: %w", errAuthRejected)
	}
	buf := make([]byte, 0, 3+len(auth.Username)+len(auth.Password))
	buf = append(buf, 0x01, byte(len(auth.Username)))
	buf = append(buf, auth.Username...)
	buf = append(buf, byte(len(auth.Password)))
	buf = append(buf, auth.Password...)
	if _, err := conn.Write(buf); err != nil {
		return fmt.Errorf("socks: auth: %w", err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return fmt.Errorf("socks: auth reply: %w", err)
	}
	if reply[1] != 0x00 {
		return fmt.Errorf("socks: %w", errAuthRejected)
	}
	return nil
}

func connect(conn net.Conn, target string) error {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return fmt.Errorf("socks: target: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("socks: target port invalid")
	}
	req := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 0x01)
			req = append(req, v4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return errors.New("socks: target host too long")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		return fmt.Errorf("socks: connect: %w", err)
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return fmt.Errorf("socks: connect reply: %w", err)
	}
	if head[0] != 0x05 {
		return errors.New("socks: bad connect version")
	}
	if head[1] != 0x00 {
		return fmt.Errorf("socks: %w", errConnectRefused)
	}
	// Drain the bound address so the caller sees the tunnel's first byte.
	var skip int
	switch head[3] {
	case 0x01:
		skip = 4
	case 0x04:
		skip = 16
	case 0x03:
		one := make([]byte, 1)
		if _, err := io.ReadFull(conn, one); err != nil {
			return fmt.Errorf("socks: connect reply: %w", err)
		}
		skip = int(one[0])
	default:
		return errors.New("socks: unknown address type")
	}
	if _, err := io.CopyN(io.Discard, conn, int64(skip+2)); err != nil {
		return fmt.Errorf("socks: connect reply: %w", err)
	}
	return nil
}

var (
	errAuthRequired       = errors.New("auth required")
	errAuthRejected       = errors.New("auth rejected")
	errNoAcceptableMethod = errors.New("no acceptable auth method")
	errConnectRefused     = errors.New("connect refused")
)

func stageOf(err error) string {
	switch {
	case errors.Is(err, errAuthRequired), errors.Is(err, errAuthRejected), errors.Is(err, errNoAcceptableMethod):
		return StageAuth
	case errors.Is(err, errConnectRefused):
		return StageSOCKS
	default:
		return StageSOCKS
	}
}

// classify maps transport errors to stable, credential-free codes.
func classify(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, netsafe.ErrPrivateAddressBlocked):
		return ErrBlocked
	case errors.Is(err, context.DeadlineExceeded):
		return ErrTimeout
	case errors.Is(err, errAuthRequired), errors.Is(err, errAuthRejected), errors.Is(err, errNoAcceptableMethod):
		return ErrAuth
	case errors.Is(err, errConnectRefused):
		return ErrConnect
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrTimeout
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return ErrRefused
	}
	return ErrConnect
}

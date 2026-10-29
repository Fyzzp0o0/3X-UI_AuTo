package chain

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
)

// TransportOptions carries the per-network knobs shared by ws/grpc/xhttp.
type TransportOptions struct {
	Path string `json:"path"`
	Host string `json:"host"`
}

// TLSOptions points at operator-provided certificate files.
type TLSOptions struct {
	CertificateFile string   `json:"certificateFile"`
	KeyFile         string   `json:"keyFile"`
	ServerName      string   `json:"serverName"`
	ALPN            []string `json:"alpn,omitempty"`
}

// RealityOptions describes a REALITY inbound. Keys are generated at pool
// creation when PrivateKey is empty.
type RealityOptions struct {
	Dest        string   `json:"dest"`
	ServerNames []string `json:"serverNames"`
	ShortIds    []string `json:"shortIds"`
	PrivateKey  string   `json:"privateKey"`
	PublicKey   string   `json:"publicKey"`
	Fingerprint string   `json:"fingerprint"`
	SpiderX     string   `json:"spiderX"`
}

const (
	SecurityNone    = "none"
	SecurityTLS     = "tls"
	SecurityReality = "reality"

	NetworkTCP         = "tcp"
	NetworkWS          = "ws"
	NetworkGRPC        = "grpc"
	NetworkHTTPUpgrade = "httpupgrade"
	NetworkXHTTP       = "xhttp"
)

var (
	validSecurity = map[string]bool{SecurityNone: true, SecurityTLS: true, SecurityReality: true}
	validNetwork  = map[string]bool{
		NetworkTCP: true, NetworkWS: true, NetworkGRPC: true,
		NetworkHTTPUpgrade: true, NetworkXHTTP: true,
	}
)

// normalizeInboundTemplate fills defaults and generates REALITY material when
// the operator left it blank. It is called once, before the pool is stored.
func normalizeInboundTemplate(t *InboundTemplate) error {
	if t.Protocol == "" {
		t.Protocol = "vless"
	}
	if t.Protocol != "vless" {
		return fmt.Errorf("only vless inbounds are supported, got %q", t.Protocol)
	}
	if t.Network == "" {
		t.Network = NetworkTCP
	}
	if t.Security == "" {
		t.Security = SecurityNone
	}
	if t.BasePort == 0 {
		t.BasePort = 2083
	}
	if t.PortEnd != 0 && t.PortEnd < t.BasePort {
		return errors.New("portEnd must be greater than or equal to basePort")
	}
	if !validSecurity[t.Security] {
		return fmt.Errorf("security must be none, tls or reality, got %q", t.Security)
	}
	if !validNetwork[t.Network] {
		return fmt.Errorf("network must be tcp, ws, grpc, httpupgrade or xhttp, got %q", t.Network)
	}
	switch t.Security {
	case SecurityTLS:
		if t.TLS.CertificateFile == "" || t.TLS.KeyFile == "" {
			return errors.New("tls requires certificateFile and keyFile")
		}
		for _, path := range []string{t.TLS.CertificateFile, t.TLS.KeyFile} {
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("tls file %s: %w", path, err)
			}
		}
	case SecurityReality:
		if err := normalizeReality(&t.Reality); err != nil {
			return err
		}
	}
	if t.Network == NetworkGRPC && strings.TrimSpace(t.Transport.Path) == "" {
		return errors.New("grpc requires a service name")
	}
	return nil
}

func normalizeReality(r *RealityOptions) error {
	host, port, err := net.SplitHostPort(r.Dest)
	if err != nil || host == "" || port == "" {
		return errors.New("reality dest must be host:port, e.g. www.microsoft.com:443")
	}
	if len(r.ServerNames) == 0 {
		r.ServerNames = []string{host}
	}
	for _, name := range r.ServerNames {
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, " /\\") {
			return errors.New("reality serverNames contains an invalid name")
		}
	}
	if len(r.ShortIds) == 0 {
		ids, err := generateShortIds(4)
		if err != nil {
			return err
		}
		r.ShortIds = ids
	}
	for _, id := range r.ShortIds {
		if id == "" {
			continue
		}
		if len(id)%2 != 0 || len(id) > 16 {
			return fmt.Errorf("reality shortId %q must be hex with an even length up to 16", id)
		}
		if _, err := hex.DecodeString(id); err != nil {
			return fmt.Errorf("reality shortId %q is not hex", id)
		}
	}
	if r.PrivateKey == "" || r.PublicKey == "" {
		priv, pub, err := generateX25519Pair()
		if err != nil {
			return err
		}
		r.PrivateKey, r.PublicKey = priv, pub
	}
	if r.Fingerprint == "" {
		r.Fingerprint = "chrome"
	}
	if r.SpiderX == "" {
		r.SpiderX = "/"
	}
	return nil
}

// buildStreamSettings renders the inbound streamSettings JSON used by Xray and
// by the subscription link builder.
func buildStreamSettings(t InboundTemplate) (string, error) {
	stream := map[string]any{"network": t.Network, "security": t.Security}
	switch t.Network {
	case NetworkWS:
		ws := map[string]any{"path": defaultPath(t.Transport.Path, "/ws")}
		if t.Transport.Host != "" {
			ws["headers"] = map[string]any{"Host": t.Transport.Host}
		}
		stream["wsSettings"] = ws
	case NetworkGRPC:
		stream["grpcSettings"] = map[string]any{"serviceName": t.Transport.Path}
	case NetworkHTTPUpgrade:
		hu := map[string]any{"path": defaultPath(t.Transport.Path, "/")}
		if t.Transport.Host != "" {
			hu["host"] = t.Transport.Host
		}
		stream["httpupgradeSettings"] = hu
	case NetworkXHTTP:
		xhttp := map[string]any{"path": defaultPath(t.Transport.Path, "/"), "mode": "auto"}
		if t.Transport.Host != "" {
			xhttp["host"] = t.Transport.Host
		}
		stream["xhttpSettings"] = xhttp
	}
	switch t.Security {
	case SecurityTLS:
		tls := map[string]any{
			"certificates": []any{map[string]any{
				"certificateFile": t.TLS.CertificateFile,
				"keyFile":         t.TLS.KeyFile,
			}},
		}
		if t.TLS.ServerName != "" {
			tls["serverName"] = t.TLS.ServerName
		}
		if len(t.TLS.ALPN) > 0 {
			tls["alpn"] = t.TLS.ALPN
		}
		stream["tlsSettings"] = tls
	case SecurityReality:
		stream["realitySettings"] = map[string]any{
			"show":        false,
			"xver":        0,
			"dest":        t.Reality.Dest,
			"serverNames": t.Reality.ServerNames,
			"privateKey":  t.Reality.PrivateKey,
			"shortIds":    t.Reality.ShortIds,
			"settings": map[string]any{
				"publicKey":   t.Reality.PublicKey,
				"fingerprint": t.Reality.Fingerprint,
				"serverName":  firstOrEmpty(t.Reality.ServerNames),
				"spiderX":     t.Reality.SpiderX,
			},
		}
	}
	raw, err := json.Marshal(stream)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func defaultPath(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	if !strings.HasPrefix(value, "/") {
		return "/" + value
	}
	return value
}

func firstOrEmpty(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func generateX25519Pair() (privateKey, publicKey string, err error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	priv := base64.RawURLEncoding.EncodeToString(key.Bytes())
	pub := base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
	return priv, pub, nil
}

func generateShortIds(count int) ([]string, error) {
	out := make([]string, 0, count)
	buf := make([]byte, 4)
	for i := 0; i < count; i++ {
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		out = append(out, hex.EncodeToString(buf))
	}
	return out, nil
}

// allocateShardPorts reserves consecutive free ports for shards. Existing
// shard tags keep their stored port; only missing shards consume the range.
func allocateShardPorts(base, end int, count int, used map[int]struct{}) ([]int, error) {
	if count == 0 {
		return nil, nil
	}
	if base < 1 || base > 65535 {
		return nil, errors.New("basePort must be 1-65535")
	}
	ports := make([]int, 0, count)
	port := base
	for len(ports) < count {
		if port > 65535 {
			return nil, fmt.Errorf("no free ports above %d", base)
		}
		if end > 0 && port > end {
			return nil, fmt.Errorf("port range %d-%d is exhausted", base, end)
		}
		if _, taken := used[port]; !taken {
			ports = append(ports, port)
			used[port] = struct{}{}
		}
		port++
	}
	return ports, nil
}

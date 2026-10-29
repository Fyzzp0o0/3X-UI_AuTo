package chain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildStreamSettingsNone(t *testing.T) {
	tpl := InboundTemplate{Protocol: "vless", BasePort: 20000}
	if err := normalizeInboundTemplate(&tpl); err != nil {
		t.Fatal(err)
	}
	raw, err := buildStreamSettings(tpl)
	if err != nil {
		t.Fatal(err)
	}
	if raw != `{"network":"tcp","security":"none"}` {
		t.Fatalf("stream = %s", raw)
	}
}

func TestBuildStreamSettingsWebSocket(t *testing.T) {
	tpl := InboundTemplate{
		Protocol: "vless", BasePort: 20000, Network: NetworkWS,
		Transport: TransportOptions{Path: "chain", Host: "cdn.example.com"},
	}
	if err := normalizeInboundTemplate(&tpl); err != nil {
		t.Fatal(err)
	}
	raw, err := buildStreamSettings(tpl)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	ws, ok := doc["wsSettings"].(map[string]any)
	if !ok {
		t.Fatalf("wsSettings missing: %s", raw)
	}
	if ws["path"] != "/chain" {
		t.Fatalf("path = %v", ws["path"])
	}
	headers, _ := ws["headers"].(map[string]any)
	if headers["Host"] != "cdn.example.com" {
		t.Fatalf("host = %v", headers["Host"])
	}
}

func TestBuildStreamSettingsRealityGeneratesKeys(t *testing.T) {
	tpl := InboundTemplate{
		Protocol: "vless", BasePort: 20000, Security: SecurityReality,
		Reality: RealityOptions{Dest: "www.microsoft.com:443", ServerNames: []string{"www.microsoft.com"}},
	}
	if err := normalizeInboundTemplate(&tpl); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(tpl.Reality.PrivateKey) == 0 || len(tpl.Reality.PublicKey) == 0 {
		t.Fatal("reality keys were not generated")
	}
	if len(tpl.Reality.ShortIds) != 4 {
		t.Fatalf("shortIds = %v", tpl.Reality.ShortIds)
	}
	raw, err := buildStreamSettings(tpl)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Reality struct {
			Dest        string   `json:"dest"`
			ServerNames []string `json:"serverNames"`
			PrivateKey  string   `json:"privateKey"`
			ShortIds    []string `json:"shortIds"`
			Settings    struct {
				PublicKey string `json:"publicKey"`
			} `json:"settings"`
		} `json:"realitySettings"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Reality.Dest != "www.microsoft.com:443" || doc.Reality.PrivateKey == "" {
		t.Fatalf("realitySettings = %s", raw)
	}
	if doc.Reality.Settings.PublicKey == "" {
		t.Fatalf("publicKey missing from settings: %s", raw)
	}
}

func TestBuildStreamSettingsTLS(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	for _, path := range []string{certPath, keyPath} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tpl := InboundTemplate{
		Protocol: "vless", BasePort: 20000, Security: SecurityTLS,
		TLS: TLSOptions{CertificateFile: certPath, KeyFile: keyPath, ServerName: "example.com"},
	}
	if err := normalizeInboundTemplate(&tpl); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	raw, err := buildStreamSettings(tpl)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"certificateFile":"`+certPath+`"`) || !strings.Contains(raw, `"serverName":"example.com"`) {
		t.Fatalf("tlsSettings missing pieces: %s", raw)
	}
}

func TestNormalizeInboundTemplateRejects(t *testing.T) {
	cases := []struct {
		name string
		tpl  InboundTemplate
	}{
		{"bad security", InboundTemplate{BasePort: 20000, Security: "xtls"}},
		{"bad network", InboundTemplate{BasePort: 20000, Network: "quic"}},
		{"tls without files", InboundTemplate{BasePort: 20000, Security: SecurityTLS}},
		{"tls missing file", InboundTemplate{
			BasePort: 20000, Security: SecurityTLS,
			TLS: TLSOptions{CertificateFile: "/nonexistent/cert.pem", KeyFile: "/nonexistent/key.pem"},
		}},
		{"reality without dest", InboundTemplate{BasePort: 20000, Security: SecurityReality}},
		{"reality dest without port", InboundTemplate{
			BasePort: 20000, Security: SecurityReality,
			Reality: RealityOptions{Dest: "www.microsoft.com"},
		}},
		{"reality bad short id", InboundTemplate{
			BasePort: 20000, Security: SecurityReality,
			Reality: RealityOptions{Dest: "www.microsoft.com:443", ShortIds: []string{"xyz"}},
		}},
		{"grpc without service", InboundTemplate{BasePort: 20000, Network: NetworkGRPC}},
		{"bad port range", InboundTemplate{BasePort: 20000, PortEnd: 19000}},
		{"unsupported protocol", InboundTemplate{Protocol: "trojan", BasePort: 20000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tpl := tc.tpl
			if err := normalizeInboundTemplate(&tpl); err == nil {
				t.Fatalf("expected an error for %+v", tc.tpl)
			}
		})
	}
}

func TestAllocateShardPorts(t *testing.T) {
	used := map[int]struct{}{20000: {}, 20002: {}}
	ports, err := allocateShardPorts(20000, 0, 3, used)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{20001, 20003, 20004}
	for i := range want {
		if ports[i] != want[i] {
			t.Fatalf("ports = %v, want %v", ports, want)
		}
	}
	if _, err := allocateShardPorts(20000, 20002, 3, map[int]struct{}{20000: {}, 20001: {}, 20002: {}}); err == nil {
		t.Fatal("expected range exhaustion")
	}
	if ports, err := allocateShardPorts(20000, 0, 0, used); err != nil || ports != nil {
		t.Fatalf("zero shards = %v, %v", ports, err)
	}
}

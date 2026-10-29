package inject

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/chain/linkparse"
	"github.com/mhsanaei/3x-ui/v3/internal/chain/socksprobe"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// TestChainEndToEnd drives a real Xray core: two VLESS clients, one active
// node routed to a marker SOCKS5 upstream, one gated node that must stay
// blocked. It is the only test that proves routing, the gate and per-client
// isolation on the real data plane.
func TestChainEndToEnd(t *testing.T) {
	bin := os.Getenv("XRAY_E2E_BINARY")
	if bin == "" {
		t.Skip("set XRAY_E2E_BINARY to the xray executable to run the end-to-end test")
	}
	markerA := startMarkerSocks(t, "proxy-A")
	serverPort := freePort(t)
	serverCfg := buildE2EServerConfig(t, serverPort, markerA.Port())
	startXray(t, bin, serverCfg, serverPort)

	uuidA := "11111111-1111-4111-8111-111111111111"
	uuidB := "22222222-2222-4222-8222-222222222222"
	socksA := freePort(t)
	socksB := freePort(t)
	startXray(t, bin, e2eClientConfig(serverPort, uuidA, socksA), socksA)
	startXray(t, bin, e2eClientConfig(serverPort, uuidB, socksB), socksB)

	marker, err := fetchThroughSocks(socksA, "example.test:80")
	if err != nil {
		t.Fatalf("active node request failed: %v", err)
	}
	if marker != "proxy-A" {
		t.Fatalf("active node reached %q, want proxy-A", marker)
	}

	refused, err := fetchThroughSocks(socksB, "example.test:80")
	if err == nil && refused != "" {
		t.Fatalf("gated node returned %q; it must not reach any upstream", refused)
	}
	if refused == "proxy-A" {
		t.Fatal("gated node leaked into the active node's chain")
	}
}

// buildE2EServerConfig starts from a stock-shaped config and runs the
// production injector so the test exercises Build/Merge, not a hand-written
// config.
func buildE2EServerConfig(t *testing.T, port int, markerPort int) string {
	t.Helper()
	const tmpl = `{
	  "log": {"loglevel": "warning"},
	  "inbounds": [{
	    "listen": "127.0.0.1", "port": %d, "protocol": "vless", "tag": "in-e2e",
	    "settings": {"clients": [
	      {"id": "11111111-1111-4111-8111-111111111111", "email": "a@chain"},
	      {"id": "22222222-2222-4222-8222-222222222222", "email": "b@chain"}
	    ], "decryption": "none"},
	    "streamSettings": {"network": "tcp", "security": "none"}
	  }],
	  "outbounds": [
	    {"protocol": "freedom", "settings": {}, "tag": "direct"},
	    {"protocol": "blackhole", "settings": {}, "tag": "blocked"}
	  ],
	  "routing": {"domainStrategy": "AsIs", "rules": [
	    {"type": "field", "ip": ["192.0.2.1/32"], "outboundTag": "blocked"}
	  ]}
	}`
	cfg := &xray.Config{}
	if err := json.Unmarshal([]byte(fmt.Sprintf(tmpl, port)), cfg); err != nil {
		t.Fatalf("parse template: %v", err)
	}
	active := Node{
		Id: 1, Email: "a@chain", InboundTag: "in-e2e", OutboundTag: "cf:p1:n001", Status: "active",
		Hops: []Hop{{Tag: "cf:p1:n001", Proxy: &linkparse.Proxy{
			Protocol: linkparse.ProtoSocks5, Host: "127.0.0.1", Port: markerPort,
		}}},
	}
	gated := Node{
		Id: 2, Email: "b@chain", InboundTag: "in-e2e", OutboundTag: "cf:p1:n002", Status: "provisioning",
	}
	outbounds, rules, err := Build([]Node{active, gated})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := MergeConfig(cfg, outbounds, rules, AfterBlocks); err != nil {
		t.Fatalf("merge: %v", err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return string(raw)
}

func e2eClientConfig(serverPort int, uuid string, socksPort int) string {
	return fmt.Sprintf(`{
	  "log": {"loglevel": "warning"},
	  "inbounds": [{
	    "listen": "127.0.0.1", "port": %d, "protocol": "socks", "tag": "socks-in",
	    "settings": {"auth": "noauth", "udp": false}
	  }],
	  "outbounds": [{
	    "protocol": "vless", "tag": "vless-out",
	    "settings": {"vnext": [{
	      "address": "127.0.0.1", "port": %d,
	      "users": [{"id": "%s", "encryption": "none"}]
	    }]},
	    "streamSettings": {"network": "tcp", "security": "none"}
	  }]
	}`, socksPort, serverPort, uuid)
}

// markerSocks answers CONNECT immediately and then speaks HTTP itself, so the
// marker proves the request travelled through this exact upstream. A fallback
// to the default outbound cannot produce it: example.test never resolves.
type markerSocks struct {
	ln     net.Listener
	marker string
}

func startMarkerSocks(t *testing.T, marker string) *markerSocks {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	m := &markerSocks{ln: ln, marker: marker}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go m.handle(conn)
		}
	}()
	return m
}

func (m *markerSocks) Port() int { return m.ln.Addr().(*net.TCPAddr).Port }

func (m *markerSocks) handle(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	head := make([]byte, 2)
	if _, err := io.ReadFull(br, head); err != nil {
		return
	}
	if _, err := io.ReadFull(br, make([]byte, int(head[1]))); err != nil {
		return
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil {
		return
	}
	switch req[3] {
	case 0x01:
		if _, err := io.ReadFull(br, make([]byte, 4)); err != nil {
			return
		}
	case 0x04:
		if _, err := io.ReadFull(br, make([]byte, 16)); err != nil {
			return
		}
	case 0x03:
		n, err := br.ReadByte()
		if err != nil {
			return
		}
		if _, err := io.ReadFull(br, make([]byte, int(n))); err != nil {
			return
		}
	}
	if _, err := io.ReadFull(br, make([]byte, 2)); err != nil {
		return
	}
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	if _, err := readHTTPHead(br); err != nil {
		return
	}
	body := m.marker
	_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		len(body), body)
}

func readHTTPHead(br *bufio.Reader) ([]byte, error) {
	var buf bytes.Buffer
	for {
		line, err := br.ReadBytes('\n')
		buf.Write(line)
		if err != nil {
			return buf.Bytes(), err
		}
		if len(bytes.TrimRight(line, "\r\n")) == 0 {
			return buf.Bytes(), nil
		}
	}
}

func fetchThroughSocks(socksPort int, target string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := socksprobe.Dial(ctx, "127.0.0.1", socksPort, socksprobe.Auth{}, target,
		socksprobe.Options{AllowPrivate: true, Timeout: 5 * time.Second})
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(6 * time.Second))
	_, _ = fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: example.test\r\nConnection: close\r\n\r\n")
	raw, err := io.ReadAll(io.LimitReader(conn, 4096))
	if err != nil {
		return string(raw), err
	}
	parts := strings.SplitN(string(raw), "\r\n\r\n", 2)
	if len(parts) != 2 {
		return string(raw), fmt.Errorf("no HTTP body in %q", raw)
	}
	return strings.TrimSpace(parts[1]), nil
}

func startXray(t *testing.T, bin, config string, readyPort int) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cmd := exec.Command(bin, "run", "-config", path)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start xray: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(readyPort)), 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("xray did not listen on %d; output:\n%s", readyPort, out.String())
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

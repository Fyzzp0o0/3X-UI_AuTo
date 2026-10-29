package socksprobe

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

// fakeSocks is a minimal SOCKS5 server that tunnels CONNECT to the requested
// target, so the probe's real handshake and HTTP path run end to end.
type fakeSocks struct {
	ln          net.Listener
	requireAuth bool
	user, pass  string
}

func startFake(t *testing.T, requireAuth bool, user, pass string) *fakeSocks {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSocks{ln: ln, requireAuth: requireAuth, user: user, pass: pass}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		_ = ln.Close()
		wg.Wait()
	})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				f.handle(conn)
			}()
		}
	}()
	return f
}

func (f *fakeSocks) addr() (string, int) {
	a := f.ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

func (f *fakeSocks) handle(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)

	head := make([]byte, 2)
	if _, err := io.ReadFull(br, head); err != nil || head[0] != 0x05 {
		return
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	want := byte(0x00)
	if f.requireAuth {
		want = 0x02
	}
	if !containsByte(methods, want) {
		_, _ = conn.Write([]byte{0x05, 0xff})
		return
	}
	if _, err := conn.Write([]byte{0x05, want}); err != nil {
		return
	}
	if f.requireAuth {
		if !f.readAuth(br, conn) {
			return
		}
	}

	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil || req[1] != 0x01 {
		return
	}
	host, err := readAddr(br, req[3])
	if err != nil {
		return
	}
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(br, portBuf); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(portBuf)

	upstream, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		_, _ = conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstream.Close()
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	go func() {
		_, _ = io.Copy(upstream, br)
		_ = upstream.(*net.TCPConn).CloseWrite()
	}()
	_, _ = io.Copy(conn, upstream)
}

func (f *fakeSocks) readAuth(br *bufio.Reader, conn net.Conn) bool {
	v := make([]byte, 2)
	if _, err := io.ReadFull(br, v); err != nil || v[0] != 0x01 {
		return false
	}
	user := make([]byte, int(v[1]))
	if _, err := io.ReadFull(br, user); err != nil {
		return false
	}
	plen, err := br.ReadByte()
	if err != nil {
		return false
	}
	pass := make([]byte, int(plen))
	if _, err := io.ReadFull(br, pass); err != nil {
		return false
	}
	ok := string(user) == f.user && string(pass) == f.pass
	status := byte(0x00)
	if !ok {
		status = 0x01
	}
	_, _ = conn.Write([]byte{0x01, status})
	return ok
}

func readAddr(br *bufio.Reader, atyp byte) (string, error) {
	switch atyp {
	case 0x01:
		b := make([]byte, 4)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", err
		}
		return net.IP(b).String(), nil
	case 0x04:
		b := make([]byte, 16)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", err
		}
		return net.IP(b).String(), nil
	case 0x03:
		n, err := br.ReadByte()
		if err != nil {
			return "", err
		}
		b := make([]byte, int(n))
		if _, err := io.ReadFull(br, b); err != nil {
			return "", err
		}
		return string(b), nil
	}
	return "", io.ErrUnexpectedEOF
}

func containsByte(b []byte, want byte) bool {
	for _, v := range b {
		if v == want {
			return true
		}
	}
	return false
}

func TestProbeTCP(t *testing.T) {
	f := startFake(t, false, "", "")
	host, port := f.addr()
	res := ProbeTCP(context.Background(), host, port, Options{AllowPrivate: true, Timeout: 2 * time.Second})
	if !res.OK || res.Stage != StageTCP {
		t.Fatalf("result = %+v", res)
	}
}

func TestProbeBlocksPrivateByDefault(t *testing.T) {
	f := startFake(t, false, "", "")
	host, port := f.addr()
	res := ProbeTCP(context.Background(), host, port, Options{Timeout: 2 * time.Second})
	if res.OK || res.Err != ErrBlocked {
		t.Fatalf("result = %+v, want blocked", res)
	}
}

func TestProbeSOCKSWithAuth(t *testing.T) {
	f := startFake(t, true, "u1", "p1")
	host, port := f.addr()
	target := startEchoTarget(t)
	opt := Options{AllowPrivate: true, Timeout: 2 * time.Second}

	res := ProbeSOCKS(context.Background(), host, port, Auth{Username: "u1", Password: "p1"}, target, opt)
	if !res.OK || res.Stage != StageSOCKS {
		t.Fatalf("result = %+v", res)
	}

	res = ProbeSOCKS(context.Background(), host, port, Auth{Username: "u1", Password: "wrong"}, target, opt)
	if res.OK || res.Err != ErrAuth {
		t.Fatalf("wrong password: %+v", res)
	}

	res = ProbeSOCKS(context.Background(), host, port, Auth{}, target, opt)
	if res.OK || res.Err != ErrAuth {
		t.Fatalf("missing credentials: %+v", res)
	}
}

func TestProbeSOCKSConnectRefused(t *testing.T) {
	f := startFake(t, false, "", "")
	host, port := f.addr()
	// A closed local port makes the fake's upstream dial fail, which it reports
	// as SOCKS reply 0x05.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().String()
	_ = ln.Close()

	res := ProbeSOCKS(context.Background(), host, port, Auth{}, closed, Options{AllowPrivate: true, Timeout: 2 * time.Second})
	if res.OK || res.Err != ErrConnect {
		t.Fatalf("result = %+v, want connect failure", res)
	}
}

func TestHTTPGetThroughProxyParsesExitIP(t *testing.T) {
	f := startFake(t, true, "u1", "p1")
	host, port := f.addr()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ip=203.0.113.9\nloc=HK\n"))
	}))
	defer srv.Close()

	res := HTTPGet(context.Background(), host, port, Auth{Username: "u1", Password: "p1"}, srv.URL,
		Options{AllowPrivate: true, Timeout: 3 * time.Second})
	if !res.OK || res.Stage != StageHTTP {
		t.Fatalf("result = %+v", res)
	}
	if res.ExitIP != "203.0.113.9" {
		t.Fatalf("exit ip = %q", res.ExitIP)
	}
}

func TestHTTPGetServerErrorIsFailure(t *testing.T) {
	f := startFake(t, false, "", "")
	host, port := f.addr()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	res := HTTPGet(context.Background(), host, port, Auth{}, srv.URL, Options{AllowPrivate: true, Timeout: 3 * time.Second})
	if res.OK || res.Err != ErrHTTP {
		t.Fatalf("result = %+v, want http failure", res)
	}
}

func startEchoTarget(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return ln.Addr().String()
}

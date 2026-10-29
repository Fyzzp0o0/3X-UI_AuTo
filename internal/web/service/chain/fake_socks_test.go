package chain

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
)

// fakeSocks is a minimal no-auth SOCKS5 server used by probe tests. It tunnels
// CONNECT to the requested target so ProbeSOCKS exercises the real handshake.
type fakeSocks struct {
	ln net.Listener
}

func startFakeSocks(t *testing.T) *fakeSocks {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSocks{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(conn)
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
	if _, err := io.ReadFull(br, head); err != nil {
		return
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil {
		return
	}
	var host string
	switch req[3] {
	case 0x01:
		b := make([]byte, 4)
		_, _ = io.ReadFull(br, b)
		host = net.IP(b).String()
	case 0x03:
		n, _ := br.ReadByte()
		b := make([]byte, int(n))
		_, _ = io.ReadFull(br, b)
		host = string(b)
	default:
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
	go func() { _, _ = io.Copy(upstream, br) }()
	_, _ = io.Copy(conn, upstream)
}

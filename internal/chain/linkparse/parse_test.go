package linkparse

import (
	"encoding/base64"
	"testing"
)

func TestParseLinesFormats(t *testing.T) {
	cases := []struct {
		name string
		line string
		want Proxy
	}{
		{"uri auth", "socks5://u1:p1@1.1.1.1:1080", Proxy{ProtoSocks5, "1.1.1.1", 1080, "u1", "p1", ""}},
		{"uri no auth", "socks5://1.1.1.1:1080", Proxy{ProtoSocks5, "1.1.1.1", 1080, "", "", ""}},
		{"uri fragment label", "socks5://u:p@1.1.1.1:1080#HK-01", Proxy{ProtoSocks5, "1.1.1.1", 1080, "u", "p", "HK-01"}},
		{"uri ipv6", "socks5://u:p@[2001:db8::1]:1080", Proxy{ProtoSocks5, "2001:db8::1", 1080, "u", "p", ""}},
		{"uri percent password", "socks5://u:p%40ss@1.1.1.1:1080", Proxy{ProtoSocks5, "1.1.1.1", 1080, "u", "p@ss", ""}},
		{"http scheme", "http://u:p@2.2.2.2:3128", Proxy{ProtoHTTP, "2.2.2.2", 3128, "u", "p", ""}},
		{"colon four", "1.1.1.1:1080:u1:p1", Proxy{ProtoSocks5, "1.1.1.1", 1080, "u1", "p1", ""}},
		{"colon three", "1.1.1.1:1080:u1", Proxy{ProtoSocks5, "1.1.1.1", 1080, "u1", "", ""}},
		{"colon two", "1.1.1.1:1080", Proxy{ProtoSocks5, "1.1.1.1", 1080, "", "", ""}},
		{"password keeps colons", "1.1.1.1:1080:u1:a:b", Proxy{ProtoSocks5, "1.1.1.1", 1080, "u1", "a:b", ""}},
		{"at form", "u1:p1@1.1.1.1:1080", Proxy{ProtoSocks5, "1.1.1.1", 1080, "u1", "p1", ""}},
		{"semicolon", "1.1.1.1;1080;u1;p1", Proxy{ProtoSocks5, "1.1.1.1", 1080, "u1", "p1", ""}},
		{"space four", "1.1.1.1 1080 u1 p1", Proxy{ProtoSocks5, "1.1.1.1", 1080, "u1", "p1", ""}},
		{"space two", "1.1.1.1 1080", Proxy{ProtoSocks5, "1.1.1.1", 1080, "", "", ""}},
		{"bracket ipv6 csv", "[2001:db8::2]:1080:u1:p1", Proxy{ProtoSocks5, "2001:db8::2", 1080, "u1", "p1", ""}},
		{"trailing label", "1.1.1.1:1080:u1:p1 #JP-02", Proxy{ProtoSocks5, "1.1.1.1", 1080, "u1", "p1", "JP-02"}},
		{"clash map", "- {name: hk1, type: socks5, server: 1.1.1.1, port: 1080, username: u1, password: p1}",
			Proxy{ProtoSocks5, "1.1.1.1", 1080, "u1", "p1", "hk1"}},
		{"domain host", "proxy.example.com:1080:u:p", Proxy{ProtoSocks5, "proxy.example.com", 1080, "u", "p", ""}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseLines(tc.line, Options{AllowPrivate: true})
			if len(got) != 1 {
				t.Fatalf("got %d results, want 1", len(got))
			}
			if got[0].Err != "" {
				t.Fatalf("line %q failed with %s", tc.line, got[0].Err)
			}
			if got[0].Proxy != tc.want {
				t.Fatalf("proxy = %+v, want %+v", got[0].Proxy, tc.want)
			}
		})
	}
}

func TestParseLinesBase64(t *testing.T) {
	raw := "socks5://u:p@1.1.1.1:1080"
	got := ParseLines(base64.StdEncoding.EncodeToString([]byte(raw)), Options{})
	if len(got) != 1 || got[0].Err != "" {
		t.Fatalf("base64 line: %+v", got)
	}
	if got[0].Proxy.Host != "1.1.1.1" || got[0].Proxy.Password != "p" {
		t.Fatalf("decoded proxy = %+v", got[0].Proxy)
	}
}

func TestParseLinesErrors(t *testing.T) {
	cases := []struct {
		name string
		line string
		code string
	}{
		{"port zero", "1.1.1.1:0", ErrPort},
		{"port high", "1.1.1.1:70000", ErrPort},
		{"port text", "1.1.1.1:abc", ErrPort},
		{"missing port", "socks5://1.1.1.1", ErrSyntax},
		{"empty host", ":1080", ErrHost},
		{"private ip", "10.0.0.1:1080", ErrPrivate},
		{"loopback", "127.0.0.1:1080", ErrPrivate},
		{"metadata", "169.254.169.254:80", ErrPrivate},
		{"localhost", "localhost:1080", ErrPrivate},
		{"ipv6 unbracketed", "2001:db8::1:1080", ErrSyntax},
		{"bad scheme", "vmess://abc@1.1.1.1:443", ErrUnsupported},
		{"bad host", "bad host:1080", ErrHost},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseLines(tc.line, Options{})
			if len(got) != 1 {
				t.Fatalf("got %d results, want 1", len(got))
			}
			if got[0].Err != tc.code {
				t.Fatalf("err = %q, want %q (line %q)", got[0].Err, tc.code, tc.line)
			}
		})
	}
}

func TestParseLinesPrivateOptIn(t *testing.T) {
	got := ParseLines("127.0.0.1:1080", Options{AllowPrivate: true})
	if len(got) != 1 || got[0].Err != "" {
		t.Fatalf("allow_private should accept loopback, got %+v", got)
	}
}

func TestParseLinesSkipsCommentsAndKeepsLineNumbers(t *testing.T) {
	input := "# pool A\n\nsocks5://u:p@1.1.1.1:1080\n// note\n2.2.2.2:1080\n"
	got := ParseLines(input, Options{})
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if got[0].LineNo != 3 || got[1].LineNo != 5 {
		t.Fatalf("line numbers = %d,%d, want 3,5", got[0].LineNo, got[1].LineNo)
	}
}

func TestParseLinesMasksPassword(t *testing.T) {
	got := ParseLines("socks5://u:supersecret@1.1.1.1:1080", Options{})
	if len(got) != 1 {
		t.Fatalf("got %d results", len(got))
	}
	if got[0].Proxy.Password != "supersecret" {
		t.Fatalf("password lost: %+v", got[0].Proxy)
	}
	if got[0].Line == "socks5://u:supersecret@1.1.1.1:1080" {
		t.Fatalf("display line still contains the password: %q", got[0].Line)
	}
}

func TestFingerprintIgnoresPasswordAndCase(t *testing.T) {
	a := Proxy{ProtoSocks5, "Example.com", 1080, "u", "p1", ""}
	b := Proxy{ProtoSocks5, "example.com", 1080, "u", "p2", ""}
	c := Proxy{ProtoSocks5, "example.com", 1080, "other", "p1", ""}
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatalf("password must not change the fingerprint")
	}
	if Fingerprint(a) == Fingerprint(c) {
		t.Fatalf("username must change the fingerprint")
	}
}

func TestDedupeDropsDuplicateFingerprints(t *testing.T) {
	got, dropped := Dedupe(ParseLines("1.1.1.1:1080:u:p\n1.1.1.1:1080:u:p2\n2.2.2.2:1080\n", Options{}))
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	if len(got) != 2 {
		t.Fatalf("kept %d, want 2", len(got))
	}
	if got[0].Proxy.Password != "p" {
		t.Fatalf("dedupe must keep the first occurrence, got %+v", got[0].Proxy)
	}
}

func FuzzParseLines(f *testing.F) {
	for _, seed := range []string{
		"socks5://u:p@1.1.1.1:1080", "1.1.1.1:1080", "[::1]:1080", "", "::::",
		"- {server: 1.1.1.1, port: 1080}", "dToxczovL3U6cEAxLjEuMS4xOjEwODA=",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		for _, r := range ParseLines(input, Options{AllowPrivate: true}) {
			if r.OK() && (r.Proxy.Port < 1 || r.Proxy.Port > 65535) {
				t.Fatalf("accepted proxy with invalid port: %+v", r.Proxy)
			}
		}
	})
}

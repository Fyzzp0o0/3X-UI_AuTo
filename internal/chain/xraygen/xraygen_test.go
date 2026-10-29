package xraygen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/chain/linkparse"
)

func TestSocksOutboundJSON(t *testing.T) {
	cases := []struct {
		name string
		in   SocksOutbound
		want string
	}{
		{
			name: "no auth",
			in:   SocksOutbound{Tag: "cf:p1:n001", Address: "1.1.1.1", Port: 1080},
			want: `{"tag":"cf:p1:n001","protocol":"socks","settings":{"servers":[{"address":"1.1.1.1","port":1080}]}}`,
		},
		{
			name: "auth",
			in:   SocksOutbound{Tag: "cf:p1:n001", Address: "1.1.1.1", Port: 1080, Username: "u", Password: "p"},
			want: `{"tag":"cf:p1:n001","protocol":"socks","settings":{"servers":[{"address":"1.1.1.1","port":1080,"users":[{"user":"u","pass":"p"}]}]}}`,
		},
		{
			name: "dialer",
			in:   SocksOutbound{Tag: "cf:p1:n001", Address: "1.1.1.1", Port: 1080, DialerProxy: "cf:p1:f:hk"},
			want: `{"tag":"cf:p1:n001","protocol":"socks","settings":{"servers":[{"address":"1.1.1.1","port":1080}]},"streamSettings":{"sockopt":{"dialerProxy":"cf:p1:f:hk"}}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.in.JSON()
			if err != nil {
				t.Fatalf("JSON: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestSocksOutboundRejects(t *testing.T) {
	cases := []struct {
		name string
		in   SocksOutbound
	}{
		{"reserved tag", SocksOutbound{Tag: TagDirect, Address: "1.1.1.1", Port: 1080}},
		{"empty tag", SocksOutbound{Tag: "", Address: "1.1.1.1", Port: 1080}},
		{"bad port", SocksOutbound{Tag: "cf:p1:n001", Address: "1.1.1.1", Port: 0}},
		{"bad address", SocksOutbound{Tag: "cf:p1:n001", Address: `1.1.1.1"`, Port: 1080}},
		{"self dialer", SocksOutbound{Tag: "cf:p1:n001", Address: "1.1.1.1", Port: 1080, DialerProxy: "cf:p1:n001"}},
		{"dialer reserved", SocksOutbound{Tag: "cf:p1:n001", Address: "1.1.1.1", Port: 1080, DialerProxy: TagBlocked}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.in.JSON(); err == nil {
				t.Fatalf("expected an error for %+v", tc.in)
			}
		})
	}
}

func TestTagAndEmailHelpers(t *testing.T) {
	if got := NodeTag(3, 17); got != "cf:p3:n017" {
		t.Fatalf("NodeTag = %q", got)
	}
	if got := FrontTag(3, "HK Relay"); got != "cf:p3:f:hk-relay" {
		t.Fatalf("FrontTag = %q", got)
	}
	if got := EmailFor(3, 17); got != "p3-n017@chain" {
		t.Fatalf("EmailFor = %q", got)
	}
	if err := ValidateEmail(EmailFor(3, 17)); err != nil {
		t.Fatalf("generated email invalid: %v", err)
	}
	for _, bad := range []string{"a/b", "a b", "a\nb", ""} {
		if err := ValidateEmail(bad); err == nil {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}

func TestBuildChainLinksDialerProxy(t *testing.T) {
	hops := []Hop{
		{Tag: "cf:p1:n001", Proxy: linkparse.Proxy{Protocol: "socks5", Host: "1.1.1.1", Port: 1080}},
		{Tag: "cf:p1:f:hk", Proxy: linkparse.Proxy{Protocol: "socks5", Host: "9.9.9.9", Port: 1080}},
	}
	got, err := BuildChain(hops)
	if err != nil {
		t.Fatalf("BuildChain: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d outbounds, want 2", len(got))
	}
	var landing, front struct {
		Tag            string `json:"tag"`
		StreamSettings *struct {
			Sockopt struct {
				DialerProxy string `json:"dialerProxy"`
			} `json:"sockopt"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(got[0], &landing); err != nil {
		t.Fatal(err)
	}
	if landing.StreamSettings == nil || landing.StreamSettings.Sockopt.DialerProxy != "cf:p1:f:hk" {
		t.Fatalf("landing must dial through the front hop: %s", got[0])
	}
	if err := json.Unmarshal(got[1], &front); err != nil {
		t.Fatal(err)
	}
	if front.StreamSettings != nil {
		t.Fatalf("front hop must not chain further: %s", got[1])
	}
}

func TestBuildChainSkipsExternalHops(t *testing.T) {
	hops := []Hop{
		{Tag: "cf:p1:n001", Proxy: linkparse.Proxy{Host: "1.1.1.1", Port: 1080}},
		{Tag: "my-existing-relay", External: true},
	}
	got, err := BuildChain(hops)
	if err != nil {
		t.Fatalf("BuildChain: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("external hop must not emit an outbound, got %d", len(got))
	}
	var landing struct {
		StreamSettings *struct {
			Sockopt struct {
				DialerProxy string `json:"dialerProxy"`
			} `json:"sockopt"`
		} `json:"streamSettings"`
	}
	_ = json.Unmarshal(got[0], &landing)
	if landing.StreamSettings == nil || landing.StreamSettings.Sockopt.DialerProxy != "my-existing-relay" {
		t.Fatalf("external hop must still be referenced: %s", got[0])
	}
}

func TestBuildChainRejectsBadTopology(t *testing.T) {
	cases := []struct {
		name string
		hops []Hop
	}{
		{"empty", nil},
		{"duplicate tags", []Hop{{Tag: "cf:p1:a"}, {Tag: "cf:p1:a"}}},
		{"too deep", []Hop{
			{Tag: "cf:p1:a"}, {Tag: "cf:p1:b"}, {Tag: "cf:p1:c"}, {Tag: "cf:p1:d"},
		}},
		{"invalid tag", []Hop{{Tag: "cf:p1:a b"}}},
		{"invalid proxy", []Hop{{Tag: "cf:p1:a", Proxy: linkparse.Proxy{Host: "", Port: 1080}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := BuildChain(tc.hops); err == nil {
				t.Fatalf("expected an error for %+v", tc.hops)
			}
		})
	}
}

func TestRules(t *testing.T) {
	raw, err := UserRule("p3-n017@chain", "cf:p3:n017")
	if err != nil {
		t.Fatalf("UserRule: %v", err)
	}
	if string(raw) != `{"type":"field","user":["p3-n017@chain"],"outboundTag":"cf:p3:n017"}` {
		t.Fatalf("user rule = %s", raw)
	}

	raw, err = InboundUserRule("in-443", "p3-n017@chain", "cf:p3:n017")
	if err != nil {
		t.Fatalf("InboundUserRule: %v", err)
	}
	var r Rule
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	if len(r.InboundTag) != 1 || r.InboundTag[0] != "in-443" || len(r.User) != 1 {
		t.Fatalf("inbound+user rule = %s", raw)
	}

	raw, err = BlackholeRule("p3-n017@chain")
	if err != nil {
		t.Fatalf("BlackholeRule: %v", err)
	}
	if !strings.Contains(string(raw), `"outboundTag":"blocked"`) {
		t.Fatalf("blackhole rule = %s", raw)
	}
}

func TestBuildChainIsDeterministic(t *testing.T) {
	hops := []Hop{
		{Tag: "cf:p1:n001", Proxy: linkparse.Proxy{Host: "1.1.1.1", Port: 1080, Username: "u", Password: "p"}},
		{Tag: "cf:p1:f:hk", Proxy: linkparse.Proxy{Host: "9.9.9.9", Port: 1080}},
	}
	a, err := BuildChain(hops)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildChain(hops)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Fatalf("length differs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if string(a[i]) != string(b[i]) {
			t.Fatalf("hop %d differs:\n%s\n%s", i, a[i], b[i])
		}
	}
}

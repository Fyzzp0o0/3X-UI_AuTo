package inject

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/chain/linkparse"
	"github.com/mhsanaei/3x-ui/v3/internal/chain/xraygen"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

const defaultConfigJSON = `{
  "api": {"services": ["HandlerService","LoggerService","StatsService","RoutingService"], "tag": "api"},
  "inbounds": [{"listen":"127.0.0.1","port":62789,"protocol":"tunnel","settings":{"rewriteAddress":"127.0.0.1"},"tag":"api"}],
  "log": {"loglevel": "warning"},
  "outbounds": [
    {"protocol":"freedom","settings":{"finalRules":[{"action":"block","ip":["geoip:private"]},{"action":"allow"}]},"tag":"direct"},
    {"protocol":"blackhole","settings":{},"tag":"blocked"}
  ],
  "policy": {"levels":{"0":{"statsUserDownlink":true,"statsUserUplink":true}},"system":{"statsInboundDownlink":true,"statsInboundUplink":true,"statsOutboundDownlink":false,"statsOutboundUplink":false}},
  "routing": {"domainStrategy":"AsIs","rules":[
    {"inboundTag":["api"],"outboundTag":"api","type":"field"},
    {"ip":["geoip:private"],"outboundTag":"blocked","type":"field"},
    {"outboundTag":"blocked","protocol":["bittorrent"],"type":"field"}
  ]},
  "stats": {}
}`

func parseConfig(t *testing.T, raw string) *xray.Config {
	t.Helper()
	cfg := &xray.Config{}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return cfg
}

func cloneConfig(t *testing.T, cfg *xray.Config) *xray.Config {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return parseConfig(t, string(raw))
}

func testNode(id int, status string) Node {
	return Node{
		Id:          id,
		Email:       xraygen.EmailFor(3, id),
		InboundTag:  "in-443",
		OutboundTag: xraygen.NodeTag(3, id),
		Status:      status,
		Hops: []Hop{{
			Tag:   xraygen.NodeTag(3, id),
			Proxy: &linkparse.Proxy{Protocol: "socks5", Host: "1.1.1.1", Port: 1080, Username: "u", Password: "p"},
		}},
	}
}

func TestBuildSeparatesGateAndActiveNodes(t *testing.T) {
	nodes := []Node{testNode(1, "active"), testNode(2, "provisioning"), testNode(3, "disabled")}
	outbounds, rules, err := Build(nodes)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(outbounds) != 2 || len(rules) != 2 {
		t.Fatalf("got %d outbounds / %d rules, want 2/2 (disabled skipped)", len(outbounds), len(rules))
	}
	if !strings.Contains(string(rules[0]), `"outboundTag":"cf:p3:n001"`) {
		t.Fatalf("active node rule = %s", rules[0])
	}
	if !strings.Contains(string(rules[1]), `"outboundTag":"blocked"`) {
		t.Fatalf("provisioning node must be gated to blocked, got %s", rules[1])
	}
	if !strings.Contains(string(rules[1]), `"inboundTag"`) {
		t.Fatalf("gate rule should still match the inbound: %s", rules[1])
	}
}

func TestBuildRejectsBadNodeState(t *testing.T) {
	cases := []struct {
		name  string
		nodes []Node
	}{
		{"duplicate email", []Node{testNode(1, "active"), testNode(1, "active")}},
		{"landing mismatch", func() []Node {
			n := testNode(1, "active")
			n.OutboundTag = "cf:p3:other"
			return []Node{n}
		}()},
		{"duplicate tag", func() []Node {
			a, b := testNode(1, "active"), testNode(2, "active")
			b.OutboundTag = a.OutboundTag
			b.Hops[0].Tag = a.OutboundTag
			return []Node{a, b}
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := Build(tc.nodes); err == nil {
				t.Fatalf("expected an error for %+v", tc.nodes)
			}
		})
	}
}

func TestMergeInsertsAfterBlockRules(t *testing.T) {
	cfg := parseConfig(t, defaultConfigJSON)
	nodes := []Node{testNode(1, "active"), testNode(2, "active")}
	outbounds, rules, err := Build(nodes)
	if err != nil {
		t.Fatal(err)
	}
	if err := MergeConfig(cfg, outbounds, rules, AfterBlocks); err != nil {
		t.Fatalf("MergeConfig: %v", err)
	}

	var routing struct {
		DomainStrategy string            `json:"domainStrategy"`
		Rules          []json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(cfg.RouterConfig, &routing); err != nil {
		t.Fatal(err)
	}
	if routing.DomainStrategy != "AsIs" {
		t.Fatalf("domainStrategy lost: %q", routing.DomainStrategy)
	}
	if len(routing.Rules) != 5 {
		t.Fatalf("rules = %d, want 5", len(routing.Rules))
	}
	// api, private, bittorrent, then the two chain rules.
	if !strings.Contains(string(routing.Rules[3]), `"cf:p3:n001"`) {
		t.Fatalf("first chain rule at index 3? got %s", routing.Rules[3])
	}
	if !strings.Contains(string(routing.Rules[4]), `"cf:p3:n002"`) {
		t.Fatalf("second chain rule at index 4? got %s", routing.Rules[4])
	}

	var outboundsDoc []struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(cfg.OutboundConfigs, &outboundsDoc); err != nil {
		t.Fatal(err)
	}
	if len(outboundsDoc) != 4 || outboundsDoc[0].Tag != "direct" || outboundsDoc[1].Tag != "blocked" {
		t.Fatalf("outbounds = %+v, want stock pair then two chain hops", outboundsDoc)
	}
}

func TestMergePlacementVariants(t *testing.T) {
	cases := []struct {
		placement Placement
		wantIndex int
	}{
		{High, 1},
		{Low, 3},
		{AfterBlocks, 3},
	}
	for _, tc := range cases {
		cfg := parseConfig(t, defaultConfigJSON)
		_, rules, err := Build([]Node{testNode(1, "active")})
		if err != nil {
			t.Fatal(err)
		}
		if err := MergeConfig(cfg, nil, rules, tc.placement); err != nil {
			t.Fatal(err)
		}
		var routing struct {
			Rules []json.RawMessage `json:"rules"`
		}
		if err := json.Unmarshal(cfg.RouterConfig, &routing); err != nil {
			t.Fatal(err)
		}
		if len(routing.Rules) != 4 || !strings.Contains(string(routing.Rules[tc.wantIndex]), "p3-n001@chain") {
			t.Fatalf("%s: chain rule not at %d: %v", tc.placement, tc.wantIndex, routing.Rules)
		}
	}
}

func TestMergeBootstrapsMissingSections(t *testing.T) {
	cfg := &xray.Config{}
	_, rules, err := Build([]Node{testNode(1, "active")})
	if err != nil {
		t.Fatal(err)
	}
	if err := MergeConfig(cfg, nil, rules, AfterBlocks); err != nil {
		t.Fatal(err)
	}
	var outbounds []struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
	}
	if err := json.Unmarshal(cfg.OutboundConfigs, &outbounds); err != nil {
		t.Fatal(err)
	}
	if len(outbounds) != 2 || outbounds[0].Tag != "direct" || outbounds[1].Tag != "blocked" {
		t.Fatalf("bootstrap outbounds = %+v", outbounds)
	}
	var routing struct {
		Rules []json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(cfg.RouterConfig, &routing); err != nil {
		t.Fatal(err)
	}
	if len(routing.Rules) != 1 {
		t.Fatalf("bootstrap rules = %v", routing.Rules)
	}
}

func TestMergeRejectsExistingTag(t *testing.T) {
	cfg := parseConfig(t, defaultConfigJSON)
	// Built by hand: ValidateTag already refuses this tag, so the merge path is
	// the last line of defence against hijacking the default outbound.
	raw := json.RawMessage(`{"tag":"direct","protocol":"socks","settings":{"servers":[]}}`)
	if err := MergeConfig(cfg, []json.RawMessage{raw}, nil, AfterBlocks); err == nil {
		t.Fatal("expected a duplicate-tag error for the existing direct tag")
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	nodes := []Node{testNode(2, "active"), testNode(1, "active")}
	a, ra, err := Build(nodes)
	if err != nil {
		t.Fatal(err)
	}
	b, rb, err := Build(nodes)
	if err != nil {
		t.Fatal(err)
	}
	for i := range a {
		if string(a[i]) != string(b[i]) {
			t.Fatalf("outbound %d differs", i)
		}
	}
	for i := range ra {
		if string(ra[i]) != string(rb[i]) {
			t.Fatalf("rule %d differs", i)
		}
	}
}

package inject

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// TestTwoHundredNodesHotApply is the M0 go/no-go check: adding 200 outbounds and
// 200 rules must be applicable through the core API without a restart, because
// a full restart per generation would drop every live connection.
func TestTwoHundredNodesHotApply(t *testing.T) {
	oldCfg := parseConfig(t, defaultConfigJSON)
	newCfg := cloneConfig(t, oldCfg)

	nodes := make([]Node, 0, 200)
	for i := 1; i <= 200; i++ {
		nodes = append(nodes, testNode(i, "active"))
	}
	outbounds, rules, err := Build(nodes)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := MergeConfig(newCfg, outbounds, rules, AfterBlocks); err != nil {
		t.Fatalf("MergeConfig: %v", err)
	}

	diff, ok := xray.ComputeHotDiff(oldCfg, newCfg)
	if !ok {
		t.Fatal("config change is not hot-applicable; 200 nodes would restart Xray")
	}
	if len(diff.AddedOutbounds) != 200 {
		t.Fatalf("added outbounds = %d, want 200", len(diff.AddedOutbounds))
	}
	if len(diff.RemovedOutboundTags) != 0 {
		t.Fatalf("unexpected removals: %v", diff.RemovedOutboundTags)
	}
	if diff.RoutingConfig == nil {
		t.Fatal("routing changes were not queued for ApplyRoutingConfig")
	}
}

// TestStockSectionsSurviveMerge guards the promise that injection never rewrites
// operator-owned sections: anything but outbounds and routing.rules must stay
// byte-identical so the hot diff keeps treating them as unchanged.
// sameJSON compares two documents semantically; json.Marshal compacts raw
// sections, so byte equality would fail for reasons the core does not care about.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var ca, cb bytes.Buffer
	if err := json.Compact(&ca, a); err != nil {
		t.Fatalf("compact a: %v", err)
	}
	if err := json.Compact(&cb, b); err != nil {
		t.Fatalf("compact b: %v", err)
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}

func TestStockSectionsSurviveMerge(t *testing.T) {
	oldCfg := parseConfig(t, defaultConfigJSON)
	newCfg := cloneConfig(t, oldCfg)
	_, rules, err := Build([]Node{testNode(1, "active")})
	if err != nil {
		t.Fatal(err)
	}
	if err := MergeConfig(newCfg, nil, rules, AfterBlocks); err != nil {
		t.Fatal(err)
	}
	if !sameJSON(t, oldCfg.LogConfig, newCfg.LogConfig) {
		t.Fatalf("log section changed")
	}
	if !sameJSON(t, oldCfg.API, newCfg.API) {
		t.Fatalf("api section changed")
	}
	if !sameJSON(t, oldCfg.Policy, newCfg.Policy) {
		t.Fatalf("policy section changed")
	}
	if len(oldCfg.InboundConfigs) != len(newCfg.InboundConfigs) {
		t.Fatalf("inbounds changed: %d vs %d", len(oldCfg.InboundConfigs), len(newCfg.InboundConfigs))
	}
}

// TestMergeIsIdempotentForEmptyBatch proves a reconcile run with nothing to do
// is a no-op rather than a config churn.
func TestMergeIsIdempotentForEmptyBatch(t *testing.T) {
	oldCfg := parseConfig(t, defaultConfigJSON)
	newCfg := cloneConfig(t, oldCfg)
	if err := MergeConfig(newCfg, nil, nil, AfterBlocks); err != nil {
		t.Fatal(err)
	}
	if !sameJSON(t, oldCfg.OutboundConfigs, newCfg.OutboundConfigs) ||
		!sameJSON(t, oldCfg.RouterConfig, newCfg.RouterConfig) {
		t.Fatal("empty merge changed the config")
	}
}

func TestMissingTableIsNotFatal(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{fmt.Errorf("no such table: chain_nodes"), true},
		{fmt.Errorf(`relation "chain_nodes" does not exist (SQLSTATE 42P01)`), true},
		{fmt.Errorf("no such table: inbounds"), false},
		{fmt.Errorf("database is locked"), false},
	}
	for _, tc := range cases {
		if got := isMissingChainTable(tc.err); got != tc.want {
			t.Fatalf("isMissingChainTable(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

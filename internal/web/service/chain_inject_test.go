package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/chain/xraygen"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// TestChainInjectionScaleAndHotApply is the M0 integration proof: 200 active
// nodes must appear in the generated config and the change must stay
// hot-applicable, because a restart would drop every live customer connection.
func TestChainInjectionScaleAndHotApply(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	db := database.GetDB()

	svc := &XrayService{}
	before, err := svc.GetXrayConfig()
	if err != nil {
		t.Fatalf("baseline GetXrayConfig: %v", err)
	}

	const nodes = 200
	pool := model.ChainPool{Name: "poc", Mode: "sharded", ShardSize: 40, Enabled: true, TagPrefix: "cf:p1"}
	if err := db.Create(&pool).Error; err != nil {
		t.Fatalf("create pool: %v", err)
	}
	proxies := make([]model.ChainProxy, 0, nodes)
	for i := 1; i <= nodes; i++ {
		proxies = append(proxies, model.ChainProxy{
			PoolId: pool.Id, Seq: i, Protocol: "socks5", Host: "198.51.100.9",
			Port: 10000 + i, Username: "u", PasswordEnc: "p",
			Fingerprint: fmt.Sprintf("poc-%03d", i), Enabled: true, Health: "healthy",
		})
	}
	if err := db.CreateInBatches(&proxies, 200).Error; err != nil {
		t.Fatalf("create proxies: %v", err)
	}
	nodeRows := make([]model.ChainNode, 0, nodes)
	for i := 1; i <= nodes; i++ {
		proxyID := proxies[i-1].Id
		tag := xraygen.NodeTag(1, i)
		nodeRows = append(nodeRows, model.ChainNode{
			PoolId: pool.Id, ProxyId: &proxyID, Name: fmt.Sprintf("p1-n%03d", i),
			Email: xraygen.EmailFor(1, i), InboundTag: "in-poc", OutboundTag: tag,
			Status: "active", ClientEnabled: true,
		})
	}
	if err := db.CreateInBatches(&nodeRows, 200).Error; err != nil {
		t.Fatalf("create nodes: %v", err)
	}
	hops := make([]model.ChainNodeHop, 0, nodes)
	for i := 1; i <= nodes; i++ {
		proxyID := proxies[i-1].Id
		hops = append(hops, model.ChainNodeHop{
			NodeId: nodeRows[i-1].Id, Position: 0, ProxyId: &proxyID,
			OutboundTag: xraygen.NodeTag(1, i), Kind: "landing",
		})
	}
	if err := db.CreateInBatches(&hops, 200).Error; err != nil {
		t.Fatalf("create hops: %v", err)
	}

	start := time.Now()
	after, err := svc.GetXrayConfig()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("GetXrayConfig with 200 chain nodes: %v", err)
	}
	t.Logf("GetXrayConfig with %d chain nodes took %s", nodes, elapsed)

	var outbounds []struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
	}
	if err := json.Unmarshal(after.OutboundConfigs, &outbounds); err != nil {
		t.Fatalf("outbounds: %v", err)
	}
	beforeCount := mustUnmarshalLen(t, before.OutboundConfigs)
	if len(outbounds) != beforeCount+nodes {
		t.Fatalf("outbounds = %d, want %d", len(outbounds), beforeCount+nodes)
	}
	var chainRules int
	var routing struct {
		Rules []json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(after.RouterConfig, &routing); err != nil {
		t.Fatalf("routing: %v", err)
	}
	for _, raw := range routing.Rules {
		if bytes.Contains(raw, []byte(`"cf:p1:n`)) {
			chainRules++
		}
	}
	if chainRules != nodes {
		t.Fatalf("chain rules = %d, want %d", chainRules, nodes)
	}

	diff, ok := xray.ComputeHotDiff(before, after)
	if !ok {
		t.Fatal("200-node injection is not hot-applicable")
	}
	if len(diff.AddedOutbounds) != nodes {
		t.Fatalf("hot diff added %d outbounds, want %d", len(diff.AddedOutbounds), nodes)
	}
	if diff.RoutingConfig == nil {
		t.Fatal("hot diff did not queue the routing replacement")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("config generation too slow for 200 nodes: %s", elapsed)
	}
}

func mustUnmarshalLen(t *testing.T, raw []byte) int {
	t.Helper()
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return len(list)
}

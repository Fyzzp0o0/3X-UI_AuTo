package chain

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/chain/secret"
	"github.com/mhsanaei/3x-ui/v3/internal/crypto/nodetoken"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

type fakeReconciler struct {
	calls     int
	err       error
	verifyErr error
}

func (f *fakeReconciler) Apply() error {
	f.calls++
	return f.err
}

// Verify answers like the core router would: the rule currently in the
// database decides the outbound. verifyErr simulates a missing rule.
func (f *fakeReconciler) Verify(_, email string) (string, error) {
	if f.verifyErr != nil {
		return "", f.verifyErr
	}
	var node model.ChainNode
	if err := database.GetDB().Select("outbound_tag").Where("email = ?", email).First(&node).Error; err != nil {
		return "", err
	}
	return node.OutboundTag, nil
}

func newTestService(t *testing.T) (*Service, *fakeReconciler) {
	t.Helper()
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	runtime.SetManager(runtime.NewManager(runtime.LocalDeps{APIPort: func() int { return 0 }}))
	t.Cleanup(func() { runtime.SetManager(nil) })
	rec := &fakeReconciler{}
	return &Service{
		Inbound:    &service.InboundService{},
		Client:     &service.ClientService{},
		Reconciler: rec,
	}, rec
}

// useEncryption pins the process-wide credential codec for one test.
func useEncryption(t *testing.T, on bool) {
	t.Helper()
	if on {
		ring := &nodetoken.Keyring{ActiveID: "k1", Keys: map[string][32]byte{"k1": {7}}}
		codec, err := nodetoken.NewCodec(nodetoken.ModeRequired, ring)
		if err != nil {
			t.Fatal(err)
		}
		nodetoken.Init(codec)
	} else {
		codec, err := nodetoken.NewCodec(nodetoken.ModeOff, nil)
		if err != nil {
			t.Fatal(err)
		}
		nodetoken.Init(codec)
	}
	t.Cleanup(func() {
		codec, _ := nodetoken.NewCodec(nodetoken.ModeOff, nil)
		nodetoken.Init(codec)
	})
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

func testPoolInput(t *testing.T, name string) PoolInput {
	t.Helper()
	return PoolInput{
		Name: name, Remark: "poc", Mode: "sharded", ShardSize: 2, Enabled: true,
		Inbound: InboundTemplate{Protocol: "vless", BasePort: freePort(t)},
		Client:  ClientTemplate{TotalGB: 10, Group: "chain"},
	}
}

var seedBatch int

func seedProxies(t *testing.T, poolID, n int, health string) []model.ChainProxy {
	t.Helper()
	seedBatch++
	rows := make([]model.ChainProxy, 0, n)
	for i := 1; i <= n; i++ {
		rows = append(rows, model.ChainProxy{
			PoolId: poolID, Seq: i, Protocol: "socks5", Host: "198.51.100.10",
			Port: 10000 + seedBatch*100 + i, Username: "u", PasswordEnc: "p",
			Fingerprint: "fp-" + strconv.Itoa(seedBatch) + "-" + strconv.Itoa(i),
			Enabled:     true, Health: health,
		})
	}
	if err := database.GetDB().CreateInBatches(&rows, 100).Error; err != nil {
		t.Fatalf("seed proxies: %v", err)
	}
	return rows
}

func TestPoolCRUD(t *testing.T) {
	svc, _ := newTestService(t)
	in := testPoolInput(t, "pool-a")
	pool, err := svc.CreatePool(in)
	if err != nil {
		t.Fatalf("CreatePool: %v", err)
	}
	if pool.TagPrefix != "cf:p1" {
		t.Fatalf("tag prefix = %q", pool.TagPrefix)
	}
	in.Remark = "updated"
	if err := svc.UpdatePool(pool.Id, in); err != nil {
		t.Fatalf("UpdatePool: %v", err)
	}
	got, err := svc.GetPool(pool.Id)
	if err != nil {
		t.Fatalf("GetPool: %v", err)
	}
	if got.Remark != "updated" || got.TagPrefix != "cf:p1" {
		t.Fatalf("pool after update = %+v", got)
	}
	pools, err := svc.ListPools()
	if err != nil || len(pools) != 1 {
		t.Fatalf("ListPools = %v, %v", pools, err)
	}
}

func TestImportDryRunThenPersist(t *testing.T) {
	svc, _ := newTestService(t)
	pool, err := svc.CreatePool(testPoolInput(t, "import"))
	if err != nil {
		t.Fatal(err)
	}
	// No passwords here: the fingerprint ignores them, and a password import
	// additionally requires credential encryption (covered separately).
	input := "socks5://1.1.1.1:1080\n1.1.1.1:1080\nsocks5://2.2.2.2:1080\n"
	dry, err := svc.ImportProxies(context.Background(), pool.Id, input, ImportOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.Valid != 2 || dry.Duplicate != 1 || dry.Created != 0 {
		t.Fatalf("dry run = %+v", dry)
	}
	var count int64
	database.GetDB().Model(&model.ChainProxy{}).Count(&count)
	if count != 0 {
		t.Fatalf("dry run wrote %d rows", count)
	}

	res, err := svc.ImportProxies(context.Background(), pool.Id, input, ImportOptions{})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 2 || res.Valid != 2 {
		t.Fatalf("import = %+v", res)
	}
	database.GetDB().Model(&model.ChainProxy{}).Count(&count)
	if count != 2 {
		t.Fatalf("rows = %d, want 2", count)
	}
	again, err := svc.ImportProxies(context.Background(), pool.Id, input, ImportOptions{})
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if again.Created != 0 {
		t.Fatalf("re-import created %d rows", again.Created)
	}
}

func TestImportWithPasswordRequiresEncryption(t *testing.T) {
	svc, _ := newTestService(t)
	pool, err := svc.CreatePool(testPoolInput(t, "enc"))
	if err != nil {
		t.Fatal(err)
	}
	useEncryption(t, false)
	if secret.Enabled() {
		t.Skip("test process has credential encryption enabled")
	}
	_, err = svc.ImportProxies(context.Background(), pool.Id, "socks5://u:p@1.1.1.1:1080", ImportOptions{})
	if err == nil || !strings.Contains(err.Error(), "encryption") {
		t.Fatalf("err = %v, want encryption requirement", err)
	}
	var count int64
	database.GetDB().Model(&model.ChainProxy{}).Count(&count)
	if count != 0 {
		t.Fatalf("refused import wrote %d rows", count)
	}
}

func TestImportSealsPasswordWhenEncryptionOn(t *testing.T) {
	svc, _ := newTestService(t)
	useEncryption(t, true)
	pool, err := svc.CreatePool(testPoolInput(t, "sealed"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ImportProxies(context.Background(), pool.Id, "socks5://u:secret@1.1.1.1:1080", ImportOptions{})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 1 {
		t.Fatalf("created = %d", res.Created)
	}
	var p model.ChainProxy
	if err := database.GetDB().First(&p).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.PasswordEnc, "enc:v1:") {
		t.Fatalf("password stored as %q, want ciphertext", p.PasswordEnc)
	}
	plain, err := secret.Open(p.Id, p.PasswordEnc)
	if err != nil || plain != "secret" {
		t.Fatalf("Open = %q, %v", plain, err)
	}
}

func TestPlanShiftsOccupiedBasePort(t *testing.T) {
	svc, _ := newTestService(t)
	port := freePort(t)
	if err := database.GetDB().Create(&model.Inbound{Tag: "taken", Port: port, Protocol: model.VLESS, Enable: true}).Error; err != nil {
		t.Fatal(err)
	}
	in := testPoolInput(t, "conflict")
	in.Inbound.BasePort = port
	pool, err := svc.CreatePool(in)
	if err != nil {
		t.Fatal(err)
	}
	seedProxies(t, pool.Id, 1, "healthy")

	plan, err := svc.Plan(pool.Id, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", plan.Conflicts)
	}
	if len(plan.Ports) != 1 || plan.Ports[0] == port {
		t.Fatalf("ports = %v, want a free port after %d", plan.Ports, port)
	}
}

func TestApplyCreatesClientsNodesAndConfig(t *testing.T) {
	svc, rec := newTestService(t)
	pool, err := svc.CreatePool(testPoolInput(t, "apply"))
	if err != nil {
		t.Fatal(err)
	}
	// ShardSize 2 with 3 proxies expects two shard inbounds.
	seedProxies(t, pool.Id, 3, "healthy")

	res, err := svc.Apply(context.Background(), pool.Id, ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Created != 3 || res.Activated != 3 {
		t.Fatalf("apply result = %+v", res)
	}
	if rec.calls != 2 {
		t.Fatalf("reconciler called %d times, want 2 (gate then activate)", rec.calls)
	}

	var inbounds []model.Inbound
	database.GetDB().Where("tag LIKE ?", "cf:p1:s%").Find(&inbounds)
	if len(inbounds) != 2 {
		t.Fatalf("shard inbounds = %d, want 2", len(inbounds))
	}
	var nodes []model.ChainNode
	database.GetDB().Find(&nodes)
	if len(nodes) != 3 {
		t.Fatalf("nodes = %d", len(nodes))
	}
	for _, n := range nodes {
		if n.Status != "active" || !n.ClientEnabled {
			t.Fatalf("node not active: %+v", n)
		}
	}
	var hops int64
	database.GetDB().Model(&model.ChainNodeHop{}).Count(&hops)
	if hops != 3 {
		t.Fatalf("hops = %d", hops)
	}

	cfg, err := (&service.XrayService{}).GetXrayConfig()
	if err != nil {
		t.Fatalf("GetXrayConfig: %v", err)
	}
	var outbounds []struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(cfg.OutboundConfigs, &outbounds); err != nil {
		t.Fatal(err)
	}
	chainOutbounds := 0
	for _, ob := range outbounds {
		if strings.HasPrefix(ob.Tag, "cf:p1:n") {
			chainOutbounds++
		}
	}
	if chainOutbounds != 3 {
		t.Fatalf("injected outbounds = %d, want 3", chainOutbounds)
	}
	var routing struct {
		Rules []json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(cfg.RouterConfig, &routing); err != nil {
		t.Fatal(err)
	}
	chainRules := 0
	for _, r := range routing.Rules {
		if strings.Contains(string(r), "cf:p1:n") {
			chainRules++
		}
	}
	if chainRules != 3 {
		t.Fatalf("injected rules = %d, want 3", chainRules)
	}
}

func TestApplyRollsBackWhenSelfCheckFails(t *testing.T) {
	svc, rec := newTestService(t)
	pool, err := svc.CreatePool(testPoolInput(t, "verify"))
	if err != nil {
		t.Fatal(err)
	}
	seedProxies(t, pool.Id, 3, "healthy")
	rec.verifyErr = errors.New("route mismatch")

	res, err := svc.Apply(context.Background(), pool.Id, ApplyOptions{})
	if err == nil {
		t.Fatal("apply must fail when the activation self-check fails")
	}
	var partial *PartialError
	if !errors.As(err, &partial) {
		t.Fatalf("error = %T %v, want PartialError", err, err)
	}
	if res.VerifyFailed != 3 || res.Activated != 0 {
		t.Fatalf("result = %+v", res)
	}
	var failed, active int64
	database.GetDB().Model(&model.ChainNode{}).Where("status = ?", "failed").Count(&failed)
	database.GetDB().Model(&model.ChainNode{}).Where("status = ?", "active").Count(&active)
	if failed != 3 || active != 0 {
		t.Fatalf("nodes: failed=%d active=%d, want 3/0", failed, active)
	}
	if rec.calls != 3 {
		t.Fatalf("reconciler calls = %d, want 3 (gate, activate, rollback)", rec.calls)
	}
}

func TestApplyRealityInboundAndPortShift(t *testing.T) {
	svc, _ := newTestService(t)
	occupied := freePort(t)
	if err := database.GetDB().Create(&model.Inbound{
		Tag: "foreign", Port: occupied, Protocol: model.VLESS, Enable: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	in := testPoolInput(t, "reality")
	in.Inbound.BasePort = occupied
	in.Inbound.Security = "reality"
	in.Inbound.Reality = RealityOptions{Dest: "www.microsoft.com:443", ServerNames: []string{"www.microsoft.com"}}
	pool, err := svc.CreatePool(in)
	if err != nil {
		t.Fatalf("CreatePool: %v", err)
	}
	_, _, _, _ = parsePoolTemplates(pool)
	stored, err := svc.GetPool(pool.Id)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, _ = parsePoolTemplates(stored)
	seedProxies(t, pool.Id, 1, "healthy")

	plan, err := svc.Plan(pool.Id, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Ports) != 1 || plan.Ports[0] == occupied {
		t.Fatalf("ports = %v, want a port shifted away from %d", plan.Ports, occupied)
	}
	if _, err := svc.Apply(context.Background(), pool.Id, ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var created model.Inbound
	if err := database.GetDB().Where("tag = ?", plan.InboundTags[0]).First(&created).Error; err != nil {
		t.Fatal(err)
	}
	if created.Port != plan.Ports[0] {
		t.Fatalf("inbound port = %d, want %d", created.Port, plan.Ports[0])
	}
	if !strings.Contains(created.StreamSettings, "realitySettings") ||
		!strings.Contains(created.StreamSettings, `"privateKey"`) {
		t.Fatalf("stream settings missing reality: %s", created.StreamSettings)
	}
}

func TestApplyRequireHealthySkipsUnknown(t *testing.T) {
	svc, _ := newTestService(t)
	pool, err := svc.CreatePool(testPoolInput(t, "healthy"))
	if err != nil {
		t.Fatal(err)
	}
	seedProxies(t, pool.Id, 2, "unknown")
	res, err := svc.Apply(context.Background(), pool.Id, ApplyOptions{RequireHealthy: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 0 {
		t.Fatalf("created %d nodes from unknown-health proxies", res.Created)
	}
}

func TestProbePoolUpdatesHealth(t *testing.T) {
	svc, _ := newTestService(t)
	pool, err := svc.CreatePool(testPoolInput(t, "probe"))
	if err != nil {
		t.Fatal(err)
	}
	good := startFakeSocks(t)
	goodHost, goodPort := good.addr()
	deadPort := freePort(t)
	rows := []model.ChainProxy{
		{PoolId: pool.Id, Seq: 1, Protocol: "socks5", Host: goodHost, Port: goodPort,
			Fingerprint: "good", Enabled: true, Health: "unknown", AllowPrivate: true},
		{PoolId: pool.Id, Seq: 2, Protocol: "socks5", Host: goodHost, Port: deadPort,
			Fingerprint: "dead", Enabled: true, Health: "unknown", AllowPrivate: true},
	}
	if err := database.GetDB().CreateInBatches(&rows, 10).Error; err != nil {
		t.Fatal(err)
	}
	summary, err := svc.ProbePool(context.Background(), pool.Id, ProbeOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("ProbePool: %v", err)
	}
	if summary.Total != 2 || summary.Healthy != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	var healthy, failed int64
	database.GetDB().Model(&model.ChainProxy{}).Where("health = ?", "healthy").Count(&healthy)
	database.GetDB().Model(&model.ChainProxy{}).Where("health <> ?", "healthy").Count(&failed)
	if healthy != 1 || failed != 1 {
		t.Fatalf("health rows: healthy=%d failed=%d", healthy, failed)
	}
}

func seedChainNode(t *testing.T, poolID int, proxyID int, status string) model.ChainNode {
	t.Helper()
	node := model.ChainNode{
		PoolId: poolID, ProxyId: &proxyID, Name: "p1-n" + strconv.Itoa(proxyID),
		Email: "p1-n" + strconv.Itoa(proxyID) + "@chain", InboundTag: "in-poc",
		OutboundTag: "cf:p1:n" + strconv.Itoa(proxyID), Status: status, ClientEnabled: true,
	}
	if err := database.GetDB().Create(&node).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}
	hop := model.ChainNodeHop{NodeId: node.Id, Position: 0, ProxyId: &proxyID,
		OutboundTag: node.OutboundTag, Kind: "landing"}
	if err := database.GetDB().Create(&hop).Error; err != nil {
		t.Fatalf("seed hop: %v", err)
	}
	return node
}

func TestFailoverRebindsToSpareAndKeepsTag(t *testing.T) {
	svc, rec := newTestService(t)
	in := testPoolInput(t, "failover")
	in.Chain.AutoFailover = true
	pool, err := svc.CreatePool(in)
	if err != nil {
		t.Fatal(err)
	}
	dead := seedProxies(t, pool.Id, 1, "unhealthy")[0]
	spare := seedProxies(t, pool.Id, 1, "healthy")[0]
	node := seedChainNode(t, pool.Id, dead.Id, "active")

	res, err := svc.Failover(context.Background(), pool.Id, FailoverOptions{})
	if err != nil {
		t.Fatalf("Failover: %v", err)
	}
	if res.Rebound != 1 || res.Gated != 0 {
		t.Fatalf("result = %+v", res)
	}
	var got model.ChainNode
	if err := database.GetDB().First(&got, node.Id).Error; err != nil {
		t.Fatal(err)
	}
	if got.ProxyId == nil || *got.ProxyId != spare.Id || got.Status != "active" {
		t.Fatalf("node after failover = %+v", got)
	}
	if got.OutboundTag != node.OutboundTag {
		t.Fatalf("outbound tag changed: %s -> %s", node.OutboundTag, got.OutboundTag)
	}
	var hop model.ChainNodeHop
	if err := database.GetDB().Where("node_id = ? AND position = 0", node.Id).First(&hop).Error; err != nil {
		t.Fatal(err)
	}
	if hop.ProxyId == nil || *hop.ProxyId != spare.Id {
		t.Fatalf("hop proxy = %v, want %d", hop.ProxyId, spare.Id)
	}
	if rec.calls != 1 {
		t.Fatalf("reconciler calls = %d, want 1", rec.calls)
	}
}

func TestFailoverGatesWhenNoSpare(t *testing.T) {
	svc, rec := newTestService(t)
	in := testPoolInput(t, "nospare")
	in.Chain.AutoFailover = true
	pool, err := svc.CreatePool(in)
	if err != nil {
		t.Fatal(err)
	}
	dead := seedProxies(t, pool.Id, 1, "unhealthy")[0]
	node := seedChainNode(t, pool.Id, dead.Id, "active")

	res, err := svc.Failover(context.Background(), pool.Id, FailoverOptions{})
	if err != nil {
		t.Fatalf("Failover: %v", err)
	}
	if res.Gated != 1 || res.Rebound != 0 {
		t.Fatalf("result = %+v", res)
	}
	var got model.ChainNode
	if err := database.GetDB().First(&got, node.Id).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" || got.ClientEnabled {
		t.Fatalf("node after failed failover = %+v", got)
	}
	if rec.calls != 1 {
		t.Fatalf("reconciler calls = %d, want 1", rec.calls)
	}
}

func TestFailoverSkipsWhenDisabled(t *testing.T) {
	svc, _ := newTestService(t)
	pool, err := svc.CreatePool(testPoolInput(t, "manual"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Failover(context.Background(), pool.Id, FailoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped {
		t.Fatalf("result = %+v, want skipped", res)
	}
}

func TestRebindNodePicksSpare(t *testing.T) {
	svc, rec := newTestService(t)
	pool, err := svc.CreatePool(testPoolInput(t, "rebind"))
	if err != nil {
		t.Fatal(err)
	}
	dead := seedProxies(t, pool.Id, 1, "unhealthy")[0]
	spare := seedProxies(t, pool.Id, 1, "healthy")[0]
	node := seedChainNode(t, pool.Id, dead.Id, "active")

	if err := svc.RebindNode(node.Id); err != nil {
		t.Fatalf("RebindNode: %v", err)
	}
	var got model.ChainNode
	if err := database.GetDB().First(&got, node.Id).Error; err != nil {
		t.Fatal(err)
	}
	if got.ProxyId == nil || *got.ProxyId != spare.Id {
		t.Fatalf("proxy = %v, want %d", got.ProxyId, spare.Id)
	}
	if rec.calls != 1 {
		t.Fatalf("reconciler calls = %d", rec.calls)
	}
}

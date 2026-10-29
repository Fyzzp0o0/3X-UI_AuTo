package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/chain/secret"
	"github.com/mhsanaei/3x-ui/v3/internal/chain/xraygen"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// ApplyOptions controls generation. MaxNodes caps one run; RequireHealthy skips
// proxies whose last probe failed; SkipReconcile is for dry tooling and tests.
type ApplyOptions struct {
	MaxNodes       int                   `json:"maxNodes"`
	RequireHealthy bool                  `json:"requireHealthy"`
	SkipReconcile  bool                  `json:"skipReconcile"`
	SkipVerify     bool                  `json:"skipVerify"`
	OnProgress     func(done, total int) `json:"-"`
}

// PlanResult is the dry-run diff shown before applying.
type PlanResult struct {
	PoolID       int      `json:"poolId"`
	Mode         string   `json:"mode"`
	ShardSize    int      `json:"shardSize"`
	Proxies      int      `json:"proxies"`
	Eligible     int      `json:"eligible"`
	Existing     int      `json:"existing"`
	NewNodes     int      `json:"newNodes"`
	NewOutbounds int      `json:"newOutbounds"`
	NewRules     int      `json:"newRules"`
	Shards       int      `json:"shards"`
	InboundTags  []string `json:"inboundTags"`
	Ports        []int    `json:"ports"`
	Conflicts    []string `json:"conflicts,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

// ApplyResult reports what generation actually wrote.
type ApplyResult struct {
	Plan         *PlanResult `json:"plan"`
	Created      int         `json:"created"`
	Activated    int         `json:"activated"`
	Inbounds     []int       `json:"inbounds"`
	Skipped      int         `json:"skipped"`
	Reconciled   bool        `json:"reconciled"`
	VerifyFailed int         `json:"verifyFailed"`
	Errors       []string    `json:"errors,omitempty"`
}

const gigabytes = int64(1024 * 1024 * 1024)

// Plan computes what Apply would do without writing anything.
func (s *Service) Plan(poolID int, opt ApplyOptions) (*PlanResult, error) {
	pool, err := s.GetPool(poolID)
	if err != nil {
		return nil, err
	}
	inboundTpl, _, strategy, _ := parsePoolTemplates(pool)
	requireHealthy := opt.RequireHealthy || strategy.RequireHealthy

	db := database.GetDB()
	var proxies []model.ChainProxy
	if err := db.Where("pool_id = ? AND enabled = ?", poolID, true).Order("id").Find(&proxies).Error; err != nil {
		return nil, err
	}
	res := &PlanResult{PoolID: pool.Id, Mode: pool.Mode, ShardSize: pool.ShardSize, Proxies: len(proxies)}

	eligible := make([]model.ChainProxy, 0, len(proxies))
	for _, p := range proxies {
		if requireHealthy && p.Health != "healthy" {
			continue
		}
		if _, err := secret.Open(p.Id, p.PasswordEnc); err != nil {
			res.Conflicts = append(res.Conflicts, fmt.Sprintf("proxy %d: credential cannot be decrypted", p.Id))
			continue
		}
		eligible = append(eligible, p)
	}
	res.Eligible = len(eligible)

	existing := make(map[int]struct{}, len(eligible))
	var boundProxyIDs []int
	if err := db.Model(&model.ChainNode{}).Where("pool_id = ? AND proxy_id IS NOT NULL", poolID).
		Pluck("proxy_id", &boundProxyIDs).Error; err != nil {
		return nil, err
	}
	for _, id := range boundProxyIDs {
		existing[id] = struct{}{}
	}
	for _, p := range eligible {
		if _, ok := existing[p.Id]; ok {
			res.Existing++
		}
	}
	res.NewNodes = len(eligible) - res.Existing
	if opt.MaxNodes > 0 && res.NewNodes > opt.MaxNodes {
		res.NewNodes = opt.MaxNodes
	}
	res.NewOutbounds = res.NewNodes
	res.NewRules = res.NewNodes

	shards := shardCount(len(eligible), pool.ShardSize)
	res.Shards = shards
	layout, err := s.shardLayout(pool, inboundTpl, shards)
	if err != nil {
		res.Conflicts = append(res.Conflicts, err.Error())
	} else {
		res.InboundTags = layout.tags
		res.Ports = layout.ports
	}
	if strategy.FrontTag != "" {
		tags, err := templateOutboundTags()
		if err != nil {
			res.Conflicts = append(res.Conflicts, "cannot read the Xray template: "+err.Error())
		} else if _, ok := tags[strategy.FrontTag]; !ok {
			res.Conflicts = append(res.Conflicts, "frontTag "+strategy.FrontTag+" is not an outbound in the Xray template")
		}
	}
	if res.NewNodes < 0 {
		res.NewNodes = 0
	}
	if res.Mode == "dedicated" {
		res.Warnings = append(res.Warnings, "dedicated mode is not implemented in M1; sharded layout is used")
	}
	return res, nil
}

func shardCount(nodes, shardSize int) int {
	if nodes == 0 {
		return 0
	}
	if shardSize <= 0 {
		shardSize = 40
	}
	return (nodes + shardSize - 1) / shardSize
}

func shardTag(pool *model.ChainPool, index int) string {
	return fmt.Sprintf("%s:s%d", xraygen.PoolPrefix(pool.Id), index)
}

func findInboundByTag(tag string) (*model.Inbound, error) {
	var ib model.Inbound
	err := database.GetDB().Where("tag = ?", tag).First(&ib).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &ib, err
}

func templateOutboundTags() (map[string]struct{}, error) {
	raw, err := (&service.SettingService{}).GetXrayConfigTemplate()
	if err != nil {
		return nil, err
	}
	var doc struct {
		Outbounds []struct {
			Tag string `json:"tag"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, err
	}
	tags := make(map[string]struct{}, len(doc.Outbounds))
	for _, ob := range doc.Outbounds {
		if ob.Tag != "" {
			tags[ob.Tag] = struct{}{}
		}
	}
	return tags, nil
}

// Apply generates missing nodes in the fail-closed order: inbound, clients
// (disabled), gated nodes, reconcile, then activation and a second reconcile.
func (s *Service) Apply(ctx context.Context, poolID int, opt ApplyOptions) (*ApplyResult, error) {
	plan, err := s.Plan(poolID, opt)
	if err != nil {
		return nil, err
	}
	if len(plan.Conflicts) > 0 {
		return nil, fmt.Errorf("cannot apply: %s", strings.Join(plan.Conflicts, "; "))
	}
	if plan.NewNodes == 0 {
		return &ApplyResult{Plan: plan}, nil
	}
	pool, err := s.GetPool(poolID)
	if err != nil {
		return nil, err
	}
	inboundTpl, clientTpl, strategy, _ := parsePoolTemplates(pool)

	var proxies []model.ChainProxy
	if err := database.GetDB().Where("pool_id = ? AND enabled = ?", poolID, true).
		Order("id").Find(&proxies).Error; err != nil {
		return nil, err
	}
	requireHealthy := opt.RequireHealthy || strategy.RequireHealthy
	eligible := make([]model.ChainProxy, 0, len(proxies))
	for _, p := range proxies {
		if requireHealthy && p.Health != "healthy" {
			continue
		}
		eligible = append(eligible, p)
	}

	existing := map[int]struct{}{}
	var bound []int
	if err := database.GetDB().Model(&model.ChainNode{}).Where("pool_id = ? AND proxy_id IS NOT NULL", poolID).
		Pluck("proxy_id", &bound).Error; err != nil {
		return nil, err
	}
	for _, id := range bound {
		existing[id] = struct{}{}
	}

	shards := shardCount(len(eligible), pool.ShardSize)
	inbounds, err := s.ensureShardInbounds(pool, inboundTpl, shards)
	if err != nil {
		return nil, err
	}

	type newNode struct {
		proxy   model.ChainProxy
		node    model.ChainNode
		inbound *model.Inbound
	}
	pending := make([]newNode, 0, plan.NewNodes)
	emails := make([]string, 0, plan.NewNodes)
	seen := make(map[string]struct{}, len(eligible))
	for i, p := range eligible {
		if len(pending) >= plan.NewNodes {
			break
		}
		if _, ok := existing[p.Id]; ok {
			continue
		}
		email := xraygen.EmailFor(pool.Id, p.Id)
		if _, dup := seen[email]; dup {
			continue
		}
		seen[email] = struct{}{}
		shard := 0
		if pool.ShardSize > 0 {
			shard = i / pool.ShardSize
		}
		if shard >= len(inbounds) {
			shard = len(inbounds) - 1
		}
		tag := xraygen.NodeTag(pool.Id, p.Id)
		node := model.ChainNode{
			PoolId: pool.Id, ProxyId: &p.Id, Name: fmt.Sprintf("p%d-n%d", pool.Id, p.Id),
			Email: email, InboundId: &inbounds[shard].Id, InboundTag: inbounds[shard].Tag,
			OutboundTag: tag, Status: "provisioning",
		}
		pending = append(pending, newNode{proxy: p, node: node, inbound: inbounds[shard]})
		emails = append(emails, email)
	}
	if len(pending) == 0 {
		return &ApplyResult{Plan: plan}, nil
	}

	// Clients start disabled: routing must exist before a customer can connect.
	payloads := make([]service.ClientCreatePayload, 0, len(pending))
	expiry := int64(0)
	if clientTpl.ExpiryDays > 0 {
		expiry = nowUnixMilli() + int64(clientTpl.ExpiryDays)*24*3600*1000
	}
	for _, p := range pending {
		payloads = append(payloads, service.ClientCreatePayload{
			Client: model.Client{
				Email: p.node.Email, Enable: false, TotalGB: clientTpl.TotalGB * gigabytes,
				ExpiryTime: expiry, LimitIP: clientTpl.LimitIP, Flow: clientTpl.Flow,
				Group: clientTpl.Group, Comment: "chain:" + pool.Name,
			},
			InboundIds: []int{p.inbound.Id},
		})
	}
	bulk, _, err := s.Client.BulkCreate(s.Inbound, payloads)
	if err != nil {
		return nil, fmt.Errorf("create clients: %w", err)
	}
	if len(bulk.Skipped) > 0 {
		return nil, fmt.Errorf("create clients: %d skipped, first: %s (%s)",
			len(bulk.Skipped), bulk.Skipped[0].Email, bulk.Skipped[0].Reason)
	}

	err = database.GetDB().Transaction(func(tx *gorm.DB) error {
		for i := range pending {
			if err := tx.Create(&pending[i].node).Error; err != nil {
				return err
			}
			landingProxy := pending[i].proxy.Id
			hop := model.ChainNodeHop{
				NodeId: pending[i].node.Id, Position: 0, ProxyId: &landingProxy,
				OutboundTag: pending[i].node.OutboundTag, Kind: "landing",
			}
			if err := tx.Create(&hop).Error; err != nil {
				return err
			}
			if strategy.FrontTag != "" {
				front := model.ChainNodeHop{
					NodeId: pending[i].node.Id, Position: 1, OutboundTag: strategy.FrontTag, Kind: "front",
				}
				if err := tx.Create(&front).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		for _, email := range emails {
			_, _ = s.Client.DeleteByEmail(s.Inbound, email, false)
		}
		return nil, fmt.Errorf("persist nodes: %w", err)
	}
	if err := s.BumpRevision(pool.Id); err != nil {
		return nil, err
	}

	res := &ApplyResult{Plan: plan, Created: len(pending)}
	for _, ib := range inbounds {
		res.Inbounds = append(res.Inbounds, ib.Id)
	}
	if opt.SkipReconcile {
		res.Skipped = len(pending)
		return res, nil
	}

	// Phase 1: outbounds + gate rules reach the core while every client is still
	// disabled, so nothing can egress before its proxy is active.
	if s.Reconciler != nil {
		if err := s.Reconciler.Apply(); err != nil {
			return res, fmt.Errorf("reconcile gate config: %w", err)
		}
	}
	if _, _, err := s.Client.BulkSetEnable(s.Inbound, emails, true); err != nil {
		return res, fmt.Errorf("enable clients: %w", err)
	}
	if err := database.GetDB().Model(&model.ChainNode{}).
		Where("pool_id = ? AND email IN ?", pool.Id, emails).
		Updates(map[string]any{"status": "active", "client_enabled": true, "activated_at": nowUnixMilli()}).Error; err != nil {
		return res, err
	}
	// Phase 2: users are added before routing in the hot diff, so the flip from
	// blocked to socks can never expose a direct window.
	if s.Reconciler != nil {
		if err := s.Reconciler.Apply(); err != nil {
			return res, fmt.Errorf("activate nodes: %w", err)
		}
	}
	res.Activated = len(pending)
	res.Reconciled = s.Reconciler != nil

	// Activation self-check: ask the core's router which outbound each new
	// client actually reaches. A mismatch means the rule did not land, so the
	// node goes back behind the gate instead of serving unprotected traffic.
	if !opt.SkipVerify && s.Reconciler != nil {
		var failedIDs []int
		var failedEmails []string
		for i := range pending {
			matched, err := s.Reconciler.Verify(pending[i].node.InboundTag, pending[i].node.Email)
			reason := ""
			switch {
			case err != nil:
				reason = err.Error()
			case matched != pending[i].node.OutboundTag:
				reason = fmt.Sprintf("route mismatch: got %q want %q", matched, pending[i].node.OutboundTag)
			default:
				continue
			}
			failedIDs = append(failedIDs, pending[i].node.Id)
			failedEmails = append(failedEmails, pending[i].node.Email)
			res.Errors = append(res.Errors, pending[i].node.Name+": "+truncate(reason, 160))
		}
		if len(failedIDs) > 0 {
			if err := database.GetDB().Model(&model.ChainNode{}).Where("id IN ?", failedIDs).
				Updates(map[string]any{"status": "failed", "client_enabled": false}).Error; err != nil {
				return res, err
			}
			if _, _, err := s.Client.BulkSetEnable(s.Inbound, failedEmails, false); err != nil {
				return res, err
			}
			if err := s.BumpRevision(pool.Id); err != nil {
				return res, err
			}
			if err := s.Reconciler.Apply(); err != nil {
				return res, fmt.Errorf("reconcile after self-check failure: %w", err)
			}
			res.VerifyFailed = len(failedIDs)
			res.Activated -= len(failedIDs)
			return res, &PartialError{Message: fmt.Sprintf("%d of %d nodes failed the activation self-check", len(failedIDs), len(pending))}
		}
	}
	if opt.OnProgress != nil {
		opt.OnProgress(res.Activated, res.Activated)
	}
	return res, nil
}

// ensureShardInbounds reuses shard inbounds by tag and creates the missing ones.
func (s *Service) ensureShardInbounds(pool *model.ChainPool, tpl InboundTemplate, shards int) ([]*model.Inbound, error) {
	layout, err := s.shardLayout(pool, tpl, shards)
	if err != nil {
		return nil, err
	}
	settings := `{"clients":[],"decryption":"none","fallbacks":[]}`
	stream, err := buildStreamSettings(tpl)
	if err != nil {
		return nil, err
	}
	remark := tpl.Remark
	if remark == "" {
		remark = pool.Name
	}
	listen := tpl.Listen
	out := make([]*model.Inbound, 0, shards)
	for i := 0; i < shards; i++ {
		if layout.existing[i] != nil {
			out = append(out, layout.existing[i])
			continue
		}
		created, _, err := s.Inbound.AddInbound(&model.Inbound{
			Remark: fmt.Sprintf("%s-%d", remark, i+1), Port: layout.ports[i], Listen: listen,
			Protocol: model.VLESS, Tag: layout.tags[i], Enable: true,
			Settings: settings, StreamSettings: stream,
			Sniffing: `{"enabled":true,"destOverride":["http","tls"]}`,
		})
		if err != nil {
			return nil, fmt.Errorf("create shard inbound %d: %w", i, err)
		}
		out = append(out, created)
	}
	return out, nil
}

type shardLayoutResult struct {
	tags     []string
	ports    []int
	existing []*model.Inbound
}

// shardLayout resolves one port and inbound per shard, reusing stored shards
// and allocating free ports from [basePort, portEnd] for the rest.
func (s *Service) shardLayout(pool *model.ChainPool, tpl InboundTemplate, shards int) (*shardLayoutResult, error) {
	used, err := inboundPorts()
	if err != nil {
		return nil, err
	}
	base := tpl.BasePort
	if base == 0 {
		base = 2083
	}
	res := &shardLayoutResult{
		tags:     make([]string, 0, shards),
		ports:    make([]int, 0, shards),
		existing: make([]*model.Inbound, 0, shards),
	}
	for i := 0; i < shards; i++ {
		tag := shardTag(pool, i)
		ib, err := findInboundByTag(tag)
		if err != nil {
			return nil, err
		}
		if ib != nil {
			res.tags = append(res.tags, tag)
			res.ports = append(res.ports, ib.Port)
			res.existing = append(res.existing, ib)
			continue
		}
		port, err := allocateShardPorts(base, tpl.PortEnd, 1, used)
		if err != nil {
			return nil, err
		}
		res.tags = append(res.tags, tag)
		res.ports = append(res.ports, port[0])
		res.existing = append(res.existing, nil)
	}
	return res, nil
}

// inboundPorts collects every port a local inbound already listens on.
func inboundPorts() (map[int]struct{}, error) {
	var ports []int
	if err := database.GetDB().Model(&model.Inbound{}).Pluck("port", &ports).Error; err != nil {
		return nil, err
	}
	used := make(map[int]struct{}, len(ports))
	for _, port := range ports {
		used[port] = struct{}{}
	}
	return used, nil
}

// nowUnixMilli is a variable so tests can pin expiry math.
var nowUnixMilli = func() int64 { return time.Now().UnixMilli() }

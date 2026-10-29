// Package chain implements the chain-proxy module's business logic: proxy
// pools, bulk import, health probes, and node generation.
package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/chain/linkparse"
	"github.com/mhsanaei/3x-ui/v3/internal/chain/secret"
	"github.com/mhsanaei/3x-ui/v3/internal/chain/xraygen"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
	"gorm.io/gorm"
)

// Reconciler applies the generated config to the running core and asks the
// core's router which outbound a client would actually reach. Tests substitute
// a fake so the data plane runs without an Xray binary.
type Reconciler interface {
	Apply() error
	Verify(inboundTag, email string) (matchedOutbound string, err error)
}

// Service owns chain state. It deliberately uses the existing Inbound/Client
// services instead of duplicating their runtime dispatch.
type Service struct {
	Inbound    *service.InboundService
	Client     *service.ClientService
	Reconciler Reconciler
}

// NewService returns a service wired to the real panel services and Xray.
func NewService() *Service {
	return &Service{
		Inbound:    &service.InboundService{},
		Client:     &service.ClientService{},
		Reconciler: xrayReconciler{svc: &service.XrayService{}},
	}
}

type xrayReconciler struct{ svc *service.XrayService }

func (r xrayReconciler) Apply() error { return r.svc.RestartXray(false) }

func (r xrayReconciler) Verify(inboundTag, email string) (string, error) {
	res, err := r.svc.TestRoute(xray.RouteTestRequest{
		InboundTag: inboundTag, Email: email, Domain: "example.com", Port: 443,
	})
	if err != nil {
		return "", err
	}
	if !res.Matched {
		return "", nil
	}
	return res.OutboundTag, nil
}

// PoolInput is the editable shape of a pool. Templates are stored as JSON so
// M1 can grow fields without a migration.
type PoolInput struct {
	Name        string          `json:"name"`
	Remark      string          `json:"remark"`
	Mode        string          `json:"mode"`
	ShardSize   int             `json:"shardSize"`
	Enabled     bool            `json:"enabled"`
	Inbound     InboundTemplate `json:"inbound"`
	Client      ClientTemplate  `json:"client"`
	Chain       ChainStrategy   `json:"chain"`
	Probe       ProbePolicy     `json:"probe"`
	OutboundExp bool            `json:"outboundStats"`
}

// InboundTemplate describes the shared entry point of a pool: protocol,
// port range, transport and security all live here and are stored as JSON.
type InboundTemplate struct {
	Protocol  string           `json:"protocol"`
	BasePort  int              `json:"basePort"`
	PortEnd   int              `json:"portEnd,omitempty"`
	Listen    string           `json:"listen,omitempty"`
	Remark    string           `json:"remark,omitempty"`
	Network   string           `json:"network,omitempty"`
	Security  string           `json:"security,omitempty"`
	Transport TransportOptions `json:"transport,omitempty"`
	TLS       TLSOptions       `json:"tls,omitempty"`
	Reality   RealityOptions   `json:"reality,omitempty"`
}

// ClientTemplate is applied to every generated client.
type ClientTemplate struct {
	TotalGB    int64  `json:"totalGB"`
	ExpiryDays int    `json:"expiryDays"`
	LimitIP    int    `json:"limitIp"`
	Flow       string `json:"flow"`
	Group      string `json:"group"`
}

// ChainStrategy is the per-node egress shape. FrontTag must name an existing
// outbound in the Xray template; empty means a direct socks hop.
type ChainStrategy struct {
	FrontTag       string `json:"frontTag"`
	RequireHealthy bool   `json:"requireHealthy"`
	AutoFailover   bool   `json:"autoFailover"`
}

// ProbePolicy controls the health check cadence and target.
type ProbePolicy struct {
	URL         string `json:"url"`
	TimeoutMs   int    `json:"timeoutMs"`
	Concurrency int    `json:"concurrency"`
}

// PoolView is a pool plus the counters the list page needs.
type PoolView struct {
	model.ChainPool
	Proxies int64 `json:"proxies"`
	Nodes   int64 `json:"nodes"`
	Healthy int64 `json:"healthy"`
}

// ImportOptions controls a bulk import. DryRun parses and dedupes without
// writing, which is what the wizard's preview step uses.
type ImportOptions struct {
	DryRun       bool `json:"dryRun"`
	AllowPrivate bool `json:"allowPrivate"`
}

// ImportLine is one parse outcome echoed to the preview table.
type ImportLine struct {
	LineNo int    `json:"lineNo"`
	Line   string `json:"line"`
	Error  string `json:"error,omitempty"`
	Proxy  string `json:"proxy,omitempty"`
}

// ImportResult summarizes an import. Invalid lines are capped by the caller.
type ImportResult struct {
	Total     int          `json:"total"`
	Valid     int          `json:"valid"`
	Duplicate int          `json:"duplicate"`
	Created   int          `json:"created"`
	DryRun    bool         `json:"dryRun"`
	Preview   []ImportLine `json:"preview"`
	Invalid   []ImportLine `json:"invalid,omitempty"`
}

const (
	// MaxImportLines bounds one paste; the UI rejects larger files earlier.
	MaxImportLines = 5000
	// importPreview caps how many parsed lines travel back to the browser.
	importPreview = 200
)

func parsePoolTemplates(p *model.ChainPool) (InboundTemplate, ClientTemplate, ChainStrategy, ProbePolicy) {
	var inbound InboundTemplate
	var client ClientTemplate
	var strategy ChainStrategy
	var probe ProbePolicy
	_ = json.Unmarshal([]byte(p.InboundTemplate), &inbound)
	_ = json.Unmarshal([]byte(p.ClientTemplate), &client)
	_ = json.Unmarshal([]byte(p.ChainStrategy), &strategy)
	_ = json.Unmarshal([]byte(p.ProbePolicy), &probe)
	return inbound, client, strategy, probe
}

func encodeTemplates(in PoolInput) (string, string, string, string, error) {
	ib, err := json.Marshal(in.Inbound)
	if err != nil {
		return "", "", "", "", err
	}
	cl, err := json.Marshal(in.Client)
	if err != nil {
		return "", "", "", "", err
	}
	ch, err := json.Marshal(in.Chain)
	if err != nil {
		return "", "", "", "", err
	}
	pr, err := json.Marshal(in.Probe)
	if err != nil {
		return "", "", "", "", err
	}
	return string(ib), string(cl), string(ch), string(pr), nil
}

func validatePoolInput(in PoolInput) error {
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 64 {
		return errors.New("pool name is required (max 64)")
	}
	switch in.Mode {
	case "", "shared", "sharded", "dedicated":
	default:
		return errors.New("mode must be shared, sharded or dedicated")
	}
	if in.ShardSize < 0 || in.ShardSize > 500 {
		return errors.New("shardSize must be 0-500")
	}
	if in.Client.ExpiryDays < 0 || in.Client.TotalGB < 0 || in.Client.LimitIP < 0 {
		return errors.New("client quotas cannot be negative")
	}
	if in.Chain.FrontTag != "" {
		if err := xraygen.ValidateTag(in.Chain.FrontTag); err != nil {
			return fmt.Errorf("frontTag: %w", err)
		}
	}
	if in.Probe.TimeoutMs < 0 || in.Probe.Concurrency < 0 || in.Probe.Concurrency > 64 {
		return errors.New("probe limits out of range")
	}
	return nil
}

// CreatePool stores a new pool and derives its immutable tag namespace.
func (s *Service) CreatePool(in PoolInput) (*model.ChainPool, error) {
	if err := normalizeInboundTemplate(&in.Inbound); err != nil {
		return nil, err
	}
	if err := validatePoolInput(in); err != nil {
		return nil, err
	}
	ib, cl, ch, pr, err := encodeTemplates(in)
	if err != nil {
		return nil, err
	}
	mode := in.Mode
	if mode == "" {
		mode = "sharded"
	}
	shard := in.ShardSize
	if shard == 0 {
		shard = 40
	}
	pool := &model.ChainPool{
		Name: in.Name, Remark: in.Remark, Mode: mode, ShardSize: shard, Enabled: in.Enabled,
		InboundTemplate: ib, ClientTemplate: cl, ChainStrategy: ch, ProbePolicy: pr,
		OutboundStats: in.OutboundExp,
	}
	if err := database.GetDB().Create(pool).Error; err != nil {
		return nil, err
	}
	pool.TagPrefix = xraygen.PoolPrefix(pool.Id)
	if err := database.GetDB().Model(pool).Update("tag_prefix", pool.TagPrefix).Error; err != nil {
		return nil, err
	}
	return pool, nil
}

// UpdatePool rewrites the editable fields; the tag namespace never changes.
func (s *Service) UpdatePool(id int, in PoolInput) error {
	if err := normalizeInboundTemplate(&in.Inbound); err != nil {
		return err
	}
	if err := validatePoolInput(in); err != nil {
		return err
	}
	ib, cl, ch, pr, err := encodeTemplates(in)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"name": in.Name, "remark": in.Remark, "mode": in.Mode, "shard_size": in.ShardSize,
		"enabled": in.Enabled, "inbound_template": ib, "client_template": cl,
		"chain_strategy": ch, "probe_policy": pr, "outbound_stats": in.OutboundExp,
	}
	res := database.GetDB().Model(&model.ChainPool{}).Where("id = ?", id).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return s.BumpRevision(id)
}

// BumpRevision invalidates any injector cache after chain state changes.
func (s *Service) BumpRevision(poolID int) error {
	return database.GetDB().Model(&model.ChainPool{}).Where("id = ?", poolID).
		UpdateColumn("revision", gorm.Expr("revision + 1")).Error
}

// GetPool loads one pool.
func (s *Service) GetPool(id int) (*model.ChainPool, error) {
	var pool model.ChainPool
	if err := database.GetDB().First(&pool, id).Error; err != nil {
		return nil, err
	}
	return &pool, nil
}

// ListPools returns pools with proxy/node/health counters.
func (s *Service) ListPools() ([]PoolView, error) {
	db := database.GetDB()
	var pools []model.ChainPool
	if err := db.Order("id").Find(&pools).Error; err != nil {
		return nil, err
	}
	out := make([]PoolView, 0, len(pools))
	for _, p := range pools {
		view := PoolView{ChainPool: p}
		_ = db.Model(&model.ChainProxy{}).Where("pool_id = ?", p.Id).Count(&view.Proxies).Error
		_ = db.Model(&model.ChainProxy{}).Where("pool_id = ? AND health = ?", p.Id, "healthy").Count(&view.Healthy).Error
		_ = db.Model(&model.ChainNode{}).Where("pool_id = ?", p.Id).Count(&view.Nodes).Error
		out = append(out, view)
	}
	return out, nil
}

// ImportProxies parses a paste and upserts proxies by fingerprint.
func (s *Service) ImportProxies(ctx context.Context, poolID int, input string, opt ImportOptions) (*ImportResult, error) {
	pool, err := s.GetPool(poolID)
	if err != nil {
		return nil, err
	}
	lines := linkparse.ParseLines(input, linkparse.Options{AllowPrivate: opt.AllowPrivate})
	if len(lines) > MaxImportLines {
		return nil, fmt.Errorf("import is limited to %d lines", MaxImportLines)
	}
	unique, duplicates := linkparse.Dedupe(lines)
	res := &ImportResult{Total: len(lines), Duplicate: duplicates, DryRun: opt.DryRun}
	for _, l := range unique {
		if l.OK() {
			res.Valid++
			if len(res.Preview) < importPreview {
				res.Preview = append(res.Preview, ImportLine{LineNo: l.LineNo, Line: l.Line, Proxy: proxySummary(l.Proxy)})
			}
			continue
		}
		if len(res.Invalid) < importPreview {
			res.Invalid = append(res.Invalid, ImportLine{LineNo: l.LineNo, Line: l.Line, Error: l.Err})
		}
	}
	if opt.DryRun || res.Valid == 0 {
		return res, nil
	}
	if !hasPasswords(unique) {
		return s.insertProxies(pool, unique, res)
	}
	if !secret.Enabled() {
		return nil, secret.ErrEncryptionRequired
	}
	return s.insertProxies(pool, unique, res)
}

func hasPasswords(lines []linkparse.Result) bool {
	for _, l := range lines {
		if l.OK() && l.Proxy.Password != "" {
			return true
		}
	}
	return false
}

func proxySummary(p linkparse.Proxy) string {
	host := p.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if p.Username != "" {
		return fmt.Sprintf("%s://%s@%s:%d", p.Protocol, p.Username, host, p.Port)
	}
	return fmt.Sprintf("%s://%s:%d", p.Protocol, host, p.Port)
}

// insertProxies writes new fingerprints and reports how many rows landed.
func (s *Service) insertProxies(pool *model.ChainPool, lines []linkparse.Result, res *ImportResult) (*ImportResult, error) {
	db := database.GetDB()
	var existing []string
	if err := db.Model(&model.ChainProxy{}).Where("pool_id = ?", pool.Id).
		Pluck("fingerprint", &existing).Error; err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(existing))
	for _, fp := range existing {
		seen[fp] = struct{}{}
	}
	var maxSeq int
	_ = db.Model(&model.ChainProxy{}).Where("pool_id = ?", pool.Id).Select("COALESCE(MAX(seq),0)").Scan(&maxSeq).Error

	rows := make([]model.ChainProxy, 0, len(lines))
	for _, l := range lines {
		if !l.OK() {
			continue
		}
		fp := linkparse.Fingerprint(l.Proxy)
		if _, dup := seen[fp]; dup {
			res.Duplicate++
			continue
		}
		seen[fp] = struct{}{}
		maxSeq++
		rows = append(rows, model.ChainProxy{
			PoolId: pool.Id, Seq: maxSeq, Protocol: l.Proxy.Protocol, Host: l.Proxy.Host,
			Port: l.Proxy.Port, Username: l.Proxy.Username, Fingerprint: fp,
			RawLine: l.Line, Label: l.Proxy.Label, Enabled: true, Health: "unknown",
		})
	}
	if len(rows) == 0 {
		return res, nil
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.CreateInBatches(&rows, 100).Error; err != nil {
			return err
		}
		// AAD binds ciphertext to the row id, so passwords are sealed after the
		// insert that assigns it.
		for i := range rows {
			plain := linePassword(lines, rows[i].Fingerprint)
			if plain == "" {
				continue
			}
			sealed, err := secret.Seal(rows[i].Id, plain)
			if err != nil {
				return err
			}
			if err := tx.Model(&model.ChainProxy{}).Where("id = ?", rows[i].Id).
				Update("password_enc", sealed).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.Created = len(rows)
	return res, s.BumpRevision(pool.Id)
}

func linePassword(lines []linkparse.Result, fingerprint string) string {
	for _, l := range lines {
		if l.OK() && linkparse.Fingerprint(l.Proxy) == fingerprint {
			return l.Proxy.Password
		}
	}
	return ""
}

// ProxyQuery is the proxy list filter.
type ProxyQuery struct {
	Page    int
	Size    int
	Status  string
	Keyword string
}

// ListProxies returns one page of proxies; credentials are never serialized.
func (s *Service) ListProxies(poolID int, q ProxyQuery) ([]model.ChainProxy, int64, error) {
	db := database.GetDB().Model(&model.ChainProxy{}).Where("pool_id = ?", poolID)
	if q.Status != "" {
		db = db.Where("health = ?", q.Status)
	}
	if q.Keyword != "" {
		kw := "%" + strings.ToLower(q.Keyword) + "%"
		db = db.Where("LOWER(host) LIKE ? OR LOWER(label) LIKE ?", kw, kw)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	size, page := normalizePage(q.Size, q.Page)
	var rows []model.ChainProxy
	if err := db.Order("seq").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// BulkProxyAction enables, disables or deletes a selection of proxies.
type BulkProxyAction string

const (
	ProxyEnable  BulkProxyAction = "enable"
	ProxyDisable BulkProxyAction = "disable"
	ProxyDelete  BulkProxyAction = "delete"
)

// BulkProxies applies one action to selected proxies and refuses to delete a
// proxy that an active node still uses.
func (s *Service) BulkProxies(poolID int, ids []int, action BulkProxyAction) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	db := database.GetDB()
	if action == ProxyDelete {
		var bound int64
		if err := db.Model(&model.ChainNode{}).Where("proxy_id IN ? AND status IN ?", ids,
			[]string{"active", "degraded", "provisioning"}).Count(&bound).Error; err != nil {
			return 0, err
		}
		if bound > 0 {
			return 0, errors.New("proxies still bound to nodes; rebind or delete those nodes first")
		}
		res := db.Where("pool_id = ? AND id IN ?", poolID, ids).Delete(&model.ChainProxy{})
		return res.RowsAffected, res.Error
	}
	enable := action == ProxyEnable
	res := db.Model(&model.ChainProxy{}).Where("pool_id = ? AND id IN ?", poolID, ids).
		Updates(map[string]any{"enabled": enable, "health": map[bool]string{true: "unknown", false: "disabled"}[enable]})
	return res.RowsAffected, res.Error
}

func normalizePage(size, page int) (int, int) {
	if size <= 0 || size > 500 {
		size = 50
	}
	if page <= 0 {
		page = 1
	}
	return size, page
}

// NodeQuery is the node list filter.
type NodeQuery struct {
	Page    int
	Size    int
	PoolID  int
	Status  string
	Keyword string
}

// ListNodeView is a node joined with its proxy for list rendering.
type ListNodeView struct {
	model.ChainNode
	ProxyHost string `json:"proxyHost"`
	ProxyPort int    `json:"proxyPort"`
	Health    string `json:"health"`
	LatencyMs int    `json:"latencyMs"`
	ExitIp    string `json:"exitIp"`
}

// ListNodes returns one page of nodes with the fields the matrix renders.
func (s *Service) ListNodes(q NodeQuery) ([]ListNodeView, int64, error) {
	db := database.GetDB().Model(&model.ChainNode{})
	if q.PoolID > 0 {
		db = db.Where("pool_id = ?", q.PoolID)
	}
	if q.Status != "" {
		db = db.Where("status = ?", q.Status)
	}
	if q.Keyword != "" {
		kw := "%" + strings.ToLower(q.Keyword) + "%"
		db = db.Where("LOWER(name) LIKE ? OR LOWER(email) LIKE ?", kw, kw)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	size, page := normalizePage(q.Size, q.Page)
	var nodes []model.ChainNode
	if err := db.Order("id").Offset((page - 1) * size).Limit(size).Find(&nodes).Error; err != nil {
		return nil, 0, err
	}
	views := make([]ListNodeView, 0, len(nodes))
	for _, n := range nodes {
		v := ListNodeView{ChainNode: n}
		if n.ProxyId != nil {
			var p model.ChainProxy
			if err := database.GetDB().First(&p, *n.ProxyId).Error; err == nil {
				v.ProxyHost, v.ProxyPort, v.Health, v.LatencyMs, v.ExitIp = p.Host, p.Port, p.Health, p.LatencyMs, p.ExitIp
			}
		}
		views = append(views, v)
	}
	return views, total, nil
}

// GetNode returns one node with its chain hops.
func (s *Service) GetNode(id int) (*model.ChainNode, []model.ChainNodeHop, error) {
	var n model.ChainNode
	if err := database.GetDB().First(&n, id).Error; err != nil {
		return nil, nil, err
	}
	var hops []model.ChainNodeHop
	if err := database.GetDB().Where("node_id = ?", id).Order("position").Find(&hops).Error; err != nil {
		return nil, nil, err
	}
	return &n, hops, nil
}

// RekeyCredentials re-encrypts every stored proxy password under the active
// keyring entry. Run it after rotating the shared node-token key so dropping
// the old key cannot strand chain credentials.
func (s *Service) RekeyCredentials() (changed, skipped int, err error) {
	db := database.GetDB()
	var rows []model.ChainProxy
	if err := db.Select("id", "password_enc").Find(&rows).Error; err != nil {
		return 0, 0, err
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		for _, p := range rows {
			sealed, did, rerr := secret.Rekey(p.Id, p.PasswordEnc)
			if rerr != nil {
				return fmt.Errorf("proxy %d: %w", p.Id, rerr)
			}
			if !did {
				skipped++
				continue
			}
			if uerr := tx.Model(&model.ChainProxy{}).Where("id = ?", p.Id).
				Update("password_enc", sealed).Error; uerr != nil {
				return uerr
			}
			changed++
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return changed, skipped, nil
}

// NodeLinks renders the node client's share links (VLESS/Clash-ready URIs).
// A missing sub-link provider is not an error: the detail view still renders.
func (s *Service) NodeLinks(nodeID int, host string) []string {
	var node model.ChainNode
	if err := database.GetDB().First(&node, nodeID).Error; err != nil {
		return nil
	}
	links, err := s.Inbound.GetAllClientLinks(host, node.Email)
	if err != nil {
		return nil
	}
	return links
}

// SetNodeEnabled toggles a node and its client. Disabling keeps the gate rule
// so traffic can never fall back to the default outbound.
func (s *Service) SetNodeEnabled(poolID, nodeID int, enable bool) error {
	db := database.GetDB()
	var node model.ChainNode
	if err := db.First(&node, nodeID).Error; err != nil {
		return err
	}
	if node.PoolId != poolID {
		return errors.New("node does not belong to this pool")
	}
	status := "active"
	if !enable {
		status = "disabled"
	}
	if err := db.Model(&model.ChainNode{}).Where("id = ?", nodeID).
		Updates(map[string]any{"status": status, "client_enabled": enable}).Error; err != nil {
		return err
	}
	if _, _, err := s.Client.BulkSetEnable(s.Inbound, []string{node.Email}, enable); err != nil {
		return err
	}
	if err := s.BumpRevision(poolID); err != nil {
		return err
	}
	if s.Reconciler != nil {
		return s.Reconciler.Apply()
	}
	return nil
}

// DeletePool removes nodes, hops, proxies and the pool, disabling clients on
// the way out. Inbounds are left in place: they may still serve other pools.
func (s *Service) DeletePool(id int) error {
	db := database.GetDB()
	var nodes []model.ChainNode
	if err := db.Where("pool_id = ?", id).Find(&nodes).Error; err != nil {
		return err
	}
	for _, n := range nodes {
		if _, _, err := s.Client.BulkSetEnable(s.Inbound, []string{n.Email}, false); err != nil {
			return err
		}
		if _, err := s.Client.DeleteByEmail(s.Inbound, n.Email, false); err != nil {
			return err
		}
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("node_id IN (?)", db.Model(&model.ChainNode{}).Select("id").Where("pool_id = ?", id)).
			Delete(&model.ChainNodeHop{}).Error; err != nil {
			return err
		}
		if err := tx.Where("pool_id = ?", id).Delete(&model.ChainNode{}).Error; err != nil {
			return err
		}
		if err := tx.Where("pool_id = ?", id).Delete(&model.ChainProxy{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.ChainPool{}, id).Error; err != nil {
			return err
		}
		return nil
	})
}

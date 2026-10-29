package inject

import (
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/chain/linkparse"
	"github.com/mhsanaei/3x-ui/v3/internal/chain/secret"
	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// Inject is called at the end of XrayService.GetXrayConfig. It reads chain
// state and merges generated outbounds/rules into cfg. Any read or build error
// is returned, which makes the panel keep the previous config instead of
// publishing one whose customers could egress directly.
func Inject(cfg *xray.Config) error {
	if !config.IsChainEnabled() {
		return nil
	}
	db := database.GetDB()
	if db == nil {
		return nil
	}
	nodes, err := LoadNodes(db)
	if err != nil {
		if isMissingChainTable(err) {
			logger.Warning("chain: tables not migrated yet, skipping injection:", err)
			return nil
		}
		return fmt.Errorf("chain: load state: %w", err)
	}
	if len(nodes) == 0 {
		return nil
	}
	outbounds, rules, err := Build(nodes)
	if err != nil {
		return fmt.Errorf("chain: build config: %w", err)
	}
	if err := MergeConfig(cfg, outbounds, rules, AfterBlocks); err != nil {
		return fmt.Errorf("chain: merge config: %w", err)
	}
	return nil
}

// LoadNodes reads every node that must appear in the running config and
// decrypts its proxy credentials. Callers get plaintext only in memory.
func LoadNodes(db *gorm.DB) ([]Node, error) {
	var rows []model.ChainNode
	if err := db.Where("status IN ?", []string{"active", "degraded", "provisioning", "failed"}).
		Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	nodeIDs := make([]int, 0, len(rows))
	for _, r := range rows {
		nodeIDs = append(nodeIDs, r.Id)
	}
	var hopRows []model.ChainNodeHop
	if err := db.Where("node_id IN ?", nodeIDs).Order("node_id, position").
		Find(&hopRows).Error; err != nil {
		return nil, err
	}
	proxyIDs := make([]int, 0, len(hopRows))
	for _, h := range hopRows {
		if h.ProxyId != nil {
			proxyIDs = append(proxyIDs, *h.ProxyId)
		}
	}
	proxies := map[int]model.ChainProxy{}
	if len(proxyIDs) > 0 {
		var proxyRows []model.ChainProxy
		if err := db.Where("id IN ?", proxyIDs).Find(&proxyRows).Error; err != nil {
			return nil, err
		}
		for _, p := range proxyRows {
			proxies[p.Id] = p
		}
	}

	byNode := make(map[int][]model.ChainNodeHop, len(hopRows))
	for _, h := range hopRows {
		byNode[h.NodeId] = append(byNode[h.NodeId], h)
	}

	nodes := make([]Node, 0, len(rows))
	for _, r := range rows {
		n := Node{
			Id: r.Id, Email: r.Email, InboundTag: r.InboundTag,
			OutboundTag: r.OutboundTag, Status: r.Status,
		}
		for _, h := range byNode[r.Id] {
			hop := Hop{Tag: h.OutboundTag, External: h.ProxyId == nil}
			if h.ProxyId != nil {
				p, ok := proxies[*h.ProxyId]
				if !ok {
					return nil, fmt.Errorf("node %d: hop proxy %d missing", r.Id, *h.ProxyId)
				}
				plain, err := secret.Open(p.Id, p.PasswordEnc)
				if err != nil {
					return nil, fmt.Errorf("node %d: proxy %d: %w", r.Id, p.Id, err)
				}
				hop.Proxy = &linkparse.Proxy{
					Protocol: p.Protocol, Host: p.Host, Port: p.Port,
					Username: p.Username, Password: plain,
				}
			}
			n.Hops = append(n.Hops, hop)
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

// isMissingChainTable treats "table not created yet" as empty state: no nodes
// means no rules, so skipping injection cannot leak traffic.
func isMissingChainTable(err error) bool {
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "chain_") {
		return false
	}
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "does not exist")
}

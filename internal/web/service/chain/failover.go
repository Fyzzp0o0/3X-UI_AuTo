package chain

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// FailoverOptions tunes one failover pass. Force runs even when the pool did
// not opt in; Max caps how many nodes are touched in one pass.
type FailoverOptions struct {
	Force bool `json:"force"`
	Max   int  `json:"max"`
}

// FailoverResult reports what one pass changed.
type FailoverResult struct {
	Checked int      `json:"checked"`
	Rebound int      `json:"rebound"`
	Gated   int      `json:"gated"`
	NoSpare int      `json:"noSpare"`
	Skipped bool     `json:"skipped"`
	Details []string `json:"details,omitempty"`
}

const defaultFailoverMax = 20

// Failover rebinds nodes whose proxy died to a spare healthy proxy. When no
// spare exists the node is marked failed, which makes the injector point its
// rule at the blackhole instead of silently falling back to direct.
func (s *Service) Failover(ctx context.Context, poolID int, opt FailoverOptions) (*FailoverResult, error) {
	pool, err := s.GetPool(poolID)
	if err != nil {
		return nil, err
	}
	_, _, strategy, _ := parsePoolTemplates(pool)
	res := &FailoverResult{}
	if !strategy.AutoFailover && !opt.Force {
		res.Skipped = true
		return res, nil
	}
	limit := opt.Max
	if limit <= 0 || limit > defaultFailoverMax {
		limit = defaultFailoverMax
	}

	db := database.GetDB()
	var nodes []model.ChainNode
	if err := db.Where("pool_id = ? AND status IN ?", poolID, []string{"active", "degraded"}).
		Order("id").Find(&nodes).Error; err != nil {
		return nil, err
	}
	res.Checked = len(nodes)
	if len(nodes) == 0 {
		return res, nil
	}

	used, err := boundProxyIDs(db, poolID)
	if err != nil {
		return nil, err
	}
	changed := false
	for i := range nodes {
		if res.Rebound+res.Gated >= limit {
			break
		}
		node := &nodes[i]
		if node.ProxyId == nil || !proxyIsDead(db, *node.ProxyId) {
			continue
		}
		spare, err := pickSpare(db, poolID, used)
		if err != nil {
			return nil, err
		}
		if spare == nil {
			res.NoSpare++
			if err := s.gateNode(node, "no healthy spare proxy"); err != nil {
				return nil, err
			}
			res.Gated++
			res.Details = append(res.Details, fmt.Sprintf("%s gated: no spare", node.Name))
			changed = true
			continue
		}
		if err := s.rebind(db, node, spare.Id); err != nil {
			return nil, err
		}
		used[spare.Id] = struct{}{}
		delete(used, *node.ProxyId)
		res.Rebound++
		res.Details = append(res.Details, fmt.Sprintf("%s rebound to proxy %d", node.Name, spare.Id))
		changed = true
	}
	if changed {
		if err := s.BumpRevision(poolID); err != nil {
			return nil, err
		}
		if s.Reconciler != nil {
			if err := s.Reconciler.Apply(); err != nil {
				return res, fmt.Errorf("reconcile after failover: %w", err)
			}
		}
	}
	return res, nil
}

// RebindNode moves one node to a spare proxy and reconciles immediately.
func (s *Service) RebindNode(nodeID int) error {
	db := database.GetDB()
	var node model.ChainNode
	if err := db.First(&node, nodeID).Error; err != nil {
		return err
	}
	used, err := boundProxyIDs(db, node.PoolId)
	if err != nil {
		return err
	}
	spare, err := pickSpare(db, node.PoolId, used)
	if err != nil {
		return err
	}
	if spare == nil {
		return errors.New("no healthy spare proxy available")
	}
	if err := s.rebind(db, &node, spare.Id); err != nil {
		return err
	}
	if err := s.BumpRevision(node.PoolId); err != nil {
		return err
	}
	if s.Reconciler != nil {
		return s.Reconciler.Apply()
	}
	return nil
}

func boundProxyIDs(db *gorm.DB, poolID int) (map[int]struct{}, error) {
	var ids []int
	if err := db.Model(&model.ChainNode{}).
		Where("pool_id = ? AND proxy_id IS NOT NULL", poolID).
		Pluck("proxy_id", &ids).Error; err != nil {
		return nil, err
	}
	used := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		used[id] = struct{}{}
	}
	return used, nil
}

func proxyIsDead(db *gorm.DB, proxyID int) bool {
	var p model.ChainProxy
	if err := db.Select("health").First(&p, proxyID).Error; err != nil {
		return true
	}
	return p.Health == "unhealthy" || p.Health == "disabled"
}

// pickSpare returns a healthy, enabled, unbound proxy from the same pool.
func pickSpare(db *gorm.DB, poolID int, used map[int]struct{}) (*model.ChainProxy, error) {
	var candidates []model.ChainProxy
	if err := db.Where("pool_id = ? AND enabled = ? AND health = ?", poolID, true, "healthy").
		Order("id").Find(&candidates).Error; err != nil {
		return nil, err
	}
	for i := range candidates {
		if _, taken := used[candidates[i].Id]; taken {
			continue
		}
		return &candidates[i], nil
	}
	return nil, nil
}

// rebind swaps the landing proxy in one transaction. The outbound tag is
// derived from the node, not the proxy, so routing rules stay untouched.
func (s *Service) rebind(db *gorm.DB, node *model.ChainNode, proxyID int) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.ChainNode{}).Where("id = ?", node.Id).Updates(map[string]any{
			"proxy_id": proxyID, "status": "active", "client_enabled": true,
			"error": "", "bound_at": nowUnixMilli(),
		}).Error; err != nil {
			return err
		}
		return tx.Model(&model.ChainNodeHop{}).
			Where("node_id = ? AND position = 0", node.Id).
			Update("proxy_id", proxyID).Error
	})
}

// gateNode marks a node failed and disables its client; the injector replaces
// its rule with a blackhole, which is the fail-closed outcome.
func (s *Service) gateNode(node *model.ChainNode, reason string) error {
	if err := database.GetDB().Model(&model.ChainNode{}).Where("id = ?", node.Id).Updates(map[string]any{
		"status": "failed", "client_enabled": false, "error": truncate(reason, 255),
	}).Error; err != nil {
		return err
	}
	_, _, err := s.Client.BulkSetEnable(s.Inbound, []string{node.Email}, false)
	return err
}

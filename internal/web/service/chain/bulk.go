package chain

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// BulkNodeAction is one of the batch operations the node matrix exposes.
type BulkNodeAction string

const (
	NodeEnable  BulkNodeAction = "enable"
	NodeDisable BulkNodeAction = "disable"
	NodeDelete  BulkNodeAction = "delete"
	NodeRebind  BulkNodeAction = "rebind"
)

// BulkNodes applies one action to a selection with a single reconcile, so a
// 200-node batch does not trigger 200 hot applies.
func (s *Service) BulkNodes(ids []int, action BulkNodeAction) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	if len(ids) > 500 {
		return 0, errors.New("bulk operations are limited to 500 nodes")
	}
	db := database.GetDB()
	var nodes []model.ChainNode
	if err := db.Where("id IN ?", ids).Order("id").Find(&nodes).Error; err != nil {
		return 0, err
	}
	if len(nodes) == 0 {
		return 0, nil
	}

	affected := 0
	emails := make([]string, 0, len(nodes))
	affectedIDs := make([]int, 0, len(nodes))
	pools := map[int]struct{}{}

	switch action {
	case NodeEnable, NodeDisable:
		for _, n := range nodes {
			if action == NodeEnable && n.Status != "disabled" {
				continue
			}
			if action == NodeDisable && n.Status == "disabled" {
				continue
			}
			affectedIDs = append(affectedIDs, n.Id)
			emails = append(emails, n.Email)
			pools[n.PoolId] = struct{}{}
		}
		if len(affectedIDs) == 0 {
			return 0, nil
		}
		status, enabled := "active", true
		if action == NodeDisable {
			status, enabled = "disabled", false
		}
		if err := db.Model(&model.ChainNode{}).Where("id IN ?", affectedIDs).
			Updates(map[string]any{"status": status, "client_enabled": enabled}).Error; err != nil {
			return 0, err
		}
		if _, _, err := s.Client.BulkSetEnable(s.Inbound, emails, enabled); err != nil {
			return 0, err
		}
		affected = len(affectedIDs)

	case NodeDelete:
		for _, n := range nodes {
			_, _, _ = s.Client.BulkSetEnable(s.Inbound, []string{n.Email}, false)
			if _, err := s.Client.DeleteByEmail(s.Inbound, n.Email, false); err != nil {
				return affected, err
			}
			if err := db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Where("node_id = ?", n.Id).Delete(&model.ChainNodeHop{}).Error; err != nil {
					return err
				}
				return tx.Delete(&model.ChainNode{}, n.Id).Error
			}); err != nil {
				return affected, err
			}
			pools[n.PoolId] = struct{}{}
			affected++
		}

	case NodeRebind:
		used := map[int]map[int]struct{}{}
		for i := range nodes {
			n := &nodes[i]
			if n.ProxyId == nil {
				continue
			}
			if _, ok := used[n.PoolId]; !ok {
				bound, err := boundProxyIDs(db, n.PoolId)
				if err != nil {
					return affected, err
				}
				used[n.PoolId] = bound
			}
			spare, err := pickSpare(db, n.PoolId, used[n.PoolId])
			if err != nil {
				return affected, err
			}
			if spare == nil {
				continue
			}
			if err := s.rebind(db, n, spare.Id); err != nil {
				return affected, err
			}
			used[n.PoolId][spare.Id] = struct{}{}
			delete(used[n.PoolId], *n.ProxyId)
			pools[n.PoolId] = struct{}{}
			affected++
		}

	default:
		return 0, fmt.Errorf("unknown action %q", action)
	}

	if affected == 0 {
		return 0, nil
	}
	for poolID := range pools {
		if err := s.BumpRevision(poolID); err != nil {
			return affected, err
		}
	}
	if s.Reconciler != nil {
		if err := s.Reconciler.Apply(); err != nil {
			return affected, fmt.Errorf("reconcile after bulk action: %w", err)
		}
	}
	return affected, nil
}

// ExportLinks collects share links for the selection, or for a whole pool when
// ids is empty. Duplicates are removed and order follows the node id.
func (s *Service) ExportLinks(ids []int, poolID int, host string) ([]string, error) {
	db := database.GetDB()
	q := db.Model(&model.ChainNode{})
	if len(ids) > 0 && poolID > 0 {
		q = q.Where("pool_id = ? OR id IN ?", poolID, ids)
	} else if poolID > 0 {
		q = q.Where("pool_id = ?", poolID)
	} else if len(ids) > 0 {
		q = q.Where("id IN ?", ids)
	} else {
		return nil, errors.New("select nodes or a pool to export")
	}
	var nodes []model.ChainNode
	if err := q.Order("id").Find(&nodes).Error; err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		for _, link := range s.NodeLinks(n.Id, host) {
			if _, dup := seen[link]; dup {
				continue
			}
			seen[link] = struct{}{}
			out = append(out, link)
		}
	}
	return out, nil
}

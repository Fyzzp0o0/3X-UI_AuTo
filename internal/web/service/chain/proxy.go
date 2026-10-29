package chain

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/chain/linkparse"
	"github.com/mhsanaei/3x-ui/v3/internal/chain/secret"
	"github.com/mhsanaei/3x-ui/v3/internal/chain/socksprobe"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// ProxyInput edits one stored proxy. A nil Password keeps the stored secret; an
// empty string clears it; any other value is sealed before it reaches the DB.
type ProxyInput struct {
	Host         string  `json:"host"`
	Port         int     `json:"port"`
	Username     string  `json:"username"`
	Password     *string `json:"password"`
	Label        string  `json:"label"`
	AllowPrivate bool    `json:"allowPrivate"`
	Enabled      *bool   `json:"enabled"`
}

// UpdateProxy validates and rewrites a proxy, then reconciles when a node is
// already using it so the running core picks up new credentials immediately.
func (s *Service) UpdateProxy(proxyID int, in ProxyInput) error {
	db := database.GetDB()
	var proxy model.ChainProxy
	if err := db.First(&proxy, proxyID).Error; err != nil {
		return err
	}
	candidate := linkparse.Proxy{
		Protocol: proxy.Protocol, Host: in.Host, Port: in.Port, Username: in.Username,
	}
	if err := linkparse.ValidateProxy(candidate, linkparse.Options{AllowPrivate: in.AllowPrivate}); err != nil {
		return err
	}
	updates := map[string]any{
		"host": in.Host, "port": in.Port, "username": in.Username,
		"label": in.Label, "allow_private": in.AllowPrivate,
		"fingerprint": linkparse.Fingerprint(candidate),
	}
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
		if !*in.Enabled {
			updates["health"] = "disabled"
		} else if proxy.Health == "disabled" {
			updates["health"] = "unknown"
		}
	}
	if in.Password != nil {
		if *in.Password == "" {
			updates["password_enc"] = ""
		} else {
			sealed, err := secret.Seal(proxy.Id, *in.Password)
			if err != nil {
				return err
			}
			updates["password_enc"] = sealed
		}
	}
	// Re-inserting the same fingerprint on another row of the pool is a conflict.
	var clash int64
	if err := db.Model(&model.ChainProxy{}).
		Where("pool_id = ? AND fingerprint = ? AND id <> ?", proxy.PoolId, updates["fingerprint"], proxy.Id).
		Count(&clash).Error; err != nil {
		return err
	}
	if clash > 0 {
		return errors.New("another proxy in this pool already uses that address and username")
	}
	if err := db.Model(&model.ChainProxy{}).Where("id = ?", proxy.Id).Updates(updates).Error; err != nil {
		return err
	}
	return s.reconcileAfterProxyChange(proxy.PoolId, proxy.Id)
}

// reconcileAfterProxyChange only triggers a config apply when a node uses the
// proxy; unused pool entries do not need to churn the core.
func (s *Service) reconcileAfterProxyChange(poolID, proxyID int) error {
	var bound int64
	if err := database.GetDB().Model(&model.ChainNode{}).
		Where("pool_id = ? AND proxy_id = ? AND status IN ?", poolID, proxyID,
			[]string{"active", "degraded", "provisioning", "failed"}).Count(&bound).Error; err != nil {
		return err
	}
	if bound == 0 {
		return nil
	}
	if err := s.BumpRevision(poolID); err != nil {
		return err
	}
	if s.Reconciler != nil {
		return s.Reconciler.Apply()
	}
	return nil
}

// ProbeProxy refreshes one proxy's health without waiting for the pool job.
func (s *Service) ProbeProxy(ctx context.Context, proxyID int) (*socksprobe.Result, error) {
	db := database.GetDB()
	var proxy model.ChainProxy
	if err := db.First(&proxy, proxyID).Error; err != nil {
		return nil, err
	}
	pool, err := s.GetPool(proxy.PoolId)
	if err != nil {
		return nil, err
	}
	_, _, _, policy := parsePoolTemplates(pool)
	timeout := time.Duration(policy.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	auth := socksprobe.Auth{}
	if proxy.Username != "" {
		plain, err := secret.Open(proxy.Id, proxy.PasswordEnc)
		if err != nil {
			return nil, err
		}
		auth = socksprobe.Auth{Username: proxy.Username, Password: plain}
	}
	opt := socksprobe.Options{Timeout: timeout, AllowPrivate: proxy.AllowPrivate}
	res := socksprobe.ProbeSOCKS(ctx, proxy.Host, proxy.Port, auth, probeTarget, opt)
	exitIP := ""
	if res.OK && policy.URL != "" {
		httpRes := socksprobe.HTTPGet(ctx, proxy.Host, proxy.Port, auth, policy.URL, opt)
		if httpRes.OK {
			exitIP = httpRes.ExitIP
			res.LatencyMs = httpRes.LatencyMs
		}
	}
	health := healthFor(res.OK, proxy.FailCount+1, res.Err)
	s.storeProbe(proxy.Id, health, res.LatencyMs, exitIP, res.Err)
	return &res, nil
}

// DeleteNode removes one node, its client and hops, then reconciles once.
func (s *Service) DeleteNode(nodeID int) error {
	db := database.GetDB()
	var node model.ChainNode
	if err := db.First(&node, nodeID).Error; err != nil {
		return err
	}
	_, _, _ = s.Client.BulkSetEnable(s.Inbound, []string{node.Email}, false)
	if _, err := s.Client.DeleteByEmail(s.Inbound, node.Email, false); err != nil {
		return fmt.Errorf("delete client: %w", err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("node_id = ?", nodeID).Delete(&model.ChainNodeHop{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.ChainNode{}, nodeID).Error
	}); err != nil {
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

// NodeTraffic returns the panel's client traffic counters for a node email.
func (s *Service) NodeTraffic(email string) (up, down int64, err error) {
	var row xray.ClientTraffic
	res := database.GetDB().Where("email = ?", email).First(&row)
	if errors.Is(res.Error, gorm.ErrRecordNotFound) {
		return 0, 0, nil
	}
	if res.Error != nil {
		return 0, 0, res.Error
	}
	return row.Up, row.Down, nil
}

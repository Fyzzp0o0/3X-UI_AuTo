package chain

import (
	"context"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/chain/secret"
	"github.com/mhsanaei/3x-ui/v3/internal/chain/socksprobe"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// ProbeSummary is the outcome of one pool probe run.
type ProbeSummary struct {
	Total     int `json:"total"`
	Healthy   int `json:"healthy"`
	Degraded  int `json:"degraded"`
	Unhealthy int `json:"unhealthy"`
	Skipped   int `json:"skipped"`
}

// ProbeOptions tunes one run; zero values fall back to the pool policy.
type ProbeOptions struct {
	Concurrency int
	Timeout     time.Duration
	// Force re-probes proxies that are already healthy.
	Force bool
}

const (
	defaultProbeConcurrency = 16
	defaultProbeTimeout     = 5 * time.Second
	probeTarget             = "example.com:443"
	unhealthyAfterFailures  = 3
)

// ProbePool runs L2 (and L3 when the policy has a URL) against the pool's
// enabled proxies and persists the result. It never returns an error for a
// single dead proxy: those are data, not failures.
func (s *Service) ProbePool(ctx context.Context, poolID int, opt ProbeOptions) (*ProbeSummary, error) {
	pool, err := s.GetPool(poolID)
	if err != nil {
		return nil, err
	}
	_, _, _, policy := parsePoolTemplates(pool)
	concurrency := opt.Concurrency
	if concurrency <= 0 {
		concurrency = policy.Concurrency
	}
	if concurrency <= 0 {
		concurrency = defaultProbeConcurrency
	}
	if concurrency > 32 {
		concurrency = 32
	}
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = time.Duration(policy.TimeoutMs) * time.Millisecond
	}
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}

	var proxies []model.ChainProxy
	if err := database.GetDB().Where("pool_id = ? AND enabled = ?", poolID, true).
		Order("id").Find(&proxies).Error; err != nil {
		return nil, err
	}
	summary := &ProbeSummary{Total: len(proxies)}
	if len(proxies) == 0 {
		return summary, nil
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	for i := range proxies {
		p := proxies[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			auth := socksprobe.Auth{}
			if p.Username != "" {
				plain, decErr := secret.Open(p.Id, p.PasswordEnc)
				if decErr != nil {
					mu.Lock()
					summary.Unhealthy++
					mu.Unlock()
					s.storeProbe(p.Id, "unhealthy", 0, "", decErr.Error())
					return
				}
				auth = socksprobe.Auth{Username: p.Username, Password: plain}
			}
			probeOpt := socksprobe.Options{Timeout: timeout, AllowPrivate: p.AllowPrivate}
			res := socksprobe.ProbeSOCKS(ctx, p.Host, p.Port, auth, probeTarget, probeOpt)
			exitIP := ""
			if res.OK && policy.URL != "" {
				httpRes := socksprobe.HTTPGet(ctx, p.Host, p.Port, auth, policy.URL, probeOpt)
				if httpRes.OK {
					exitIP = httpRes.ExitIP
					res.LatencyMs = httpRes.LatencyMs
				}
			}
			mu.Lock()
			if res.OK {
				summary.Healthy++
			} else if p.FailCount+1 >= unhealthyAfterFailures || res.Err == socksprobe.ErrBlocked {
				summary.Unhealthy++
			} else {
				summary.Degraded++
			}
			mu.Unlock()
			s.storeProbe(p.Id, healthFor(res.OK, p.FailCount+1, res.Err), res.LatencyMs, exitIP, res.Err)
		}()
	}
	wg.Wait()
	return summary, nil
}

func healthFor(ok bool, failures int, code string) string {
	if ok {
		return "healthy"
	}
	if code == socksprobe.ErrBlocked || failures >= unhealthyAfterFailures {
		return "unhealthy"
	}
	return "degraded"
}

// storeProbe persists one probe outcome; a failure here is logged by the caller
// through the job message rather than aborting the whole run.
func (s *Service) storeProbe(proxyID int, health string, latencyMs int64, exitIP, errCode string) {
	updates := map[string]any{
		"health": health, "latency_ms": latencyMs, "last_probe_at": time.Now().UnixMilli(),
		"last_error": errCode, "exit_ip": exitIP,
	}
	if health == "healthy" {
		updates["fail_count"] = 0
	} else {
		updates["fail_count"] = databaseFailureCount(proxyID)
	}
	_ = database.GetDB().Model(&model.ChainProxy{}).Where("id = ?", proxyID).Updates(updates).Error
}

// databaseFailureCount reads the current counter so the update can saturate it.
func databaseFailureCount(proxyID int) int {
	var p model.ChainProxy
	if err := database.GetDB().Select("fail_count").First(&p, proxyID).Error; err != nil {
		return 1
	}
	return p.FailCount + 1
}

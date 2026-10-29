package job

import (
	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	chainsvc "github.com/mhsanaei/3x-ui/v3/internal/web/service/chain"
)

// ChainProbeJob refreshes the health of every enabled pool. A pool whose
// previous job is still running is skipped by the service, so a slow probe
// cannot pile up on the next tick.
type ChainProbeJob struct{}

// NewChainProbeJob builds the cron job.
func NewChainProbeJob() *ChainProbeJob { return &ChainProbeJob{} }

// Run probes all enabled pools.
func (j *ChainProbeJob) Run() {
	if !config.IsChainEnabled() {
		return
	}
	svc := chainsvc.NewService()
	pools, err := svc.ListPools()
	if err != nil {
		logger.Warning("chain probe: list pools:", err)
		return
	}
	for _, p := range pools {
		if !p.Enabled {
			continue
		}
		if _, err := svc.StartProbeJob(p.Id, "cron"); err != nil {
			logger.Debug("chain probe: pool", p.Id, "skipped:", err)
		}
	}
}

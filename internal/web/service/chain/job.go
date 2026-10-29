package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// Job statuses shared with the UI.
const (
	JobQueued    = "queued"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobPartial   = "partial"
	JobFailed    = "failed"
)

// PartialError marks a run that changed state but did not fully succeed, e.g.
// some nodes failed the activation self-check. The job is reported as partial.
type PartialError struct{ Message string }

func (e *PartialError) Error() string { return e.Message }

// StartApplyJob queues one generation run and returns immediately.
func (s *Service) StartApplyJob(poolID int, opt ApplyOptions, operator string) (*model.ChainJob, error) {
	job, err := s.newJob(poolID, "generate", operator)
	if err != nil {
		return nil, err
	}
	go func() {
		s.runJob(job.Id, func(ctx context.Context) (string, error) {
			res, err := s.Apply(ctx, poolID, opt)
			// Keep the partial result in the job detail even when it failed.
			return marshalDetail(res), err
		})
	}()
	return job, nil
}

// StartProbeJob queues one probe run for a pool.
func (s *Service) StartProbeJob(poolID int, operator string) (*model.ChainJob, error) {
	if _, err := s.GetPool(poolID); err != nil {
		return nil, err
	}
	job, err := s.newJob(poolID, "probe", operator)
	if err != nil {
		return nil, err
	}
	go func() {
		s.runJob(job.Id, func(ctx context.Context) (string, error) {
			summary, err := s.ProbePool(ctx, poolID, ProbeOptions{})
			if err != nil {
				return marshalDetail(summary), err
			}
			detail := map[string]any{"probe": summary}
			if pool, perr := s.GetPool(poolID); perr == nil {
				_, _, strategy, _ := parsePoolTemplates(pool)
				if strategy.AutoFailover {
					fo, ferr := s.Failover(ctx, poolID, FailoverOptions{})
					if ferr != nil {
						return marshalDetail(detail), ferr
					}
					detail["failover"] = fo
					if fo.NoSpare > 0 || fo.Gated > 0 {
						return marshalDetail(detail), &PartialError{Message: fmt.Sprintf("%d nodes gated: no spare proxy", fo.Gated)}
					}
				}
			}
			return marshalDetail(detail), nil
		})
	}()
	return job, nil
}

// StartFailoverJob queues one manual failover pass for a pool.
func (s *Service) StartFailoverJob(poolID int, operator string) (*model.ChainJob, error) {
	if _, err := s.GetPool(poolID); err != nil {
		return nil, err
	}
	job, err := s.newJob(poolID, "failover", operator)
	if err != nil {
		return nil, err
	}
	go func() {
		s.runJob(job.Id, func(ctx context.Context) (string, error) {
			res, err := s.Failover(ctx, poolID, FailoverOptions{Force: true})
			return marshalDetail(res), err
		})
	}()
	return job, nil
}

// GetJob loads one job row.
func (s *Service) GetJob(id int) (*model.ChainJob, error) {
	var job model.ChainJob
	if err := database.GetDB().First(&job, id).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

// ListJobs returns recent jobs, optionally scoped to one pool.
func (s *Service) ListJobs(poolID int, limit int) ([]model.ChainJob, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	db := database.GetDB().Model(&model.ChainJob{})
	if poolID > 0 {
		db = db.Where("pool_id = ?", poolID)
	}
	var jobs []model.ChainJob
	err := db.Order("id DESC").Limit(limit).Find(&jobs).Error
	return jobs, err
}

func (s *Service) newJob(poolID int, kind, operator string) (*model.ChainJob, error) {
	db := database.GetDB()
	var running int64
	if err := db.Model(&model.ChainJob{}).
		Where("pool_id = ? AND status IN ?", poolID, []string{JobQueued, JobRunning}).
		Count(&running).Error; err != nil {
		return nil, err
	}
	if running > 0 {
		return nil, fmt.Errorf("a %s job is already running for this pool", kind)
	}
	job := &model.ChainJob{PoolId: &poolID, Kind: kind, Status: JobQueued, Operator: operator}
	if err := db.Create(job).Error; err != nil {
		return nil, err
	}
	return job, nil
}

// runJob owns the job lifecycle; every exit path writes a terminal status so a
// crashed run can never look perpetually "running".
func (s *Service) runJob(jobID int, run func(ctx context.Context) (string, error)) {
	db := database.GetDB()
	_ = db.Model(&model.ChainJob{}).Where("id = ?", jobID).
		Updates(map[string]any{"status": JobRunning, "started_at": time.Now().UnixMilli()}).Error

	detail, err := run(context.Background())

	status := JobSucceeded
	message := ""
	if err != nil {
		status = JobFailed
		var partial *PartialError
		if errors.As(err, &partial) {
			status = JobPartial
		}
		message = err.Error()
	}
	updates := map[string]any{
		"status": status, "message": truncate(message, 512), "detail": truncate(detail, 64<<10),
		"finished_at": time.Now().UnixMilli(),
	}
	_ = db.Model(&model.ChainJob{}).Where("id = ?", jobID).Updates(updates).Error
}

func marshalDetail(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(raw)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

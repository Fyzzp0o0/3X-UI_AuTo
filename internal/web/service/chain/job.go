package chain

import (
	"context"
	"encoding/json"
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

// StartApplyJob queues one generation run and returns immediately.
func (s *Service) StartApplyJob(poolID int, opt ApplyOptions, operator string) (*model.ChainJob, error) {
	job, err := s.newJob(poolID, "generate", operator)
	if err != nil {
		return nil, err
	}
	go func() {
		s.runJob(job.Id, func(ctx context.Context) (string, error) {
			res, err := s.Apply(ctx, poolID, opt)
			if err != nil {
				return "", err
			}
			return marshalDetail(res), nil
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
				return "", err
			}
			return marshalDetail(summary), nil
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

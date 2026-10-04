package store

import (
	"context"
	"sort"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func (m *Memory) ListModelExperienceRuns(_ context.Context, organizationID, centerUserID string) ([]domain.ModelExperienceRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	now := time.Now().UTC()
	result := make([]domain.ModelExperienceRun, 0)
	for _, run := range m.modelRuns {
		if run.OrganizationID == organizationID && run.CenterUserID == centerUserID && now.Before(run.ExpiresAt) {
			result = append(result, run)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

func (m *Memory) GetModelExperienceRun(_ context.Context, organizationID, centerUserID, runID string) (domain.ModelExperienceRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok := m.modelRuns[runID]
	if !ok || run.OrganizationID != organizationID || run.CenterUserID != centerUserID || !time.Now().UTC().Before(run.ExpiresAt) {
		return domain.ModelExperienceRun{}, domain.NewError(404, "experience_run_not_found", "体验任务不存在或已过期")
	}
	return run, nil
}

func (m *Memory) FindModelExperienceRunByRequestID(_ context.Context, organizationID, requestID string) (domain.ModelExperienceRun, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, run := range m.modelRuns {
		if run.OrganizationID == organizationID && run.RequestID == requestID && time.Now().UTC().Before(run.ExpiresAt) {
			return run, true, nil
		}
	}
	return domain.ModelExperienceRun{}, false, nil
}

func (m *Memory) CreateModelExperienceRun(_ context.Context, run domain.ModelExperienceRun) (domain.ModelExperienceRun, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.modelRuns == nil {
		m.modelRuns = make(map[string]domain.ModelExperienceRun)
	}
	now := time.Now().UTC()
	active := 0
	for _, existing := range m.modelRuns {
		if existing.OrganizationID == run.OrganizationID && existing.RequestID == run.RequestID {
			if existing.CenterUserID == run.CenterUserID && existing.ModelID == run.ModelID && existing.Prompt == run.Prompt && now.Before(existing.ExpiresAt) {
				return existing, false, nil
			}
			return domain.ModelExperienceRun{}, false, domain.NewError(409, "experience_request_conflict", "请求 ID 已用于其他体验任务")
		}
		if existing.OrganizationID == run.OrganizationID && existing.Status == "submitting" && now.Sub(existing.CreatedAt) < 2*time.Minute {
			active++
		}
	}
	if active >= 1 {
		return domain.ModelExperienceRun{}, false, domain.NewError(409, "experience_concurrency_exceeded", "当前组织已有正在执行的模型体验")
	}
	m.modelRuns[run.ID] = run
	return run, true, nil
}

func (m *Memory) FinishModelExperienceRun(_ context.Context, organizationID, centerUserID, runID, status, response, errorCode string, promptTokens, outputTokens, totalTokens int) (domain.ModelExperienceRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.modelRuns[runID]
	if !ok || run.OrganizationID != organizationID || run.CenterUserID != centerUserID {
		return domain.ModelExperienceRun{}, domain.NewError(404, "experience_run_not_found", "体验任务不存在")
	}
	if run.Status != "submitting" {
		return run, nil
	}
	now := time.Now().UTC()
	run.Status = status
	run.Response = response
	run.ErrorCode = errorCode
	run.PromptTokens = promptTokens
	run.OutputTokens = outputTokens
	run.TotalTokens = totalTokens
	run.CompletedAt = &now
	m.modelRuns[runID] = run
	return run, nil
}

func (m *Memory) SweepModelExperienceRuns(_ context.Context, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, run := range m.modelRuns {
		if !now.Before(run.ExpiresAt) {
			delete(m.modelRuns, id)
			continue
		}
		if run.Status == "submitting" && now.Sub(run.CreatedAt) >= 2*time.Minute {
			run.Status = "outcome_unknown"
			run.ErrorCode = "submission_interrupted"
			run.CompletedAt = &now
			m.modelRuns[id] = run
		}
	}
	return nil
}

func (p *Postgres) ListModelExperienceRuns(ctx context.Context, organizationID, centerUserID string) ([]domain.ModelExperienceRun, error) {
	m, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	return m.ListModelExperienceRuns(ctx, organizationID, centerUserID)
}

func (p *Postgres) GetModelExperienceRun(ctx context.Context, organizationID, centerUserID, runID string) (domain.ModelExperienceRun, error) {
	m, err := p.read(ctx)
	if err != nil {
		return domain.ModelExperienceRun{}, err
	}
	return m.GetModelExperienceRun(ctx, organizationID, centerUserID, runID)
}

func (p *Postgres) FindModelExperienceRunByRequestID(ctx context.Context, organizationID, requestID string) (domain.ModelExperienceRun, bool, error) {
	m, err := p.read(ctx)
	if err != nil {
		return domain.ModelExperienceRun{}, false, err
	}
	return m.FindModelExperienceRunByRequestID(ctx, organizationID, requestID)
}

func (p *Postgres) CreateModelExperienceRun(ctx context.Context, run domain.ModelExperienceRun) (result domain.ModelExperienceRun, created bool, err error) {
	err = p.mutate(ctx, func(m *Memory) error {
		var e error
		result, created, e = m.CreateModelExperienceRun(ctx, run)
		return e
	})
	return
}

func (p *Postgres) FinishModelExperienceRun(ctx context.Context, organizationID, centerUserID, runID, status, response, errorCode string, promptTokens, outputTokens, totalTokens int) (result domain.ModelExperienceRun, err error) {
	err = p.mutate(ctx, func(m *Memory) error {
		var e error
		result, e = m.FinishModelExperienceRun(ctx, organizationID, centerUserID, runID, status, response, errorCode, promptTokens, outputTokens, totalTokens)
		return e
	})
	return
}

func (p *Postgres) SweepModelExperienceRuns(ctx context.Context, now time.Time) error {
	return p.mutate(ctx, func(m *Memory) error { return m.SweepModelExperienceRuns(ctx, now) })
}

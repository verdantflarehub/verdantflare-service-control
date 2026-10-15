package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"sort"
)

type SurveyRepository interface {
	ReadSurvey(context.Context) (domain.SurveyState, error)
	UpdateSurvey(context.Context, func(*domain.SurveyState) error) error
	ListSurveys(context.Context) ([]domain.SurveyState, error)
	CreateSurvey(context.Context, domain.SurveyMetadata) (domain.SurveyMetadata, error)
}

type surveyContextKey struct{}

func WithSurveyID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, surveyContextKey{}, id)
}
func SurveyID(ctx context.Context) string {
	if id, ok := ctx.Value(surveyContextKey{}).(string); ok && id != "" {
		return id
	}
	return domain.SurveyID
}

func surveyMissing() error { return domain.NewError(404, "survey_not_found", "未找到该问卷") }

func surveyMetadata(id string, state domain.SurveyState) domain.SurveyMetadata {
	if state.Metadata != nil {
		return *state.Metadata
	}
	return domain.SurveyMetadata{ID: id, Name: "云南交流", Description: "需求反馈"}
}

func copySurvey(state domain.SurveyState) (domain.SurveyState, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return domain.SurveyState{}, err
	}
	var result domain.SurveyState
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	if result.Challenges == nil {
		result.Challenges = map[string]domain.SurveyChallenge{}
	}
	if result.Guests == nil {
		result.Guests = map[string]domain.SurveyGuest{}
	}
	if result.Rates == nil {
		result.Rates = map[string]domain.SurveyRate{}
	}
	if result.Responses == nil {
		result.Responses = map[string]domain.SurveyResponse{}
	}
	return result, nil
}
func (m *Memory) ReadSurvey(ctx context.Context) (domain.SurveyState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, exists := m.surveys[SurveyID(ctx)]
	if !exists {
		return domain.SurveyState{}, surveyMissing()
	}
	return copySurvey(state)
}
func (m *Memory) UpdateSurvey(ctx context.Context, update func(*domain.SurveyState) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := SurveyID(ctx)
	current, exists := m.surveys[id]
	if !exists {
		return surveyMissing()
	}
	state, err := copySurvey(current)
	if err != nil {
		return err
	}
	if err = update(&state); err != nil {
		return err
	}
	m.surveys[id] = state
	return nil
}
func (m *Memory) ListSurveys(_ context.Context) ([]domain.SurveyState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]domain.SurveyState, 0, len(m.surveys))
	for id, state := range m.surveys {
		copy, err := copySurvey(state)
		if err != nil {
			return nil, err
		}
		meta := surveyMetadata(id, copy)
		copy.Metadata = &meta
		result = append(result, copy)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Metadata.ID < result[j].Metadata.ID })
	return result, nil
}
func (m *Memory) CreateSurvey(_ context.Context, meta domain.SurveyMetadata) (domain.SurveyMetadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, exists := m.surveys[meta.ID]; exists {
		previous := surveyMetadata(meta.ID, old)
		if previous.Name == meta.Name && previous.Description == meta.Description && previous.CreatedBy == meta.CreatedBy {
			return previous, nil
		}
		return domain.SurveyMetadata{}, domain.NewError(409, "survey_create_conflict", "创建请求已用于另一份问卷")
	}
	if len(m.surveys) >= 100 {
		return domain.SurveyMetadata{}, domain.NewError(409, "survey_limit", "问卷数量已达上限")
	}
	m.surveys[meta.ID] = domain.SurveyState{Metadata: &meta}
	return meta, nil
}
func (p *Postgres) ReadSurvey(ctx context.Context) (domain.SurveyState, error) {
	var raw []byte
	if err := p.db.QueryRowContext(ctx, `SELECT state FROM control_survey_state WHERE survey_id=$1`, SurveyID(ctx)).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return domain.SurveyState{}, surveyMissing()
		}
		return domain.SurveyState{}, fmt.Errorf("read survey repository: %w", err)
	}
	var state domain.SurveyState
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	return copySurvey(state)
}
func (p *Postgres) UpdateSurvey(ctx context.Context, update func(*domain.SurveyState) error) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT state FROM control_survey_state WHERE survey_id=$1 FOR UPDATE`, SurveyID(ctx)).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return surveyMissing()
		}
		return err
	}
	var state domain.SurveyState
	if err = json.Unmarshal(raw, &state); err != nil {
		return err
	}
	state, err = copySurvey(state)
	if err != nil {
		return err
	}
	if err = update(&state); err != nil {
		return err
	}
	raw, err = json.Marshal(state)
	if err != nil {
		return err
	}
	if len(raw) > 64<<20 {
		return domain.NewError(503, "survey_capacity", "本次问卷存储已达上限，请联系组织方")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE control_survey_state SET state=$1, updated_at=NOW() WHERE survey_id=$2`, raw, SurveyID(ctx)); err != nil {
		return err
	}
	return tx.Commit()
}
func (p *Postgres) ListSurveys(ctx context.Context) ([]domain.SurveyState, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT survey_id, state FROM control_survey_state ORDER BY survey_id LIMIT 101`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.SurveyState{}
	for rows.Next() {
		var id string
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var state domain.SurveyState
		if err = json.Unmarshal(raw, &state); err != nil {
			return nil, err
		}
		meta := surveyMetadata(id, state)
		state.Metadata = &meta
		result = append(result, state)
	}
	return result, rows.Err()
}
func (p *Postgres) CreateSurvey(ctx context.Context, meta domain.SurveyMetadata) (domain.SurveyMetadata, error) {
	state, err := json.Marshal(domain.SurveyState{Metadata: &meta})
	if err != nil {
		return domain.SurveyMetadata{}, err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.SurveyMetadata{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `LOCK TABLE control_survey_state IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return domain.SurveyMetadata{}, err
	}
	var existing []byte
	if err = tx.QueryRowContext(ctx, `SELECT state FROM control_survey_state WHERE survey_id=$1`, meta.ID).Scan(&existing); err == nil {
		var saved domain.SurveyState
		if err = json.Unmarshal(existing, &saved); err != nil {
			return domain.SurveyMetadata{}, err
		}
		if saved.Metadata == nil || saved.Metadata.Name != meta.Name || saved.Metadata.Description != meta.Description || saved.Metadata.CreatedBy != meta.CreatedBy {
			return domain.SurveyMetadata{}, domain.NewError(409, "survey_create_conflict", "创建请求已用于另一份问卷")
		}
		return *saved.Metadata, tx.Commit()
	} else if err != sql.ErrNoRows {
		return domain.SurveyMetadata{}, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM control_survey_state`).Scan(&count); err != nil {
		return domain.SurveyMetadata{}, err
	}
	if count >= 100 {
		return domain.SurveyMetadata{}, domain.NewError(409, "survey_limit", "问卷数量已达上限")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO control_survey_state (survey_id, state) VALUES ($1,$2)`, meta.ID, state); err != nil {
		return domain.SurveyMetadata{}, err
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT state FROM control_survey_state WHERE survey_id=$1`, meta.ID).Scan(&raw); err != nil {
		return domain.SurveyMetadata{}, err
	}
	var saved domain.SurveyState
	if err = json.Unmarshal(raw, &saved); err != nil {
		return domain.SurveyMetadata{}, err
	}
	if saved.Metadata == nil || saved.Metadata.Name != meta.Name || saved.Metadata.Description != meta.Description || saved.Metadata.CreatedBy != meta.CreatedBy {
		return domain.SurveyMetadata{}, domain.NewError(409, "survey_create_conflict", "创建请求已用于另一份问卷")
	}
	if err = tx.Commit(); err != nil {
		return domain.SurveyMetadata{}, err
	}
	return *saved.Metadata, nil
}

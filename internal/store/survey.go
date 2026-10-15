package store

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

type SurveyRepository interface {
	ReadSurvey(context.Context) (domain.SurveyState, error)
	UpdateSurvey(context.Context, func(*domain.SurveyState) error) error
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
func (m *Memory) ReadSurvey(_ context.Context) (domain.SurveyState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return copySurvey(m.survey)
}
func (m *Memory) UpdateSurvey(_ context.Context, update func(*domain.SurveyState) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := copySurvey(m.survey)
	if err != nil {
		return err
	}
	if err = update(&state); err != nil {
		return err
	}
	m.survey = state
	return nil
}
func (p *Postgres) ReadSurvey(ctx context.Context) (domain.SurveyState, error) {
	var raw []byte
	if err := p.db.QueryRowContext(ctx, `SELECT state FROM control_survey_state WHERE survey_id=$1`, domain.SurveyID).Scan(&raw); err != nil {
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
	if err = tx.QueryRowContext(ctx, `SELECT state FROM control_survey_state WHERE survey_id=$1 FOR UPDATE`, domain.SurveyID).Scan(&raw); err != nil {
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
	if _, err = tx.ExecContext(ctx, `UPDATE control_survey_state SET state=$1, updated_at=NOW() WHERE survey_id=$2`, raw, domain.SurveyID); err != nil {
		return err
	}
	return tx.Commit()
}

package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"testing"
	"time"
)

func TestSurveySnapshotAndRollback(t *testing.T) {
	repo := NewMemoryBootstrap()
	ctx := context.Background()
	if err := repo.UpdateSurvey(ctx, func(s *domain.SurveyState) error {
		s.Responses["r1"] = domain.SurveyResponse{ID: "r1", Nickname: "访客", CreatedAt: time.Now().UTC(), SurveySubmission: domain.SurveySubmission{Version: 1, Analysis: "yes", Answers: map[string]domain.SurveyAnswer{"q1": {Choices: []string{"技术运维"}}}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateSurvey(ctx, func(s *domain.SurveyState) error { delete(s.Responses, "r1"); return errors.New("rollback") }); err == nil {
		t.Fatal("missing rollback")
	}
	state, err := repo.ReadSurvey(ctx)
	if err != nil || len(state.Responses) != 1 {
		t.Fatal("rollback failed", err)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var decoded domain.SurveyState
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Responses["r1"].Answers["q1"].Choices[0] != "技术运维" {
		t.Fatal("roundtrip failed")
	}
	delete(state.Responses, "r1")
	state, _ = repo.ReadSurvey(ctx)
	if len(state.Responses) != 1 {
		t.Fatal("snapshot aliases repository")
	}
	core, err := encodeMemory(repo)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(core, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["survey"]; ok {
		t.Fatal("survey must stay separate from the core aggregate")
	}
}

func TestSurveyLegacyEmptyStateCanBeUpdated(t *testing.T) {
	var state domain.SurveyState
	if err := json.Unmarshal([]byte(`{}`), &state); err != nil {
		t.Fatal(err)
	}
	initializeSurveyMaps(&state)
	state.Guests["guest"] = domain.SurveyGuest{Nickname: "访客"}
	state.Responses["response"] = domain.SurveyResponse{ID: "response"}
	state.Challenges["challenge"] = domain.SurveyChallenge{}
	state.Rates["source"] = domain.SurveyRate{}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var restored domain.SurveyState
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Guests["guest"].Nickname != "访客" || restored.Responses["response"].ID != "response" || len(restored.Challenges) != 1 || len(restored.Rates) != 1 {
		t.Fatal("empty legacy state did not retain a survey update")
	}
}

package control

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

func TestSurveyDraftFastPathRespectsClosedSwitch(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("CONTROL_TEST_DATABASE_URL is not set")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || !strings.HasSuffix(parsed.Path, "_test") {
		t.Fatal("fast draft test requires a disposable *_test database")
	}
	repo, err := store.OpenPostgres(databaseURL, 4, 4, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	id := "survey-draft-test-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	ctx := store.WithSurveyID(context.Background(), id)
	if _, err := repo.CreateSurvey(ctx, domain.SurveyMetadata{ID: id, Name: "Isolated draft test"}); err != nil {
		t.Fatal(err)
	}
	cleanupDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = cleanupDB.ExecContext(context.Background(), `DELETE FROM control_survey_state WHERE survey_id=$1`, id)
		_ = cleanupDB.Close()
	})
	token := strings.Repeat("b", 64)
	if err := repo.UpdateSurvey(ctx, func(state *domain.SurveyState) error {
		state.Settings.Open = true
		state.Guests[surveyHash(token)] = domain.SurveyGuest{Nickname: "访客", ExpiresAt: time.Now().UTC().Add(time.Hour)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CutoverSurveyRows(ctx, id); err != nil {
		t.Fatal(err)
	}
	s := NewService(repo)
	draft := domain.SurveySubmission{Version: 1, Answers: map[string]domain.SurveyAnswer{"q1": {Choices: []string{"技术运维"}}}}
	first, err := s.SaveSurveyDraft(ctx, token, draft, 1, 1)
	if err != nil || first == nil {
		t.Fatalf("first draft: %v %v", first, err)
	}
	stale, err := s.SaveSurveyDraft(ctx, token, draft, 1, 1)
	if err != nil || stale == nil || !stale.Equal(*first) {
		t.Fatal("stale revision changed saved draft", err)
	}
	if err := repo.UpdateSurvey(ctx, func(state *domain.SurveyState) error { state.Settings.Open = false; return nil }); err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveSurveyDraft(ctx, token, draft, 2, 2)
	surveyStatus(t, err, 423)
	state, err := repo.ReadSurvey(ctx)
	if err != nil || state.Guests[surveyHash(token)].DraftRevision != 1 {
		t.Fatal("closed survey accepted a draft", err)
	}
	if err := repo.UpdateSurvey(ctx, func(state *domain.SurveyState) error { state.Settings.Open = true; return nil }); err != nil {
		t.Fatal(err)
	}
	second, err := s.SaveSurveyDraft(ctx, token, draft, 2, 2)
	if err != nil || second == nil {
		t.Fatal("reopened survey did not accept draft", err)
	}
}

func TestSurveyRowsSessionRecoveryAndSubmit(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("CONTROL_TEST_DATABASE_URL is not set")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || !strings.HasSuffix(parsed.Path, "_test") {
		t.Fatal("session test requires a disposable *_test database")
	}
	repo, err := store.OpenPostgres(databaseURL, 4, 4, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	id := "survey-session-test-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	ctx := store.WithSurveyID(context.Background(), id)
	if _, err := repo.CreateSurvey(ctx, domain.SurveyMetadata{ID: id, Name: "Isolated session test"}); err != nil {
		t.Fatal(err)
	}
	cleanupDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = cleanupDB.ExecContext(context.Background(), `DELETE FROM control_survey_state WHERE survey_id=$1`, id)
		_ = cleanupDB.Close()
	})
	if err := repo.UpdateSurvey(ctx, func(state *domain.SurveyState) error {
		state.Settings.Open = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CutoverSurveyRows(ctx, id); err != nil {
		t.Fatal(err)
	}
	s := NewService(repo)
	challenge := func(seed, nickname, recovery string) SurveySessionInput {
		t.Helper()
		challengeID := strings.Repeat(seed, 64)
		if err := repo.UpdateSurvey(ctx, func(state *domain.SurveyState) error {
			state.Challenges[challengeID] = domain.SurveyChallenge{
				AnswerHash: surveyHash(challengeID + ":123456"), ExpiresAt: time.Now().UTC().Add(time.Minute),
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return SurveySessionInput{Nickname: nickname, ChallengeID: challengeID, Code: "123456", SaveDraft: true, RecoveryCode: recovery}
	}
	first, err := s.StartSurveySession(ctx, "test-source", challenge("1", "Alice", ""))
	if err != nil || len(first.RecoveryCode) != 64 {
		t.Fatalf("first session: %v", err)
	}
	_, err = s.StartSurveySession(ctx, "test-source", challenge("2", "ＡＬＩＣＥ", ""))
	surveyStatus(t, err, 409)
	_, err = s.SaveSurveyDraft(ctx, first.Token, domain.SurveySubmission{Version: 1, Answers: map[string]domain.SurveyAnswer{"q1": {Text: "saved"}}}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := s.StartSurveySession(ctx, "test-source", challenge("3", "ＡＬＩＣＥ", first.RecoveryCode))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.GetSurvey(ctx, first.Token)
	surveyStatus(t, err, 401)
	page, err := s.GetSurvey(ctx, recovered.Token)
	if err != nil || page.Draft == nil || page.DraftRevision != 1 {
		t.Fatal("draft was not recovered", err)
	}
	receipt, err := s.SubmitSurvey(ctx, recovered.Token, surveyInput())
	if err != nil || receipt == "" {
		t.Fatal("submit failed", err)
	}
	retry, err := s.SubmitSurvey(ctx, recovered.Token, surveyInput())
	if err != nil || retry != receipt {
		t.Fatal("submit was not idempotent", err)
	}
	_, err = s.StartSurveySession(ctx, "test-source", challenge("4", "alice", first.RecoveryCode))
	surveyStatus(t, err, 409)
	state, err := repo.ReadSurvey(ctx)
	if err != nil || len(state.Responses) != 1 || state.Guests[surveyHash(recovered.Token)].Draft != nil {
		t.Fatal("submitted state changed", err)
	}
	if err := repo.UpdateSurvey(ctx, func(state *domain.SurveyState) error {
		response := state.Responses[receipt]
		response.CreatedAt = time.Now().UTC().Add(-181 * 24 * time.Hour)
		state.Responses[receipt] = response
		guest := state.Guests[surveyHash(recovered.Token)]
		guest.ExpiresAt = time.Now().UTC().Add(-time.Hour)
		state.Guests[surveyHash(recovered.Token)] = guest
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SweepSurveyResponses(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err = repo.ReadSurvey(ctx)
	if err != nil || len(state.Responses) != 0 || len(state.Guests) != 0 {
		t.Fatal("retention sweep left expired rows", err)
	}
	var claims int
	if err := cleanupDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM control_survey_nickname_claim WHERE survey_id=$1`, id).Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("retention sweep left nickname claims: %d %v", claims, err)
	}
}

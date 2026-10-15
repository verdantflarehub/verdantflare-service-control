package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

func surveyFixture(t *testing.T) (*Service, *store.Memory, string) {
	t.Helper()
	repo := store.NewMemoryBootstrap()
	service := NewService(repo)
	service.now = func() time.Time { return time.Date(2026, 10, 15, 17, 0, 0, 0, time.UTC) }
	token := strings.Repeat("a", 64)
	if err := repo.UpdateSurvey(context.Background(), func(state *domain.SurveyState) error {
		state.Settings.Open = true
		state.Guests[surveyHash(token)] = domain.SurveyGuest{Nickname: "测试访客", ExpiresAt: service.now().Add(time.Hour)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return service, repo, token
}

func TestSurveySwitchDefaultClosedAndRetainsResponses(t *testing.T) {
	ctx := context.Background()
	repo := store.NewMemoryBootstrap()
	s := NewService(repo)
	const admin = "00000000-0000-4000-8000-000000000001"
	stats, err := s.SurveyStatistics(ctx, admin)
	if err != nil || stats.Settings.Open || stats.Settings.Version != 0 {
		t.Fatal("must default closed", err)
	}
	_, err = s.GetSurvey(ctx, "")
	surveyStatus(t, err, 423)
	_, err = s.CreateSurveyCaptcha(ctx, "source")
	surveyStatus(t, err, 423)
	_, err = s.StartSurveySession(ctx, "source", SurveySessionInput{Nickname: "测试", ChallengeID: strings.Repeat("c", 64), Code: "123456"})
	surveyStatus(t, err, 423)
	_, err = s.SetSurveySettings(ctx, "unknown", true, 0)
	surveyStatus(t, err, 403)
	settings, err := s.SetSurveySettings(ctx, admin, true, 0)
	if err != nil || !settings.Open || settings.Version != 1 || settings.UpdatedAt == nil || settings.UpdatedBy != admin {
		t.Fatal("open/audit failed", err)
	}
	_, err = s.SetSurveySettings(ctx, admin, false, 0)
	surveyStatus(t, err, 409)
	_, err = s.GetSurvey(ctx, "")
	surveyStatus(t, err, 401)
	token := strings.Repeat("b", 64)
	if err = repo.UpdateSurvey(ctx, func(state *domain.SurveyState) error {
		state.Guests[surveyHash(token)] = domain.SurveyGuest{Nickname: "测试访客", ExpiresAt: time.Now().Add(time.Hour)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err = s.SetSurveySettings(ctx, admin, false, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SubmitSurvey(ctx, token, surveyInput())
	surveyStatus(t, err, 423)
	state, _ := repo.ReadSurvey(ctx)
	if len(state.Responses) != 0 {
		t.Fatal("closed survey accepted a response")
	}
	_, err = s.SetSurveySettings(ctx, admin, true, 2)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.SubmitSurvey(ctx, token, surveyInput())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SetSurveySettings(ctx, admin, false, 3)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.SubmitSurvey(ctx, token, surveyInput())
	if err != nil || retry != id {
		t.Fatal("closed retry must recover same receipt", err)
	}
	page, err := s.GetSurvey(ctx, token)
	if err != nil || page.ResponseID != id || len(page.Questions) != 0 {
		t.Fatal("receipt recovery", err)
	}
	stats, err = s.SurveyStatistics(ctx, admin)
	if err != nil || stats.Total != 1 || stats.Settings.Open {
		t.Fatal("closed statistics", err)
	}
	if _, err = s.GetSurveyResponse(ctx, admin, id); err != nil {
		t.Fatal("closed details", err)
	}
	state, err = repo.ReadSurvey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(state)
	var restored domain.SurveyState
	if err = json.Unmarshal(raw, &restored); err != nil || restored.Settings.Version != 4 || restored.Settings.Open || restored.Settings.UpdatedBy != admin || restored.Settings.UpdatedAt == nil {
		t.Fatal("settings persistence", err)
	}
}

func TestSurveySwitchConcurrentAdministrators(t *testing.T) {
	s := NewService(store.NewMemoryBootstrap())
	ctx := context.Background()
	const admin = "00000000-0000-4000-8000-000000000001"
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.SetSurveySettings(ctx, admin, true, 0); outcomes <- err }()
	}
	wg.Wait()
	close(outcomes)
	success := 0
	for err := range outcomes {
		if err == nil {
			success++
		} else {
			surveyStatus(t, err, 409)
		}
	}
	if success != 1 {
		t.Fatalf("successful updates=%d want 1", success)
	}
}
func surveyInput() domain.SurveySubmission {
	return domain.SurveySubmission{Version: 1, Analysis: "yes", Quote: "no", ContactPermission: "no", Answers: map[string]domain.SurveyAnswer{"q1": {Choices: []string{"一线制作", "技术运维"}}, "q7": {Rows: map[string]string{"模型版本": "不稳定"}}, "q12": {Choices: []string{"版本找回", "批量执行"}}, "q15": {Choices: []string{"需要内部确认"}}}}
}
func surveyStatus(t *testing.T, err error, want int) {
	t.Helper()
	var e *domain.Error
	if !errors.As(err, &e) || e.Status != want {
		t.Fatalf("got %v, want status %d", err, want)
	}
}

func TestSurveyConcurrentSubmissionAndStatistics(t *testing.T) {
	s, repo, token := surveyFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := s.SubmitSurvey(ctx, token, surveyInput())
			if err != nil {
				t.Error(err)
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Error("duplicate receipt")
		}
	}
	state, err := repo.ReadSurvey(ctx)
	if err != nil || len(state.Responses) != 1 {
		t.Fatalf("responses=%d err=%v", len(state.Responses), err)
	}
	page, err := s.GetSurvey(ctx, token)
	if err != nil || page.ResponseID != first {
		t.Fatal("receipt recovery failed", err)
	}
	const admin = "00000000-0000-4000-8000-000000000001"
	stats, err := s.SurveyStatistics(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 1 || stats.Daily["2026-10-16"] != 1 || stats.Distribution["q1"].Answered != 1 || stats.Distribution["q1"].Counts["技术运维"] != 1 || stats.Distribution["q7:模型版本"].Counts["不稳定"] != 1 {
		t.Fatalf("wrong counts: %+v", stats)
	}
	list, err := s.ListSurveyResponses(ctx, admin, 0, 20)
	if err != nil || len(list.Items) != 1 || list.Total != 1 {
		t.Fatal("list", err)
	}
	list, err = s.ListSurveyResponses(ctx, admin, 1000, 20)
	if err != nil || len(list.Items) != 0 {
		t.Fatal("pagination", err)
	}
	response, err := s.GetSurveyResponse(ctx, admin, first)
	if err != nil || response.Analysis != "yes" {
		t.Fatal("detail", err)
	}
	_, err = s.GetSurveyResponse(ctx, admin, "missing")
	surveyStatus(t, err, 404)
}

func TestSurveyValidation(t *testing.T) {
	cases := map[string]func(*domain.SurveySubmission){
		"version":          func(v *domain.SurveySubmission) { v.Version = 2 },
		"analysis":         func(v *domain.SurveySubmission) { v.Analysis = "no" },
		"quote":            func(v *domain.SurveySubmission) { v.Quote = "" },
		"contact":          func(v *domain.SurveySubmission) { v.Contact = "secret" },
		"role":             func(v *domain.SurveySubmission) { delete(v.Answers, "q1") },
		"unknown question": func(v *domain.SurveySubmission) { v.Answers["q99"] = domain.SurveyAnswer{} },
		"unknown option":   func(v *domain.SurveySubmission) { v.Answers["q1"] = domain.SurveyAnswer{Choices: []string{"invalid"}} },
		"duplicates": func(v *domain.SurveySubmission) {
			v.Answers["q1"] = domain.SurveyAnswer{Choices: []string{"技术运维", "技术运维"}}
		},
		"max": func(v *domain.SurveySubmission) {
			v.Answers["q12"] = domain.SurveyAnswer{Choices: []string{"版本找回", "批量执行", "生成质量"}}
		},
		"exclusive": func(v *domain.SurveySubmission) {
			v.Answers["q12"] = domain.SurveyAnswer{Choices: []string{"版本找回", "暂无足够痛点"}}
		},
		"text length": func(v *domain.SurveySubmission) {
			v.Answers["q4"] = domain.SurveyAnswer{Text: strings.Repeat("字", 2001)}
		},
		"unknown row": func(v *domain.SurveySubmission) {
			v.Answers["q7"] = domain.SurveyAnswer{Rows: map[string]string{"secret": "不稳定"}}
		},
		"matrix choices": func(v *domain.SurveySubmission) {
			v.Answers["q7"] = domain.SurveyAnswer{Choices: []string{"不稳定"}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) { v := surveyInput(); mutate(&v); surveyStatus(t, validateSurvey(v), 400) })
	}
	if err := validateSurvey(surveyInput()); err != nil {
		t.Fatal(err)
	}
}

func TestSurveyCAPTCHAExpiryReplayAndLimits(t *testing.T) {
	s, repo, _ := surveyFixture(t)
	ctx := context.Background()
	captcha, err := s.CreateSurveyCaptcha(ctx, "source")
	if err != nil {
		t.Fatal(err)
	}
	png, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(captcha.ImageDataURL, "data:image/png;base64,"))
	if err != nil || string(png[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatal("not a PNG")
	}
	raw, _ := json.Marshal(captcha)
	if strings.Contains(string(raw), "answerHash") {
		t.Fatal("answer leaked")
	}
	seed := func(expiry time.Time) {
		t.Helper()
		if err := repo.UpdateSurvey(ctx, func(state *domain.SurveyState) error {
			state.Challenges[captcha.ChallengeID] = domain.SurveyChallenge{AnswerHash: surveyHash(captcha.ChallengeID + ":123456"), ExpiresAt: expiry}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	input := SurveySessionInput{Nickname: "访客", ChallengeID: captcha.ChallengeID, Code: "654321"}
	seed(s.now().Add(time.Minute))
	_, err = s.StartSurveySession(ctx, "source", input)
	surveyStatus(t, err, 400)
	input.Code = "123456"
	_, err = s.StartSurveySession(ctx, "source", input)
	surveyStatus(t, err, 400)
	seed(s.now().Add(-time.Second))
	_, err = s.StartSurveySession(ctx, "source", input)
	surveyStatus(t, err, 400)
	seed(s.now().Add(time.Minute))
	token, err := s.StartSurveySession(ctx, "source", input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.StartSurveySession(ctx, "source", input)
	surveyStatus(t, err, 400)
	state, _ := repo.ReadSurvey(ctx)
	if _, ok := state.Guests[token]; ok {
		t.Fatal("raw token persisted")
	}
	if _, err = s.GetSurvey(ctx, token); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC) }
	_, err = s.GetSurvey(ctx, token)
	surveyStatus(t, err, 401)
	if err = repo.UpdateSurvey(ctx, func(state *domain.SurveyState) error {
		state.Rates[surveyHash("limited")] = domain.SurveyRate{Count: 120, ExpiresAt: s.now().Add(time.Minute)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateSurveyCaptcha(ctx, "limited")
	surveyStatus(t, err, 429)
}

func TestSurveyAdminOrganizationAndRoleBoundary(t *testing.T) {
	s, _, _ := surveyFixture(t)
	ctx := context.Background()
	_, err := s.SurveyStatistics(ctx, "unknown")
	surveyStatus(t, err, 403)
	// Seeded fixture has a second organization; switching must revoke survey access.
	repo := store.NewMemorySeeded(s.now())
	s = NewService(repo)
	const admin = "00000000-0000-4000-8000-000000000001"
	if _, err = repo.SetActiveOrganization(ctx, admin, "org_northshore"); err != nil {
		t.Fatal(err)
	}
	_, err = s.SurveyStatistics(ctx, admin)
	surveyStatus(t, err, 403)
	_, err = s.ListSurveyResponses(ctx, admin, 0, 20)
	surveyStatus(t, err, 403)
	_, err = s.GetSurveyResponse(ctx, admin, "id")
	surveyStatus(t, err, 403)
}

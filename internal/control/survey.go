package control

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math/big"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

func surveyHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func surveyRandom() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func (s *Service) surveyRepository() (store.SurveyRepository, error) {
	repo, ok := s.repository.(store.SurveyRepository)
	if !ok {
		return nil, domain.NewError(503, "survey_unavailable", "问卷暂时不可用")
	}
	return repo, nil
}

var surveyRequestID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const surveyResponseRetention = 180 * 24 * time.Hour

func (s *Service) requireSurveyAdmin(ctx context.Context, subject string) error {
	org, roles, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return err
	}
	if org.OrganizationID != "org_verdantflare" || !hasAnyRole(roles, "customer_success_admin") {
		return domain.NewError(403, "survey_forbidden", "仅 VF 内部客户成功管理员可管理问卷")
	}
	return nil
}
func surveyMeta(id string, state domain.SurveyState) domain.SurveyMetadata {
	if state.Metadata != nil {
		return *state.Metadata
	}
	return domain.SurveyMetadata{ID: id, Name: "云南交流", Description: "需求反馈"}
}

type SurveyCatalogItem struct {
	domain.SurveyMetadata
	Open  bool `json:"open"`
	Total int  `json:"total"`
}

func (s *Service) ListSurveys(ctx context.Context, subject string) ([]SurveyCatalogItem, error) {
	if err := s.requireSurveyAdmin(ctx, subject); err != nil {
		return nil, err
	}
	repo, err := s.surveyRepository()
	if err != nil {
		return nil, err
	}
	states, err := repo.ListSurveys(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]SurveyCatalogItem, 0, len(states))
	for _, state := range states {
		result = append(result, SurveyCatalogItem{SurveyMetadata: *state.Metadata, Open: state.Settings.Open, Total: len(state.Responses)})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result, nil
}
func (s *Service) CreateSurvey(ctx context.Context, subject, name, description, requestID string) (domain.SurveyMetadata, error) {
	if err := s.requireSurveyAdmin(ctx, subject); err != nil {
		return domain.SurveyMetadata{}, err
	}
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if n := utf8.RuneCountInString(name); n < 2 || n > 80 {
		return domain.SurveyMetadata{}, domain.NewError(400, "survey_name_invalid", "问卷名称需要 2—80 个字符")
	}
	if utf8.RuneCountInString(description) > 200 {
		return domain.SurveyMetadata{}, domain.NewError(400, "survey_description_invalid", "说明最多 200 个字符")
	}
	if !surveyRequestID.MatchString(requestID) {
		return domain.SurveyMetadata{}, domain.NewError(400, "survey_request_invalid", "创建请求标识无效")
	}
	repo, err := s.surveyRepository()
	if err != nil {
		return domain.SurveyMetadata{}, err
	}
	meta := domain.SurveyMetadata{ID: "survey-" + strings.ReplaceAll(requestID, "-", ""), Name: name, Description: description, CreatedAt: s.now(), CreatedBy: subject}
	return repo.CreateSurvey(ctx, meta)
}
func (s *Service) SurveyMetadata(ctx context.Context) (domain.SurveyMetadata, error) {
	repo, err := s.surveyRepository()
	if err != nil {
		return domain.SurveyMetadata{}, err
	}
	state, err := repo.ReadSurvey(ctx)
	if err != nil {
		return domain.SurveyMetadata{}, err
	}
	meta := surveyMeta(store.SurveyID(ctx), state)
	meta.CreatedBy = ""
	return meta, nil
}
func surveyCleanup(state *domain.SurveyState, now time.Time) {
	for key, v := range state.Challenges {
		if !now.Before(v.ExpiresAt) {
			delete(state.Challenges, key)
		}
	}
	for key, v := range state.Guests {
		if !now.Before(v.ExpiresAt) {
			delete(state.Guests, key)
		}
	}
	for key, v := range state.Rates {
		if !now.Before(v.ExpiresAt) {
			delete(state.Rates, key)
		}
	}
	for key, response := range state.Responses {
		if !now.Before(response.CreatedAt.Add(surveyResponseRetention)) {
			delete(state.Responses, key)
		}
	}
}

// SweepSurveyResponses removes expired survey data from storage, including on
// otherwise inactive surveys. Every update rechecks expiry while holding the
// repository's survey lock so a concurrent submission cannot be lost.
func (s *Service) SweepSurveyResponses(ctx context.Context) error {
	repo, err := s.surveyRepository()
	if err != nil {
		return err
	}
	states, err := repo.ListSurveys(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	for _, state := range states {
		expired := false
		for _, response := range state.Responses {
			if !now.Before(response.CreatedAt.Add(surveyResponseRetention)) {
				expired = true
				break
			}
		}
		if !expired {
			continue
		}
		if err := repo.UpdateSurvey(store.WithSurveyID(ctx, state.Metadata.ID), func(current *domain.SurveyState) error {
			surveyCleanup(current, s.now())
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
func surveyRate(state *domain.SurveyState, source string, now time.Time) error {
	surveyCleanup(state, now)
	key := surveyHash(source)
	rate := state.Rates[key]
	if rate.Count >= 120 || len(state.Challenges) >= 2000 || len(state.Guests) >= 2000 || (rate.Count == 0 && len(state.Rates) >= 2000) {
		return domain.NewError(429, "survey_rate_limited", "操作较频繁，请十分钟后再试")
	}
	if rate.Count == 0 {
		rate.ExpiresAt = now.Add(10 * time.Minute)
	}
	rate.Count++
	state.Rates[key] = rate
	return nil
}

func requireSurveyOpen(state domain.SurveyState) error {
	if !state.Settings.Open {
		return domain.NewError(423, "survey_closed", "问卷暂未开放或已暂停，当前不能提交；页面内的答案会保留")
	}
	return nil
}

func (s *Service) SetSurveySettings(ctx context.Context, subject string, open bool, expectedVersion int) (domain.SurveySettings, error) {
	if _, err := s.surveyAdminState(ctx, subject); err != nil {
		return domain.SurveySettings{}, err
	}
	if expectedVersion < 0 {
		return domain.SurveySettings{}, domain.NewError(400, "survey_settings_invalid", "设置版本无效")
	}
	repo, err := s.surveyRepository()
	if err != nil {
		return domain.SurveySettings{}, err
	}
	var result domain.SurveySettings
	err = repo.UpdateSurvey(ctx, func(state *domain.SurveyState) error {
		if state.Settings.Version != expectedVersion {
			return domain.NewError(409, "survey_settings_conflict", "开放状态已被修改，请刷新后再操作")
		}
		if state.Settings.Open != open {
			now := s.now()
			state.Settings = domain.SurveySettings{Open: open, Version: state.Settings.Version + 1, UpdatedAt: &now, UpdatedBy: subject}
		}
		result = state.Settings
		return nil
	})
	return result, err
}

type SurveyCaptcha struct {
	ChallengeID  string    `json:"challengeId"`
	ImageDataURL string    `json:"imageDataUrl"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

func (s *Service) CreateSurveyCaptcha(ctx context.Context, source string) (SurveyCaptcha, error) {
	repo, err := s.surveyRepository()
	if err != nil {
		return SurveyCaptcha{}, err
	}
	id, err := surveyRandom()
	if err != nil {
		return SurveyCaptcha{}, err
	}
	answer := make([]byte, 6)
	for i := range answer {
		n, e := rand.Int(rand.Reader, big.NewInt(10))
		if e != nil {
			return SurveyCaptcha{}, e
		}
		answer[i] = byte(n.Int64()) + '0'
	}
	picture, err := surveyCaptchaPNG(string(answer))
	if err != nil {
		return SurveyCaptcha{}, err
	}
	expires := s.now().Add(5 * time.Minute)
	err = repo.UpdateSurvey(ctx, func(state *domain.SurveyState) error {
		if err := requireSurveyOpen(*state); err != nil {
			return err
		}
		if err := surveyRate(state, source, s.now()); err != nil {
			return err
		}
		state.Challenges[id] = domain.SurveyChallenge{AnswerHash: surveyHash(id + ":" + string(answer)), ExpiresAt: expires}
		return nil
	})
	return SurveyCaptcha{id, "data:image/png;base64," + base64.StdEncoding.EncodeToString(picture), expires}, err
}

// Raster-only digits: the answer is never present in image metadata or API JSON.
func surveyCaptchaPNG(answer string) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, 240, 70))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{244, 241, 232, 255}), image.Point{}, draw.Src)
	noise := make([]byte, 800)
	if _, err := rand.Read(noise); err != nil {
		return nil, err
	}
	for i := 0; i < 600; i += 2 {
		img.Set(int(noise[i])%240, int(noise[i+1])%70, color.RGBA{130, 155, 140, 255})
	}
	segments := []byte{0x3f, 0x06, 0x5b, 0x4f, 0x66, 0x6d, 0x7d, 0x07, 0x7f, 0x6f}
	positions := [][4]int{{3, 0, 21, 4}, {20, 3, 24, 23}, {20, 25, 24, 45}, {3, 44, 21, 48}, {0, 25, 4, 45}, {0, 3, 4, 23}, {3, 22, 21, 26}}
	for i, digit := range []byte(answer) {
		x, y := 12+i*38, 8+int(noise[600+i])%9
		for j, p := range positions {
			if segments[digit-'0']&(1<<j) != 0 {
				draw.Draw(img, image.Rect(x+p[0], y+p[1], x+p[2], y+p[3]), image.NewUniform(color.RGBA{20, 55, 35, 255}), image.Point{}, draw.Src)
			}
		}
	}
	var out bytes.Buffer
	err := png.Encode(&out, img)
	return out.Bytes(), err
}

type SurveySessionInput struct {
	Nickname     string `json:"nickname"`
	ChallengeID  string `json:"challengeId"`
	Code         string `json:"code"`
	SaveDraft    bool   `json:"saveDraft"`
	RecoveryCode string `json:"recoveryCode,omitempty"`
}

type SurveySessionCredentials struct {
	Token        string
	RecoveryCode string
}

func surveyNicknameKey(value string) string {
	return cases.Fold().String(norm.NFKC.String(strings.TrimSpace(value)))
}

func (s *Service) StartSurveySession(ctx context.Context, source string, input SurveySessionInput) (SurveySessionCredentials, error) {
	input.Nickname = strings.TrimSpace(input.Nickname)
	if n := utf8.RuneCountInString(input.Nickname); n < 2 || n > 32 {
		return SurveySessionCredentials{}, domain.NewError(400, "survey_nickname_invalid", "昵称需要 2—32 个字符")
	}
	if !input.SaveDraft {
		return SurveySessionCredentials{}, domain.NewError(400, "survey_draft_consent_required", "请确认临时保存草稿以便续填")
	}
	if len(input.ChallengeID) != 64 || len(input.Code) != 6 {
		return SurveySessionCredentials{}, domain.NewError(400, "survey_captcha_invalid", "请输入完整验证码，并在失效后换一张")
	}
	repo, err := s.surveyRepository()
	if err != nil {
		return SurveySessionCredentials{}, err
	}
	token, err := surveyRandom()
	if err != nil {
		return SurveySessionCredentials{}, err
	}
	recoveryCode := ""
	if input.RecoveryCode == "" {
		recoveryCode, err = surveyRandom()
		if err != nil {
			return SurveySessionCredentials{}, err
		}
	} else if len(input.RecoveryCode) != 64 {
		return SurveySessionCredentials{}, domain.NewError(400, "survey_recovery_invalid", "续填码无效或已过期")
	}
	var invalid error
	err = repo.UpdateSurvey(ctx, func(state *domain.SurveyState) error {
		if err := requireSurveyOpen(*state); err != nil {
			return err
		}
		if err := surveyRate(state, source, s.now()); err != nil {
			return err
		}
		challenge, exists := state.Challenges[input.ChallengeID]
		delete(state.Challenges, input.ChallengeID) // Wrong attempts also consume the challenge.
		if !exists || !s.now().Before(challenge.ExpiresAt) || subtle.ConstantTimeCompare([]byte(challenge.AnswerHash), []byte(surveyHash(input.ChallengeID+":"+input.Code))) != 1 {
			invalid = domain.NewError(400, "survey_captcha_invalid", "验证码错误或已过期，请换一张重试")
			return nil
		}
		key := surveyNicknameKey(input.Nickname)
		for _, response := range state.Responses {
			if surveyNicknameKey(response.Nickname) == key {
				invalid = domain.NewError(409, "survey_nickname_taken", "该昵称已被使用，请换一个昵称")
				return nil
			}
		}
		for oldToken, guest := range state.Guests {
			if surveyNicknameKey(guest.Nickname) != key || !s.now().Before(guest.ExpiresAt) {
				continue
			}
			if input.RecoveryCode == "" {
				invalid = domain.NewError(409, "survey_nickname_taken", "该昵称已被使用；若是您本人，请输入续填码")
				return nil
			}
			if subtle.ConstantTimeCompare([]byte(guest.RecoveryHash), []byte(surveyHash(input.RecoveryCode))) != 1 {
				invalid = domain.NewError(400, "survey_recovery_invalid", "续填码无效或已过期")
				return nil
			}
			delete(state.Guests, oldToken)
			state.Guests[surveyHash(token)] = guest
			return nil
		}
		if input.RecoveryCode != "" {
			invalid = domain.NewError(400, "survey_recovery_invalid", "续填码无效或已过期")
			return nil
		}
		state.Guests[surveyHash(token)] = domain.SurveyGuest{Nickname: input.Nickname, ExpiresAt: s.now().Add(7 * 24 * time.Hour), RecoveryHash: surveyHash(recoveryCode), DraftConsentAt: s.now()}
		return nil
	})
	if err != nil {
		return SurveySessionCredentials{}, err
	}
	if invalid != nil {
		return SurveySessionCredentials{}, invalid
	}
	return SurveySessionCredentials{Token: token, RecoveryCode: recoveryCode}, nil
}

func surveyGuest(state domain.SurveyState, token string, now time.Time) (domain.SurveyGuest, error) {
	guest, ok := state.Guests[surveyHash(token)]
	if !ok || len(token) != 64 || !now.Before(guest.ExpiresAt) {
		return guest, domain.NewError(401, "survey_session_required", "填写会话已过期，请重新输入昵称和验证码；页面中的答案会保留")
	}
	return guest, nil
}

type SurveyPage struct {
	ID            string                   `json:"id"`
	Version       int                      `json:"version"`
	Nickname      string                   `json:"nickname"`
	ResponseID    string                   `json:"responseId"`
	Questions     []domain.SurveyQuestion  `json:"questions"`
	Draft         *domain.SurveySubmission `json:"draft,omitempty"`
	DraftSavedAt  *time.Time               `json:"draftSavedAt,omitempty"`
	DraftStep     int                      `json:"draftStep,omitempty"`
	DraftRevision int64                    `json:"draftRevision,omitempty"`
	ExpiresAt     time.Time                `json:"expiresAt"`
	RecoveryCode  string                   `json:"recoveryCode,omitempty"`
}

func (s *Service) GetSurvey(ctx context.Context, token string) (SurveyPage, error) {
	repo, err := s.surveyRepository()
	if err != nil {
		return SurveyPage{}, err
	}
	state, err := repo.ReadSurvey(ctx)
	if err != nil {
		return SurveyPage{}, err
	}
	guest, err := surveyGuest(state, token, s.now())
	if err == nil && guest.ResponseID != "" {
		return SurveyPage{ID: store.SurveyID(ctx), Version: 1, Nickname: guest.Nickname, ResponseID: guest.ResponseID, Questions: []domain.SurveyQuestion{}, ExpiresAt: guest.ExpiresAt}, nil
	}
	if closed := requireSurveyOpen(state); closed != nil {
		return SurveyPage{}, closed
	}
	if err != nil {
		return SurveyPage{}, err
	}
	return SurveyPage{ID: store.SurveyID(ctx), Version: 1, Nickname: guest.Nickname, ResponseID: guest.ResponseID, Questions: domain.SurveyQuestions(), Draft: guest.Draft, DraftSavedAt: guest.DraftSavedAt, DraftStep: guest.DraftStep, DraftRevision: guest.DraftRevision, ExpiresAt: guest.ExpiresAt}, nil
}
func validateSurveyFields(input domain.SurveySubmission) error {
	invalid := func(message string) error { return domain.NewError(400, "survey_answers_invalid", message) }
	if input.Version != 1 {
		return invalid("题目版本已变化，请刷新页面后重填")
	}
	for _, value := range []string{input.Analysis, input.Quote, input.ContactPermission} {
		if value != "" && value != "yes" && value != "no" {
			return invalid("使用许可选项无效")
		}
	}
	if utf8.RuneCountInString(input.Contact) > 200 || (input.ContactPermission != "yes" && strings.TrimSpace(input.Contact) != "") {
		return invalid("联系方式最多 200 字，且须同意会后联系")
	}
	questions := map[string]domain.SurveyQuestion{}
	for _, q := range domain.SurveyQuestions() {
		if q.ID != "q16" {
			questions[q.ID] = q
		}
	}
	for id, answer := range input.Answers {
		q, ok := questions[id]
		if !ok {
			return invalid("含未知题目")
		}
		if utf8.RuneCountInString(answer.Text) > 2000 {
			return invalid(id + " 补充说明最多 2000 字")
		}
		if len(answer.Rows) > len(q.Rows) || (len(q.Rows) > 0 && len(answer.Choices) > 0) {
			return invalid(id + " 回答格式有误")
		}
		for row, value := range answer.Rows {
			if !slices.Contains(q.Rows, row) || !slices.Contains(q.Options, value) {
				return invalid(id + " 含未知行或选项")
			}
		}
		seen := map[string]bool{}
		for _, value := range answer.Choices {
			if !slices.Contains(q.Options, value) || seen[value] {
				return invalid(id + " 选项无效或重复")
			}
			seen[value] = true
		}
		if q.Max > 0 && len(answer.Choices) > q.Max {
			return invalid(id + " 选择超过上限")
		}
		if id == "q12" && seen["暂无足够痛点"] && len(seen) > 1 {
			return invalid("暂无足够痛点不能与其他优先项同时选择")
		}
	}
	return nil
}
func validateSurvey(input domain.SurveySubmission) error {
	if err := validateSurveyFields(input); err != nil {
		return err
	}
	if input.Analysis != "yes" {
		return domain.NewError(400, "survey_answers_invalid", "不同意内部需求分析时不保存本次答卷")
	}
	if input.Quote == "" || input.ContactPermission == "" {
		return domain.NewError(400, "survey_answers_invalid", "请分别选择三项使用许可")
	}
	if len(input.Answers["q1"].Choices) == 0 {
		return domain.NewError(400, "survey_answers_invalid", "请选择您的角色")
	}
	return nil
}

func (s *Service) SaveSurveyDraft(ctx context.Context, token string, input domain.SurveySubmission, step int, revision int64) (*time.Time, error) {
	if err := validateSurveyFields(input); err != nil {
		return nil, err
	}
	if step < 0 || step > 3 || revision < 1 {
		return nil, domain.NewError(400, "survey_draft_invalid", "草稿进度无效")
	}
	repo, err := s.surveyRepository()
	if err != nil {
		return nil, err
	}
	var savedAt *time.Time
	err = repo.UpdateSurvey(ctx, func(state *domain.SurveyState) error {
		if err := requireSurveyOpen(*state); err != nil {
			return err
		}
		guest, err := surveyGuest(*state, token, s.now())
		if err != nil {
			return err
		}
		if guest.ResponseID != "" {
			return domain.NewError(409, "survey_already_submitted", "问卷已提交，不能再保存草稿")
		}
		if revision <= guest.DraftRevision {
			savedAt = guest.DraftSavedAt
			return nil
		}
		guest.DraftRevision = revision
		if input.Analysis == "no" {
			guest.Draft, guest.DraftSavedAt, guest.DraftStep = nil, nil, 0
		} else {
			now := s.now()
			guest.Draft, guest.DraftSavedAt = &input, &now
			guest.DraftStep = step
			savedAt = &now
		}
		state.Guests[surveyHash(token)] = guest
		return nil
	})
	return savedAt, err
}
func (s *Service) SubmitSurvey(ctx context.Context, token string, input domain.SurveySubmission) (string, error) {
	repo, err := s.surveyRepository()
	if err != nil {
		return "", err
	}
	id, err := surveyRandom()
	if err != nil {
		return "", err
	}
	err = repo.UpdateSurvey(ctx, func(state *domain.SurveyState) error {
		guest, err := surveyGuest(*state, token, s.now())
		if err != nil {
			return err
		}
		if guest.ResponseID != "" {
			id = guest.ResponseID
			return nil
		}
		if err := requireSurveyOpen(*state); err != nil {
			return err
		}
		if err := validateSurvey(input); err != nil {
			return err
		}
		// Bound this small event survey's aggregate storage, including public abuse.
		if len(state.Responses) >= 1000 {
			return domain.NewError(503, "survey_capacity", "本次问卷收集已达上限，请联系组织方")
		}
		state.Responses[id] = domain.SurveyResponse{ID: id, Nickname: guest.Nickname, CreatedAt: s.now(), SurveySubmission: input}
		guest.ResponseID = id
		guest.Draft, guest.DraftSavedAt, guest.RecoveryHash = nil, nil, ""
		state.Guests[surveyHash(token)] = guest
		return nil
	})
	return id, err
}

func (s *Service) surveyAdminState(ctx context.Context, subject string) (domain.SurveyState, error) {
	if err := s.requireSurveyAdmin(ctx, subject); err != nil {
		return domain.SurveyState{}, err
	}
	repo, err := s.surveyRepository()
	if err != nil {
		return domain.SurveyState{}, err
	}
	return repo.ReadSurvey(ctx)
}

type SurveyDistribution struct {
	Answered int            `json:"answered"`
	Counts   map[string]int `json:"counts"`
}
type SurveyStats struct {
	Metadata       domain.SurveyMetadata         `json:"metadata"`
	Settings       domain.SurveySettings         `json:"settings"`
	Total          int                           `json:"total"`
	Daily          map[string]int                `json:"daily"`
	Questions      []domain.SurveyQuestion       `json:"questions"`
	Distribution   map[string]SurveyDistribution `json:"distribution"`
	ContactAllowed int                           `json:"contactAllowed"`
	QuoteAllowed   int                           `json:"quoteAllowed"`
}

func (s *Service) SurveyStatistics(ctx context.Context, subject string) (SurveyStats, error) {
	state, err := s.surveyAdminState(ctx, subject)
	if err != nil {
		return SurveyStats{}, err
	}
	result := SurveyStats{Metadata: surveyMeta(store.SurveyID(ctx), state), Total: len(state.Responses), Daily: map[string]int{}, Questions: domain.SurveyQuestions(), Distribution: map[string]SurveyDistribution{}}
	result.Settings = state.Settings
	for _, q := range result.Questions {
		if len(q.Options) == 0 {
			continue
		}
		keys := []string{q.ID}
		if len(q.Rows) > 0 {
			keys = nil
			for _, row := range q.Rows {
				keys = append(keys, q.ID+":"+row)
			}
		}
		for _, key := range keys {
			counts := map[string]int{}
			for _, option := range q.Options {
				counts[option] = 0
			}
			result.Distribution[key] = SurveyDistribution{Counts: counts}
		}
	}
	for _, response := range state.Responses {
		result.Daily[response.CreatedAt.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")]++
		if response.ContactPermission == "yes" {
			result.ContactAllowed++
		}
		if response.Quote == "yes" {
			result.QuoteAllowed++
		}
		for id, answer := range response.Answers {
			if len(answer.Choices) > 0 {
				d := result.Distribution[id]
				d.Answered++
				for _, choice := range answer.Choices {
					d.Counts[choice]++
				}
				result.Distribution[id] = d
			}
			for row, value := range answer.Rows {
				key := id + ":" + row
				d := result.Distribution[key]
				d.Answered++
				d.Counts[value]++
				result.Distribution[key] = d
			}
		}
	}
	return result, nil
}

type SurveySummary struct {
	ID        string    `json:"id"`
	Nickname  string    `json:"nickname"`
	CreatedAt time.Time `json:"createdAt"`
}
type SurveyResponseList struct {
	Total int             `json:"total"`
	Items []SurveySummary `json:"items"`
}

func (s *Service) ListSurveyResponses(ctx context.Context, subject string, offset, limit int) (SurveyResponseList, error) {
	state, err := s.surveyAdminState(ctx, subject)
	if err != nil {
		return SurveyResponseList{}, err
	}
	items := make([]SurveySummary, 0, len(state.Responses))
	for _, r := range state.Responses {
		items = append(items, SurveySummary{r.ID, r.Nickname, r.CreatedAt})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	total := len(items)
	offset = max(0, min(offset, total))
	limit = max(1, min(limit, 50))
	return SurveyResponseList{total, items[offset:min(offset+limit, total)]}, nil
}
func (s *Service) GetSurveyResponse(ctx context.Context, subject, id string) (domain.SurveyResponse, error) {
	state, err := s.surveyAdminState(ctx, subject)
	if err != nil {
		return domain.SurveyResponse{}, err
	}
	response, ok := state.Responses[id]
	if !ok {
		return response, domain.NewError(404, "survey_response_not_found", "未找到答卷")
	}
	return response, nil
}

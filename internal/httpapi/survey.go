package httpapi

import (
	"encoding/hex"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

const surveyCookie = "vf_survey_session"

func (s *Server) surveySettings(w http.ResponseWriter, r *http.Request) {
	if !s.surveyRoute(w, r) {
		return
	}
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	if !s.surveyWriteAllowed(w, r) {
		return
	}
	var input struct {
		Open            *bool `json:"open"`
		ExpectedVersion *int  `json:"expectedVersion"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.Open == nil || input.ExpectedVersion == nil {
		s.writeError(w, r, domain.NewError(400, "survey_settings_invalid", "必须提供开放状态与预期版本"))
		return
	}
	result, err := s.service.SetSurveySettings(r.Context(), subject, *input.Open, *input.ExpectedVersion)
	s.respond(w, r, result, err, 200)
}

func (s *Server) surveyRoute(w http.ResponseWriter, r *http.Request) bool {
	id := r.PathValue("surveyID")
	valid := id == domain.SurveyID
	if len(id) == 39 && len(id) > 7 && id[:7] == "survey-" {
		_, err := hex.DecodeString(id[7:])
		valid = err == nil
	}
	if !valid {
		s.writeError(w, r, domain.NewError(404, "survey_not_found", "未找到该问卷"))
		return false
	}
	*r = *r.WithContext(store.WithSurveyID(r.Context(), id))
	return true
}
func surveyCookieName(r *http.Request) string {
	if r.PathValue("surveyID") == domain.SurveyID {
		return surveyCookie
	}
	return surveyCookie + "_" + r.PathValue("surveyID")
}
func (s *Server) surveyMetadata(w http.ResponseWriter, r *http.Request) {
	if !s.surveyRoute(w, r) {
		return
	}
	result, err := s.service.SurveyMetadata(r.Context())
	s.respond(w, r, result, err, 200)
}
func (s *Server) surveyList(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	result, err := s.service.ListSurveys(r.Context(), subject)
	s.respond(w, r, result, err, 200)
}
func (s *Server) surveyCreate(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok || !s.surveyWriteAllowed(w, r) {
		return
	}
	var input struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		RequestID   string `json:"requestId"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	result, err := s.service.CreateSurvey(r.Context(), subject, input.Name, input.Description, input.RequestID)
	s.respond(w, r, result, err, 201)
}
func (s *Server) surveyWriteAllowed(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	u, err := url.Parse(origin)
	valid := err == nil && u.Host != "" && (u.Scheme == "https" || (s.config.Environment != "production" && u.Scheme == "http")) && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && (u.Host == r.Host || slices.Contains(s.config.AllowedOrigins, origin))
	media, _, parseErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if !valid || parseErr != nil || media != "application/json" {
		s.writeError(w, r, domain.NewError(403, "survey_origin_forbidden", "请从问卷页面提交 JSON 请求"))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	return true
}
func (s *Server) surveySource(r *http.Request) string {
	// Only the trusted Hub proxy may supply this header; its config overwrites it.
	if s.config.TrustAuthHeaders && r.Header.Get("X-Real-IP") != "" {
		return r.Header.Get("X-Real-IP")
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
func surveyToken(r *http.Request) string {
	c, err := r.Cookie(surveyCookieName(r))
	if err != nil {
		return ""
	}
	return c.Value
}
func (s *Server) surveyCaptcha(w http.ResponseWriter, r *http.Request) {
	if !s.surveyRoute(w, r) {
		return
	}
	result, err := s.service.CreateSurveyCaptcha(r.Context(), s.surveySource(r))
	s.respond(w, r, result, err, 200)
}
func (s *Server) surveySession(w http.ResponseWriter, r *http.Request) {
	if !s.surveyRoute(w, r) || !s.surveyWriteAllowed(w, r) {
		return
	}
	var input control.SurveySessionInput
	if !s.decode(w, r, &input) {
		return
	}
	// A successful session/receipt is reused so a lost response can be retried safely.
	if page, err := s.service.GetSurvey(r.Context(), surveyToken(r)); err == nil && input.RecoveryCode == "" && page.Nickname == input.Nickname {
		writeJSON(w, 200, page)
		return
	}
	credentials, err := s.service.StartSurveySession(r.Context(), s.surveySource(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	cookiePath := "/api/control/public/surveys/"
	if r.PathValue("surveyID") != domain.SurveyID {
		cookiePath += r.PathValue("surveyID")
	}
	http.SetCookie(w, &http.Cookie{Name: surveyCookieName(r), Value: credentials.Token, Path: cookiePath, MaxAge: 7 * 24 * 3600, HttpOnly: true, Secure: s.config.Environment == "production", SameSite: http.SameSiteStrictMode})
	page, err := s.service.GetSurvey(r.Context(), credentials.Token)
	page.RecoveryCode = credentials.RecoveryCode
	s.respond(w, r, page, err, 200)
}
func (s *Server) surveyDraft(w http.ResponseWriter, r *http.Request) {
	if !s.surveyRoute(w, r) || !s.surveyWriteAllowed(w, r) {
		return
	}
	var input struct {
		domain.SurveySubmission
		Step     int   `json:"step"`
		Revision int64 `json:"revision"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	savedAt, err := s.service.SaveSurveyDraft(r.Context(), surveyToken(r), input.SurveySubmission, input.Step, input.Revision)
	s.respond(w, r, map[string]any{"savedAt": savedAt}, err, 200)
}
func (s *Server) surveyPage(w http.ResponseWriter, r *http.Request) {
	if !s.surveyRoute(w, r) {
		return
	}
	result, err := s.service.GetSurvey(r.Context(), surveyToken(r))
	s.respond(w, r, result, err, 200)
}
func (s *Server) surveySubmit(w http.ResponseWriter, r *http.Request) {
	if !s.surveyRoute(w, r) || !s.surveyWriteAllowed(w, r) {
		return
	}
	var input domain.SurveySubmission
	if !s.decode(w, r, &input) {
		return
	}
	id, err := s.service.SubmitSurvey(r.Context(), surveyToken(r), input)
	s.respond(w, r, map[string]string{"responseId": id}, err, 200)
}
func (s *Server) surveyStats(w http.ResponseWriter, r *http.Request) {
	if !s.surveyRoute(w, r) {
		return
	}
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	result, err := s.service.SurveyStatistics(r.Context(), subject)
	s.respond(w, r, result, err, 200)
}
func (s *Server) surveyResponses(w http.ResponseWriter, r *http.Request) {
	if !s.surveyRoute(w, r) {
		return
	}
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, _ = strconv.Atoi(raw)
	}
	result, err := s.service.ListSurveyResponses(r.Context(), subject, offset, limit)
	s.respond(w, r, result, err, 200)
}
func (s *Server) surveyResponse(w http.ResponseWriter, r *http.Request) {
	if !s.surveyRoute(w, r) {
		return
	}
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	result, err := s.service.GetSurveyResponse(r.Context(), subject, r.PathValue("responseID"))
	s.respond(w, r, result, err, 200)
}

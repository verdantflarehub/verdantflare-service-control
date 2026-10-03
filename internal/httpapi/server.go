package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/config"
	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

const maxBodyBytes = 1 << 20

type Server struct {
	config  config.Config
	service *control.Service
	logger  *slog.Logger
	handler http.Handler
}

func New(config config.Config, service *control.Service, logger *slog.Logger) *Server {
	server := &Server{config: config, service: service, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /readyz", server.health)

	mux.HandleFunc("GET /api/control/context", server.getContext)
	mux.HandleFunc("PUT /api/control/context/active-organization", server.setActiveOrganization)
	mux.HandleFunc("GET /api/control/overview", server.getOverview)

	mux.HandleFunc("GET /api/control/market/apps", server.listApps)
	mux.HandleFunc("GET /api/control/market/apps/{appID}", server.getApp)

	mux.HandleFunc("GET /api/control/experience/sessions", server.listExperienceSessions)
	mux.HandleFunc("POST /api/control/experience/sessions", server.createExperienceSession)
	mux.HandleFunc("DELETE /api/control/experience/sessions/{sessionID}", server.closeExperienceSession)

	mux.HandleFunc("GET /api/control/api-keys", server.listAPIKeys)
	mux.HandleFunc("POST /api/control/api-keys", server.createAPIKey)
	mux.HandleFunc("DELETE /api/control/api-keys/{keyID}", server.revokeAPIKey)
	mux.HandleFunc("GET /api/control/api/models", server.listModels)
	mux.HandleFunc("GET /api/control/api/tasks", server.listAPITasks)
	mux.HandleFunc("GET /api/control/api/usage", server.getUsage)

	mux.HandleFunc("GET /api/control/settings/organization", server.getOrganization)
	mux.HandleFunc("PATCH /api/control/settings/organization", server.updateOrganization)
	mux.HandleFunc("GET /api/control/settings/members", server.listMembers)
	mux.HandleFunc("POST /api/control/settings/members", server.inviteMember)
	mux.HandleFunc("GET /api/control/settings/billing", server.getBilling)

	mux.HandleFunc("GET /api/control/ops/releases", server.listReleases)
	mux.HandleFunc("GET /api/control/ops/organizations", server.listOperationsOrganizations)
	mux.HandleFunc("POST /api/control/ops/apps", server.createManagedApp)
	mux.HandleFunc("GET /api/control/ops/apps/{appID}", server.getManagedApp)
	mux.HandleFunc("PATCH /api/control/ops/apps/{appID}", server.updateManagedApp)
	mux.HandleFunc("POST /api/control/ops/organizations", server.createManagedOrganization)
	mux.HandleFunc("GET /api/control/ops/organizations/{organizationID}", server.getManagedOrganization)
	mux.HandleFunc("PATCH /api/control/ops/organizations/{organizationID}", server.updateManagedOrganization)

	server.handler = server.middleware(mux)
	return server
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	s.handler.ServeHTTP(writer, request)
}

func (s *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) getContext(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.Context(request.Context(), subject)
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) setActiveOrganization(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	var input struct {
		OrganizationID string `json:"organizationId"`
	}
	if !s.decode(writer, request, &input) {
		return
	}
	result, err := s.service.SetActiveOrganization(request.Context(), subject, input.OrganizationID)
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) getOverview(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.Overview(request.Context(), subject)
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) listApps(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.ListApps(request.Context(), subject)
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) getApp(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.GetApp(request.Context(), subject, request.PathValue("appID"))
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) listExperienceSessions(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.ListExperienceSessions(request.Context(), subject)
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) createExperienceSession(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	var input control.CreateExperienceInput
	if !s.decode(writer, request, &input) {
		return
	}
	result, err := s.service.CreateExperienceSession(request.Context(), subject, input)
	s.respond(writer, request, result, err, http.StatusCreated)
}

func (s *Server) closeExperienceSession(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	_, err := s.service.CloseExperienceSession(request.Context(), subject, request.PathValue("sessionID"))
	if err != nil {
		s.writeError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAPIKeys(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.ListAPIKeys(request.Context(), subject)
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) createAPIKey(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	var input control.CreateAPIKeyInput
	if !s.decode(writer, request, &input) {
		return
	}
	result, err := s.service.CreateAPIKey(request.Context(), subject, input)
	s.respond(writer, request, result, err, http.StatusCreated)
}

func (s *Server) revokeAPIKey(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	if err := s.service.RevokeAPIKey(request.Context(), subject, request.PathValue("keyID")); err != nil {
		s.writeError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) listModels(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.ListModels(request.Context(), subject)
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) listAPITasks(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.ListAPITasks(request.Context(), subject)
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) getUsage(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.Usage(request.Context(), subject)
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) getOrganization(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.Organization(request.Context(), subject)
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) updateOrganization(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	var input control.UpdateOrganizationInput
	if !s.decode(writer, request, &input) {
		return
	}
	result, err := s.service.UpdateOrganization(request.Context(), subject, input)
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) listMembers(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.ListMembers(request.Context(), subject)
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) inviteMember(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	var input control.InviteMemberInput
	if !s.decode(writer, request, &input) {
		return
	}
	result, err := s.service.InviteMember(request.Context(), subject, input)
	s.respond(writer, request, result, err, http.StatusCreated)
}

func (s *Server) getBilling(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.Billing(request.Context(), subject)
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) listReleases(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.ListReleases(request.Context(), subject)
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) listOperationsOrganizations(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.ListOperationsOrganizations(request.Context(), subject)
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) subject(writer http.ResponseWriter, request *http.Request) (string, bool) {
	subject := ""
	if s.config.TrustAuthHeaders {
		subject = strings.TrimSpace(request.Header.Get("X-VF-Login-Subject"))
	}
	if subject == "" {
		subject = s.config.DevLoginSubject
	}
	if subject == "" {
		s.writeError(writer, request, domain.NewError(401, "unauthorized", "未建立 Login 会话"))
		return "", false
	}
	return subject, true
}

func (s *Server) decode(writer http.ResponseWriter, request *http.Request, target any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, maxBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		s.writeError(writer, request, domain.NewError(400, "invalid_json", "请求体不是有效 JSON 或包含未知字段"))
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		s.writeError(writer, request, domain.NewError(400, "invalid_json", "请求体只能包含一个 JSON 对象"))
		return false
	}
	return true
}

func (s *Server) respond(writer http.ResponseWriter, request *http.Request, result any, err error, status int) {
	if err != nil {
		s.writeError(writer, request, err)
		return
	}
	writeJSON(writer, status, result)
}

func (s *Server) writeError(writer http.ResponseWriter, request *http.Request, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	message := "服务暂时不可用"
	var domainError *domain.Error
	if errors.As(err, &domainError) {
		status = domainError.Status
		code = domainError.Code
		message = domainError.Message
	} else {
		s.logger.Error("request failed", "request_id", requestID(request.Context()), "method", request.Method, "path", request.URL.Path, "error", err)
	}
	writeJSON(writer, status, map[string]any{"error": map[string]string{"code": code, "message": message, "requestId": requestID(request.Context())}})
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		requestID := strings.TrimSpace(request.Header.Get("X-Request-ID"))
		if requestID == "" || len(requestID) > 100 {
			requestID = newRequestID()
		}
		request = request.WithContext(context.WithValue(request.Context(), requestIDKey{}, requestID))
		writer.Header().Set("X-Request-ID", requestID)
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "same-origin")
		writer.Header().Set("Cache-Control", "no-store")

		if origin := request.Header.Get("Origin"); origin != "" && slices.Contains(s.config.AllowedOrigins, origin) {
			writer.Header().Set("Access-Control-Allow-Origin", origin)
			writer.Header().Set("Access-Control-Allow-Credentials", "true")
			writer.Header().Set("Vary", "Origin")
			writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Request-ID")
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			if request.Method == http.MethodOptions {
				writer.WriteHeader(http.StatusNoContent)
				return
			}
		}

		recorder := &statusRecorder{ResponseWriter: writer, status: http.StatusOK}
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("panic recovered", "request_id", requestID, "method", request.Method, "path", request.URL.Path)
				if !recorder.wroteHeader {
					s.writeError(recorder, request, errors.New("panic recovered"))
				}
			}
			s.logger.Info("request", "request_id", requestID, "method", request.Method, "path", request.URL.Path, "status", recorder.status, "duration_ms", time.Since(started).Milliseconds())
		}()
		next.ServeHTTP(recorder, request)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(body)
}

type requestIDKey struct{}

func requestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey{}).(string)
	return value
}

func newRequestID() string {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(bytes)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

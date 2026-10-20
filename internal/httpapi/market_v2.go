package httpapi

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func setAppETag(w http.ResponseWriter, app domain.App) {
	if app.ID != "" && app.Revision > 0 {
		w.Header().Set("ETag", fmt.Sprintf(`"app:%s:%d"`, app.ID, app.Revision))
	}
}

func (s *Server) listManagedAppsV2(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	apps, err := s.service.ListManagedAppsV2(r.Context(), subject)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if apps == nil {
		apps = []domain.App{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": apps})
}

func (s *Server) createManagedAppV2(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var input control.CreateAppV2Input
	if !s.decode(w, r, &input) {
		return
	}
	app, err := s.service.CreateManagedAppV2(r.Context(), subject, input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	setAppETag(w, app)
	writeJSON(w, http.StatusCreated, app)
}

func (s *Server) getManagedAppV2(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	managed, err := s.service.ManagedApp(r.Context(), subject, r.PathValue("appID"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	setAppETag(w, managed.App)
	writeJSON(w, http.StatusOK, managed.App)
}

func (s *Server) getAppPublicationReadinessV2(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	result, err := s.service.AppPublicationReadiness(r.Context(), subject, r.PathValue("appID"))
	s.respond(w, r, result, err, http.StatusOK)
}

func (s *Server) patchManagedAppV2(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var input control.PatchAppV2Input
	if !s.decode(w, r, &input) {
		return
	}
	app, err := s.service.PatchManagedAppV2(r.Context(), subject, r.PathValue("appID"), r.Header.Get("If-Match"), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	setAppETag(w, app)
	writeJSON(w, http.StatusOK, app)
}

func (s *Server) createAppVersionV2(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var input control.CreateAppVersionInput
	if !s.decode(w, r, &input) {
		return
	}
	appID := r.PathValue("appID")
	targetVersion := strings.TrimSpace(input.Version)
	if targetVersion == "" && input.Chart != nil {
		targetVersion = strings.TrimSpace(input.Chart.Version)
	}
	_, existedBefore := s.service.AppVersion(r.Context(), subject, appID, targetVersion)
	result, err := s.service.CreateAppVersion(r.Context(), subject, appID, input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	status := http.StatusCreated
	if existedBefore == nil {
		status = http.StatusOK
	}
	writeJSON(w, status, result)
}

func (s *Server) listAppVersionsV2(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	appID := r.PathValue("appID")
	versions, err := s.service.ListAppVersions(r.Context(), subject, appID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if versions == nil {
		versions = []domain.AppVersion{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"appId": appID,
		"items": versions,
	})
}

func (s *Server) getAppVersionV2(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	result, err := s.service.AppVersion(r.Context(), subject, r.PathValue("appID"), r.PathValue("version"))
	s.respond(w, r, result, err, http.StatusOK)
}

func (s *Server) selectAppReleaseV2(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var input control.ReleaseSelectionInput
	if !s.decode(w, r, &input) {
		return
	}
	app, err := s.service.SelectAppReleaseV2(r.Context(), subject, r.PathValue("appID"), r.Header.Get("If-Match"), input.Version)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	setAppETag(w, app)
	writeJSON(w, http.StatusOK, app)
}

func (s *Server) opsAppsYAMLV2(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	if _, err := s.service.ListManagedAppsV2(r.Context(), subject); err != nil {
		s.writeError(w, r, err)
		return
	}
	_, raw, etag, err := s.service.MarketAppsYAML(r.Context(), "ops", false)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if matchETag(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Server) stationInstallGrantV2(w http.ResponseWriter, r *http.Request) {
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(r.Header.Values("Authorization")) != 1 || !strings.HasPrefix(authorization, "Bearer ") || s.config.StationCatalogToken == "" || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(authorization, "Bearer ")), []byte(s.config.StationCatalogToken)) != 1 {
		s.writeError(w, r, domain.NewError(401, "UNAUTHORIZED", "缺少有效 Station 凭证"))
		return
	}
	stationOrg := r.PathValue("organizationID")
	controlOrg := ""
	for _, binding := range s.config.StationOrganizationBindings {
		if binding.StationOrganizationID == stationOrg {
			controlOrg = binding.ControlOrganizationID
			break
		}
	}
	grant, err := s.service.StationInstallGrant(r.Context(), r.PathValue("appID"), r.PathValue("version"), controlOrg)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if !grant.Allowed {
		s.writeError(w, r, domain.NewError(403, "INSTALL_GRANT_REQUIRED", "该组织未获应用安装权益"))
		return
	}
	writeJSON(w, http.StatusOK, grant)
}

func (s *Server) authorizeStationV2(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 && len(r.TLS.PeerCertificates) > 0 {
		identity, ok := s.stationIdentity(w, r)
		if !ok {
			return "", false
		}
		return identity.StationID, true
	}
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		token := strings.TrimSpace(authHeader[7:])
		expected := strings.TrimSpace(s.config.StationCatalogToken)
		if expected != "" && subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1 {
			return "station-bearer", true
		}
		s.writeError(w, r, domain.NewError(401, "UNAUTHORIZED", "Station 凭证无效或已过期"))
		return "", false
	}
	if strings.TrimSpace(r.Header.Get("X-VF-Login-Subject")) == "" && strings.TrimSpace(r.Header.Get("Cookie")) != "" && strings.TrimSpace(s.config.LoginDirectoryBaseURL) != "" {
		if cookieSubject := s.resolveCookieSessionSubject(r); cookieSubject != "" {
			return cookieSubject, true
		}
	}
	if s.config.StationCatalogToken != "" && strings.TrimSpace(r.Header.Get("X-VF-Login-Subject")) == "" {
		s.writeError(w, r, domain.NewError(401, "UNAUTHORIZED", "缺少 Station 访问凭证"))
		return "", false
	}
	subject, ok := s.subject(w, r)
	if !ok {
		return "", false
	}
	return subject, true
}

func (s *Server) resolveCookieSessionSubject(r *http.Request) string {
	base := strings.TrimRight(strings.TrimSpace(s.config.LoginDirectoryBaseURL), "/")
	if base == "" {
		return ""
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, base+"/api/auth/session", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Cookie", r.Header.Get("Cookie"))
	if r.Host != "" {
		req.Header.Set("Host", r.Host)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	return strings.TrimSpace(resp.Header.Get("X-VF-Login-Subject"))
}

func (s *Server) stationAppsYAMLV2(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.authorizeStationV2(w, r)
	if !ok {
		return
	}
	_, raw, etag, err := s.service.MarketAppsYAML(r.Context(), scope, true)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=30, stale-while-revalidate=300")
	if matchETag(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Server) stationAppVersionV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizeStationV2(w, r); !ok {
		return
	}
	ver, err := s.service.StationAppVersionV2(r.Context(), r.PathValue("appID"), r.PathValue("version"))
	s.respond(w, r, ver, err, http.StatusOK)
}

func matchETag(ifNoneMatch, etag string) bool {
	if strings.TrimSpace(ifNoneMatch) == "" || etag == "" {
		return false
	}
	cleanTarget := strings.Trim(strings.TrimPrefix(strings.TrimSpace(etag), "W/"), `"`)
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		c := strings.TrimSpace(candidate)
		if c == "*" {
			return true
		}
		cleanCandidate := strings.Trim(strings.TrimPrefix(c, "W/"), `"`)
		if cleanCandidate == cleanTarget {
			return true
		}
	}
	return false
}

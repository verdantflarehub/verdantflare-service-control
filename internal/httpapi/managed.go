package httpapi

import (
	"io"
	"net/http"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
)

const maxChartUploadBytes = 2 << 20

func (s *Server) auditAppChart(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxChartUploadBytes+(16<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		s.writeError(w, r, control.InvalidChartUpload())
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "chart" || !strings.HasSuffix(strings.ToLower(part.FileName()), ".tgz") {
		s.writeError(w, r, control.InvalidChartUpload())
		return
	}
	archive, err := io.ReadAll(part)
	if err != nil || len(archive) == 0 || len(archive) > maxChartUploadBytes {
		s.writeError(w, r, control.InvalidChartUpload())
		return
	}
	if next, err := reader.NextPart(); err != io.EOF || next != nil {
		s.writeError(w, r, control.InvalidChartUpload())
		return
	}
	result, err := s.service.AuditAppChart(r.Context(), subject, r.PathValue("appID"), archive)
	s.respond(w, r, result, err, http.StatusOK)
}

func (s *Server) createManagedApp(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var input control.ManagedAppInput
	if !s.decode(w, r, &input) {
		return
	}
	result, err := s.service.CreateManagedApp(r.Context(), subject, input)
	s.respond(w, r, result, err, http.StatusCreated)
}

func (s *Server) getManagedApp(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	result, err := s.service.ManagedApp(r.Context(), subject, r.PathValue("appID"))
	s.respond(w, r, result, err, http.StatusOK)
}

func (s *Server) updateManagedApp(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var input control.ManagedAppInput
	if !s.decode(w, r, &input) {
		return
	}
	result, err := s.service.UpdateManagedApp(r.Context(), subject, r.PathValue("appID"), input)
	s.respond(w, r, result, err, http.StatusOK)
}

func (s *Server) createAppVersion(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var input control.CreateAppVersionInput
	if !s.decode(w, r, &input) {
		return
	}
	result, err := s.service.CreateAppVersion(r.Context(), subject, r.PathValue("appID"), input)
	s.respond(w, r, result, err, http.StatusCreated)
}

func (s *Server) listAppVersions(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	result, err := s.service.ListAppVersions(r.Context(), subject, r.PathValue("appID"))
	s.respond(w, r, result, err, http.StatusOK)
}

func (s *Server) getAppVersion(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	result, err := s.service.AppVersion(r.Context(), subject, r.PathValue("appID"), r.PathValue("version"))
	s.respond(w, r, result, err, http.StatusOK)
}

func (s *Server) createManagedOrganization(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var input control.CreateManagedOrganizationInput
	if !s.decode(w, r, &input) {
		return
	}
	result, err := s.service.CreateManagedOrganization(r.Context(), subject, input)
	s.respond(w, r, result, err, http.StatusCreated)
}

func (s *Server) getManagedOrganization(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	result, err := s.service.ManagedOrganization(r.Context(), subject, r.PathValue("organizationID"))
	s.respond(w, r, result, err, http.StatusOK)
}

func (s *Server) updateManagedOrganization(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var input control.UpdateManagedOrganizationInput
	if !s.decode(w, r, &input) {
		return
	}
	result, err := s.service.UpdateManagedOrganization(r.Context(), subject, r.PathValue("organizationID"), input)
	s.respond(w, r, result, err, http.StatusOK)
}

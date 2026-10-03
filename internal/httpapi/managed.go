package httpapi

import (
	"net/http"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
)

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

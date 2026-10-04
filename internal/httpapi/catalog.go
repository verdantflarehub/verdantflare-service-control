package httpapi

import (
	"net/http"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func (s *Server) publicCatalog(w http.ResponseWriter, r *http.Request) {
	result, err := s.service.PublicCatalog(r.Context())
	s.respond(w, r, result, err, http.StatusOK)
}

func (s *Server) listManagedModels(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	result, err := s.service.ListManagedModels(r.Context(), subject)
	s.respond(w, r, result, err, http.StatusOK)
}

func (s *Server) listGatewayModels(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	result, err := s.service.ListGatewayModels(r.Context(), subject)
	s.respond(w, r, result, err, http.StatusOK)
}

func (s *Server) createManagedModel(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var model domain.PublicModel
	if !s.decode(w, r, &model) {
		return
	}
	result, err := s.service.CreateManagedModel(r.Context(), subject, model)
	s.respond(w, r, result, err, http.StatusCreated)
}

func (s *Server) updateManagedModel(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var model domain.PublicModel
	if !s.decode(w, r, &model) {
		return
	}
	result, err := s.service.UpdateManagedModel(r.Context(), subject, r.PathValue("modelID"), model)
	s.respond(w, r, result, err, http.StatusOK)
}

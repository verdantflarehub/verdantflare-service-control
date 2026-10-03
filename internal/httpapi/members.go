package httpapi

import (
	"net/http"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
)

func (s *Server) updateMember(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var input control.UpdateMemberInput
	if !s.decode(w, r, &input) {
		return
	}
	result, err := s.service.UpdateMember(r.Context(), subject, r.PathValue("memberID"), input)
	s.respond(w, r, result, err, http.StatusOK)
}

func (s *Server) createManagedMember(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var input control.InviteMemberInput
	if !s.decode(w, r, &input) {
		return
	}
	result, err := s.service.CreateManagedMember(r.Context(), subject, r.PathValue("organizationID"), input)
	s.respond(w, r, result, err, http.StatusCreated)
}

func (s *Server) updateManagedMember(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var input control.UpdateMemberInput
	if !s.decode(w, r, &input) {
		return
	}
	result, err := s.service.UpdateManagedMember(r.Context(), subject, r.PathValue("organizationID"), r.PathValue("memberID"), input)
	s.respond(w, r, result, err, http.StatusOK)
}

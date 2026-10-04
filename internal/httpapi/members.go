package httpapi

import (
	"net/http"
	"strconv"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func (s *Server) listGuests(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			s.writeError(w, r, controlGuestQueryError())
			return
		}
		limit = parsed
	}
	result, err := s.service.ListGuests(r.Context(), subject, r.URL.Query().Get("cursor"), r.URL.Query().Get("q"), limit)
	s.respond(w, r, result, err, http.StatusOK)
}

func controlGuestQueryError() error {
	return domain.NewError(400, "guest_query_invalid", "游客查询参数无效")
}

func (s *Server) bindManagedMember(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var input control.BindMemberInput
	if !s.decode(w, r, &input) {
		return
	}
	result, err := s.service.BindManagedMember(r.Context(), subject, r.PathValue("organizationID"), input)
	s.respond(w, r, result, err, http.StatusOK)
}

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

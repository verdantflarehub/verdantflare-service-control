package httpapi

import (
	"net/http"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
)

func (s *Server) probeAPIKey(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.ProbeAPIKey(request.Context(), subject, request.PathValue("keyID"))
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) getOrganizationAPICredit(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.OrganizationAPICredit(request.Context(), subject, request.PathValue("organizationID"))
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) grantOrganizationAPICredit(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	var input control.GrantAPICreditInput
	if !s.decode(writer, request, &input) {
		return
	}
	result, err := s.service.GrantOrganizationAPICredit(request.Context(), subject, request.PathValue("organizationID"), input)
	s.respond(writer, request, result, err, http.StatusOK)
}

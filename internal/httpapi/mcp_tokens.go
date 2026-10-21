package httpapi

import (
	"net/http"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
)

func (s *Server) listMCPTokens(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.ListMCPTokens(request.Context(), subject)
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) createMCPToken(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	var input control.CreateMCPTokenInput
	if !s.decode(writer, request, &input) {
		return
	}
	result, err := s.service.CreateMCPToken(request.Context(), subject, input)
	s.respond(writer, request, result, err, http.StatusCreated)
}

func (s *Server) revokeMCPToken(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	if err := s.service.RevokeMCPToken(request.Context(), subject, request.PathValue("tokenID")); err != nil {
		s.writeError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

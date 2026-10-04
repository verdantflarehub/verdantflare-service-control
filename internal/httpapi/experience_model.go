package httpapi

import (
	"net/http"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
)

func (s *Server) listModelExperienceRuns(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.ListModelExperienceRuns(request.Context(), subject)
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) getModelExperienceRun(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	result, err := s.service.GetModelExperienceRun(request.Context(), subject, request.PathValue("runID"))
	s.respond(writer, request, result, err, http.StatusOK)
}

func (s *Server) createModelExperienceRun(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	var input control.CreateModelExperienceInput
	if !s.decode(writer, request, &input) {
		return
	}
	result, err := s.service.CreateModelExperienceRun(request.Context(), subject, input)
	s.respond(writer, request, result, err, http.StatusAccepted)
}

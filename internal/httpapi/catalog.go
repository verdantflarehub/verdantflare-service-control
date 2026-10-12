package httpapi

import (
	"io"
	"net/http"
	"strconv"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

const maxModelIconUploadBytes = 1 << 20

func (s *Server) uploadModelProviderIcon(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxModelIconUploadBytes+(16<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		s.writeError(w, r, control.InvalidModelIconUpload())
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "icon" || part.FileName() == "" {
		s.writeError(w, r, control.InvalidModelIconUpload())
		return
	}
	data, err := io.ReadAll(part)
	if err != nil || len(data) == 0 || len(data) > maxModelIconUploadBytes {
		s.writeError(w, r, control.InvalidModelIconUpload())
		return
	}
	if next, err := reader.NextPart(); err != io.EOF || next != nil {
		s.writeError(w, r, control.InvalidModelIconUpload())
		return
	}
	result, err := s.service.UploadModelProviderIcon(r.Context(), subject, data)
	s.respond(w, r, result, err, http.StatusCreated)
}

func (s *Server) publicModelProviderIcon(w http.ResponseWriter, r *http.Request) {
	icon, err := s.service.ModelProviderIcon(r.Context(), r.PathValue("digest"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	etag := `"` + icon.SHA256 + `"`
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", icon.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(icon.Data)))
	_, _ = w.Write(icon.Data)
}

func (s *Server) publicCatalog(w http.ResponseWriter, r *http.Request) {
	result, err := s.service.PublicCatalog(r.Context())
	s.respond(w, r, result, err, http.StatusOK)
}

func (s *Server) guestCatalog(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.subject(w, r); !ok {
		return
	}
	result, err := s.service.GuestCatalog(r.Context())
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

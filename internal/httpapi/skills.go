package httpapi

import (
	"fmt"
	"net/http"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func setSkillETag(w http.ResponseWriter, entry domain.SkillCatalogEntry) {
	w.Header().Set("ETag", fmt.Sprintf(`"skill:%s:%d"`, entry.EntryID, entry.Revision))
}

func (s *Server) listSkills(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	items, err := s.service.ListPublicSkillEntries(r.Context(), subject)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getSkill(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	entry, err := s.service.PublicSkillEntry(r.Context(), subject, r.PathValue("entryID"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	setSkillETag(w, entry)
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) listManagedSkills(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	items, err := s.service.ListManagedSkillEntries(r.Context(), subject)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getManagedSkill(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	entry, err := s.service.ManagedSkillEntry(r.Context(), subject, r.PathValue("entryID"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	setSkillETag(w, entry)
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) createManagedSkill(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var input control.CreateSkillEntryInput
	if !s.decode(w, r, &input) {
		return
	}
	entry, err := s.service.CreateManagedSkillEntry(r.Context(), subject, input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	setSkillETag(w, entry)
	writeJSON(w, http.StatusCreated, entry)
}

func (s *Server) patchManagedSkill(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var input control.PatchSkillEntryInput
	if !s.decode(w, r, &input) {
		return
	}
	entry, err := s.service.PatchManagedSkillEntry(r.Context(), subject, r.PathValue("entryID"), r.Header.Get("If-Match"), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	setSkillETag(w, entry)
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) skillAudit(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	items, err := s.service.SkillAudit(r.Context(), subject, r.PathValue("entryID"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

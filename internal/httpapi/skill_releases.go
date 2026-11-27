package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

// Releases are registered only through the skill publishing MCP; the page
// lists, inspects, publishes and withdraws them through the same services.
func (s *Server) listSkillReleases(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	list, err := s.service.ListManagedSkillReleases(r.Context(), subject)
	w.Header().Set("Cache-Control", "private, no-store")
	s.respond(w, r, list, err, http.StatusOK)
}

func (s *Server) getSkillRelease(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	detail, err := s.service.SkillReleaseDetailFor(r.Context(), subject, r.PathValue("releaseID"))
	w.Header().Set("Cache-Control", "private, no-store")
	s.respond(w, r, detail, err, http.StatusOK)
}

type skillReleaseChange struct {
	ExpectedArchiveSHA256 string `json:"expectedArchiveSha256"`
	Reason                string `json:"reason"`
}

// readSkillReleaseChange accepts an empty body or a small JSON object.
func readSkillReleaseChange(r *http.Request) (skillReleaseChange, error) {
	var change skillReleaseChange
	body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil || len(body) > 4096 {
		return change, domain.NewError(400, "INVALID_REQUEST", "请求体无效")
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return change, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&change); err != nil {
		return change, domain.NewError(400, "INVALID_REQUEST", "请求体须为 JSON 对象")
	}
	return change, nil
}

func (s *Server) publishSkillRelease(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	change, err := readSkillReleaseChange(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	release, err := s.service.PublishSkillRelease(r.Context(), control.SkillActor{Subject: subject}, r.PathValue("releaseID"), change.ExpectedArchiveSHA256)
	w.Header().Set("Cache-Control", "private, no-store")
	s.respond(w, r, release, err, http.StatusOK)
}

func (s *Server) withdrawSkillRelease(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	change, err := readSkillReleaseChange(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	release, err := s.service.WithdrawSkillRelease(r.Context(), control.SkillActor{Subject: subject}, r.PathValue("releaseID"), change.ExpectedArchiveSHA256, change.Reason)
	w.Header().Set("Cache-Control", "private, no-store")
	s.respond(w, r, release, err, http.StatusOK)
}

func (s *Server) currentSkillRelease(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	view, err := s.service.CurrentSkillRelease(r.Context(), subject)
	w.Header().Set("Cache-Control", "private, no-store")
	s.respond(w, r, view, err, http.StatusOK)
}

func (s *Server) downloadSkillReleaseFile(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	if r.Header.Get("Range") != "" {
		s.writeError(w, r, domain.NewError(416, "range_not_supported", "技能文件不支持分段下载"))
		return
	}
	releaseID, name := r.PathValue("releaseID"), r.PathValue("name")
	file, err := s.service.DownloadSkillReleaseFile(r.Context(), subject, releaseID, name)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	contentType := "application/zip"
	switch file.Kind {
	case "guide":
		contentType = "text/markdown; charset=utf-8"
	case "manifest":
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(file.Size, 10))
	w.Header().Set("Content-Disposition", `attachment; filename="`+file.Name+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-VF-Skill-Release", releaseID)
	w.Header().Set("X-Content-SHA256", file.SHA256)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Data)
}

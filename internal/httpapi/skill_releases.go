package httpapi

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

const maxSkillReleaseUploadBytes = 32 << 20

func invalidSkillReleaseUpload() error {
	return domain.NewError(400, "skill_release_upload_invalid", "请以 multipart 字段 archive 上传一个 .zip 发行包")
}

func (s *Server) uploadSkillRelease(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSkillReleaseUploadBytes+(16<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		s.writeError(w, r, invalidSkillReleaseUpload())
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "archive" || !strings.HasSuffix(strings.ToLower(part.FileName()), ".zip") {
		s.writeError(w, r, invalidSkillReleaseUpload())
		return
	}
	archive, err := io.ReadAll(part)
	if err != nil || len(archive) == 0 || len(archive) > maxSkillReleaseUploadBytes {
		s.writeError(w, r, invalidSkillReleaseUpload())
		return
	}
	if next, err := reader.NextPart(); err != io.EOF || next != nil {
		s.writeError(w, r, invalidSkillReleaseUpload())
		return
	}
	release, created, err := s.service.UploadSkillRelease(r.Context(), subject, archive)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	w.Header().Set("Cache-Control", "private, no-store")
	s.respond(w, r, release, err, status)
}

func (s *Server) listSkillReleases(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	items, err := s.service.ListManagedSkillReleases(r.Context(), subject)
	w.Header().Set("Cache-Control", "private, no-store")
	s.respond(w, r, map[string]any{"items": items}, err, http.StatusOK)
}

func (s *Server) setSkillReleaseStatus(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subject, ok := s.subject(w, r)
		if !ok {
			return
		}
		release, err := s.service.SetSkillReleaseStatus(r.Context(), subject, r.PathValue("releaseID"), status)
		w.Header().Set("Cache-Control", "private, no-store")
		s.respond(w, r, release, err, http.StatusOK)
	}
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

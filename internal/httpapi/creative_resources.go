package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

func newResourceID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return prefix + "_" + time.Now().UTC().Format("20060102150405")
	}
	return prefix + "_" + hex.EncodeToString(b)
}

func (s *Server) listCreativeResources(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	q := request.URL.Query()
	items, err := s.service.ListCreativeResources(request.Context(), subject, q.Get("media_type"), q.Get("subject_category"))
	if err != nil {
		s.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getCreativeResource(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	item, err := s.service.GetCreativeResource(request.Context(), subject, request.PathValue("resourceID"))
	s.respond(writer, request, item, err, http.StatusOK)
}

func (s *Server) getCreativeResourceImage(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	img, err := s.service.GetCreativeResourceImage(request.Context(), subject, request.PathValue("resourceID"))
	if err != nil {
		s.writeError(writer, request, err)
		return
	}
	writer.Header().Set("Content-Type", img.Mime)
	writer.Header().Set("Cache-Control", "private, max-age=300")
	writer.Header().Set("ETag", `"`+img.SHA256+`"`)
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(img.Data)
}

func (s *Server) getCreativeResourceItemImage(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	img, err := s.service.GetCreativeResourceItemImage(request.Context(), subject, request.PathValue("resourceID"), request.PathValue("itemID"))
	if err != nil {
		s.writeError(writer, request, err)
		return
	}
	writer.Header().Set("Content-Type", img.Mime)
	writer.Header().Set("Cache-Control", "private, max-age=300")
	writer.Header().Set("ETag", `"`+img.SHA256+`"`)
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(img.Data)
}

func (s *Server) withdrawCreativeResource(writer http.ResponseWriter, request *http.Request) {
	subject, ok := s.subject(writer, request)
	if !ok {
		return
	}
	item, err := s.service.WithdrawCreativeResource(request.Context(), subject, request.PathValue("resourceID"))
	s.respond(writer, request, item, err, http.StatusOK)
}

func registerAssistantResourceRoutes(mux *http.ServeMux, repo any) {
	resStore, ok := repo.(store.CreativeResourceStore)
	if !ok {
		return
	}
	mux.HandleFunc("POST /internal/assistant/resources", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ResourceID     string `json:"resource_id"`
			OrganizationID string `json:"organization_id"`
			OwnerSubject   string `json:"owner_subject"`
			Title           string `json:"title"`
			Category        string `json:"category"`
			SubjectCategory string `json:"subject_category"`
			MediaType       string `json:"media_type"`
			PostKind        string `json:"post_kind"`
			Prompt         string `json:"prompt"`
			PromptOrigin   string `json:"prompt_origin"`
			SourceURL      string `json:"source_url"`
			SourceAuthor   string `json:"source_author"`
			Engine         string `json:"engine"`
			ModelName      string `json:"model_name"`
			AspectRatio    string `json:"aspect_ratio"`
			Resolution     string `json:"resolution"`
			DrivePath      string `json:"drive_path"`
			ImageMime      string `json:"image_mime"`
			ImageBase64    string `json:"image_base64"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20))
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid resource payload", http.StatusBadRequest)
			return
		}
		imgBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(input.ImageBase64))
		if err != nil || len(imgBytes) == 0 {
			http.Error(w, "invalid image_base64", http.StatusBadRequest)
			return
		}
		resID := strings.TrimSpace(input.ResourceID)
		if resID == "" {
			resID = newResourceID("res")
		}
		orgID := strings.TrimSpace(input.OrganizationID)
		if orgID == "" {
			orgID = "org_verdantflare"
		}
		mediaType := strings.TrimSpace(input.MediaType)
		if mediaType == "" {
			if strings.TrimSpace(input.Category) == "video" {
				mediaType = "video"
			} else {
				mediaType = "image"
			}
		}
		subjectCat := strings.TrimSpace(input.SubjectCategory)
		if subjectCat != "" && subjectCat != "beauty" && subjectCat != "handsome" && subjectCat != "animal" {
			http.Error(w, "INVALID_SUBJECT_CATEGORY: must be beauty, handsome, or animal", http.StatusBadRequest)
			return
		}
		if subjectCat == "" {
			subjectCat = "beauty"
		}
		created, err := resStore.CreateCreativeResource(r.Context(), domain.CreativeResource{
			ResourceID:      resID,
			OrganizationID:  orgID,
			OwnerSubject:    strings.TrimSpace(input.OwnerSubject),
			Title:           strings.TrimSpace(input.Title),
			Category:        strings.TrimSpace(input.Category),
			SubjectCategory: subjectCat,
			MediaType:       mediaType,
			PostKind:       strings.TrimSpace(input.PostKind),
			Prompt:         strings.TrimSpace(input.Prompt),
			PromptOrigin:   strings.TrimSpace(input.PromptOrigin),
			SourceURL:      strings.TrimSpace(input.SourceURL),
			SourceAuthor:   strings.TrimSpace(input.SourceAuthor),
			Engine:         strings.TrimSpace(input.Engine),
			ModelName:      strings.TrimSpace(input.ModelName),
			AspectRatio:    strings.TrimSpace(input.AspectRatio),
			Resolution:     strings.TrimSpace(input.Resolution),
			DrivePath:      strings.TrimSpace(input.DrivePath),
			ImageMime:      strings.TrimSpace(input.ImageMime),
		}, imgBytes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		pathPrefix := "/prompts/images/"
		if created.MediaType == "video" {
			pathPrefix = "/prompts/videos/"
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"resource_id": created.ResourceID,
			"viewer_url":  "https://hub.verdantflare.ai" + pathPrefix + created.ResourceID,
			"resource":    created,
		})
	})

	mux.HandleFunc("POST /internal/assistant/resources/{resourceID}/items", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ItemID      string `json:"item_id"`
			PostKind    string `json:"post_kind"`
			SortOrder   int    `json:"sort_order"`
			StepLabel   string `json:"step_label"`
			Label       string `json:"label"`
			Engine      string `json:"engine"`
			ModelName   string `json:"model_name"`
			AspectRatio string `json:"aspect_ratio"`
			Resolution  string `json:"resolution"`
			EditPrompt  string `json:"edit_prompt"`
			Note        string `json:"note"`
			ImageMime   string `json:"image_mime"`
			ImageBase64 string `json:"image_base64"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20))
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid item payload", http.StatusBadRequest)
			return
		}
		imgBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(input.ImageBase64))
		if err != nil || len(imgBytes) == 0 {
			http.Error(w, "invalid image_base64", http.StatusBadRequest)
			return
		}
		itemID := strings.TrimSpace(input.ItemID)
		if itemID == "" {
			itemID = newResourceID("itm")
		}
		appended, err := resStore.AppendCreativeResourceItem(r.Context(), r.PathValue("resourceID"), strings.TrimSpace(input.PostKind), domain.CreativeResourceItem{
			ItemID:      itemID,
			SortOrder:   input.SortOrder,
			StepLabel:   strings.TrimSpace(input.StepLabel),
			Label:       strings.TrimSpace(input.Label),
			Engine:      strings.TrimSpace(input.Engine),
			ModelName:   strings.TrimSpace(input.ModelName),
			AspectRatio: strings.TrimSpace(input.AspectRatio),
			Resolution:  strings.TrimSpace(input.Resolution),
			EditPrompt:  strings.TrimSpace(input.EditPrompt),
			Note:        strings.TrimSpace(input.Note),
			ImageMime:   strings.TrimSpace(input.ImageMime),
		}, imgBytes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"item": appended})
	})

	mux.HandleFunc("POST /internal/assistant/resources/{resourceID}/withdraw", func(w http.ResponseWriter, r *http.Request) {
		orgID := strings.TrimSpace(r.URL.Query().Get("org_id"))
		if orgID == "" {
			orgID = "org_verdantflare"
		}
		updated, err := resStore.WithdrawCreativeResource(r.Context(), orgID, r.PathValue("resourceID"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"resource": updated})
	})
}

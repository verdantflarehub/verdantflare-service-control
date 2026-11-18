package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/config"
	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/httpapi"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

func skillPatchPayload() map[string]any {
	return map[string]any{
		"name": "图像与角色", "summary": "面向创作的图像与角色能力组合", "description": "先浏览资料，待制品验收后再开放下载。",
		"category": "图像创作", "sortOrder": 10, "visibility": "listed",
	}
}

func TestSkillCatalogDemoPublicProjection(t *testing.T) {
	service := control.NewService(store.NewMemorySkillCatalogDemo())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", DevLoginSubject: "00000000-0000-4000-8000-000000000001"}, service, logger))
	t.Cleanup(server.Close)
	response := request(t, server, http.MethodGet, "/api/control/market/skills", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("demo status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	var payload struct {
		Items []domain.SkillCatalogEntry `json:"items"`
	}
	decode(t, response, &payload)
	response.Body.Close()
	if len(payload.Items) != 16 {
		t.Fatalf("demo entries=%d, want 16", len(payload.Items))
	}
	for _, entry := range payload.Items {
		if !entry.Sample || entry.Distribution != "unavailable" || entry.PublishedReleaseID != nil || entry.Visibility != "listed" {
			t.Fatalf("unsafe demo entry: %+v", entry)
		}
	}
	denied := request(t, server, http.MethodGet, "/api/control/ops/v2/skills", nil)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("demo ops status=%d body=%s", denied.StatusCode, readBody(t, denied))
	}
	denied.Body.Close()
}

func patchSkill(t *testing.T, base, id, revision string, payload any, subject string) *http.Response {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPatch, base+"/api/control/ops/v2/skills/"+id, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if revision != "" {
		req.Header.Set("If-Match", revision)
	}
	if subject != "" {
		req.Header.Set("X-VF-Login-Subject", subject)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestSkillCatalogDirectoryAndRevision(t *testing.T) {
	server := newTestServer(t, testConfig())
	managed := request(t, server, http.MethodGet, "/api/control/ops/v2/skills", nil)
	if managed.StatusCode != http.StatusOK {
		t.Fatalf("ops status=%d body=%s", managed.StatusCode, readBody(t, managed))
	}
	var list struct {
		Items []domain.SkillCatalogEntry `json:"items"`
	}
	decode(t, managed, &list)
	managed.Body.Close()
	if len(list.Items) != 16 {
		t.Fatalf("entries=%d, want 16", len(list.Items))
	}
	for id, want := range map[string]int{"B01": 3, "B02": 3, "B03": 4, "B04": 1, "B05": 3} {
		found := false
		for _, entry := range list.Items {
			if entry.EntryID == id {
				found = true
				if len(entry.PreviewSkillIDs) != want {
					t.Fatalf("%s preview skills=%v", id, entry.PreviewSkillIDs)
				}
			}
		}
		if !found {
			t.Fatalf("missing bundle %s", id)
		}
	}
	for _, entry := range list.Items {
		if entry.Visibility != "draft" || !entry.Sample || entry.Distribution != "unavailable" || entry.PublishedReleaseID != nil {
			t.Fatalf("entry claims distribution: %+v", entry)
		}
	}

	public := request(t, server, http.MethodGet, "/api/control/market/skills", nil)
	var before struct {
		Items []domain.SkillCatalogEntry `json:"items"`
	}
	decode(t, public, &before)
	public.Body.Close()
	if len(before.Items) != 0 {
		t.Fatalf("draft visible: %+v", before.Items)
	}
	privateDetail := request(t, server, http.MethodGet, "/api/control/market/skills/B01", nil)
	if privateDetail.StatusCode != http.StatusNotFound {
		t.Fatalf("draft detail status=%d", privateDetail.StatusCode)
	}
	privateDetail.Body.Close()

	missingRevision := patchSkill(t, server.URL, "B01", "", skillPatchPayload(), "")
	if missingRevision.StatusCode != 428 {
		t.Fatalf("missing revision status=%d body=%s", missingRevision.StatusCode, readBody(t, missingRevision))
	}
	missingRevision.Body.Close()

	updated := patchSkill(t, server.URL, "B01", `"skill:B01:1"`, skillPatchPayload(), "")
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", updated.StatusCode, readBody(t, updated))
	}
	var entry domain.SkillCatalogEntry
	decode(t, updated, &entry)
	updated.Body.Close()
	if entry.Revision != 2 || entry.Visibility != "listed" || !entry.Sample || entry.Distribution != "unavailable" || updated.Header.Get("ETag") != `"skill:B01:2"` {
		t.Fatalf("wrong updated entry %+v ETag=%s", entry, updated.Header.Get("ETag"))
	}

	stale := patchSkill(t, server.URL, "B01", `"skill:B01:1"`, skillPatchPayload(), "")
	if stale.StatusCode != http.StatusConflict {
		t.Fatalf("stale status=%d body=%s", stale.StatusCode, readBody(t, stale))
	}
	stale.Body.Close()

	public = request(t, server, http.MethodGet, "/api/control/market/skills", nil)
	var after struct {
		Items []domain.SkillCatalogEntry `json:"items"`
	}
	decode(t, public, &after)
	public.Body.Close()
	if len(after.Items) != 1 || after.Items[0].EntryID != "B01" || after.Items[0].PublishedReleaseID != nil {
		t.Fatalf("public projection=%+v", after.Items)
	}
	publicDetail := request(t, server, http.MethodGet, "/api/control/market/skills/B01", nil)
	if publicDetail.StatusCode != http.StatusOK {
		t.Fatalf("listed detail status=%d", publicDetail.StatusCode)
	}
	var publicEntry domain.SkillCatalogEntry
	decode(t, publicDetail, &publicEntry)
	publicDetail.Body.Close()
	if publicEntry.Distribution != "unavailable" || publicEntry.PublishedReleaseID != nil {
		t.Fatalf("listed detail claims package: %+v", publicEntry)
	}
	audit := request(t, server, http.MethodGet, "/api/control/ops/v2/skills/B01/audit", nil)
	var events struct {
		Items []domain.SkillCatalogAuditEvent `json:"items"`
	}
	decode(t, audit, &events)
	audit.Body.Close()
	if len(events.Items) != 1 || events.Items[0].Action != "update" || events.Items[0].Revision != 2 {
		t.Fatalf("audit=%+v", events.Items)
	}
}

func TestSkillCatalogGuestAndRoleBoundary(t *testing.T) {
	cfg := config.Config{Environment: "test", TrustAuthHeaders: true}
	server := newTestServer(t, cfg)
	guestReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/control/market/skills", nil)
	if err != nil {
		t.Fatal(err)
	}
	guestReq.Header.Set("X-VF-Login-Subject", "new-unbound-user")
	guest, err := http.DefaultClient.Do(guestReq)
	if err != nil {
		t.Fatal(err)
	}
	if guest.StatusCode != http.StatusOK {
		t.Fatalf("guest status=%d body=%s", guest.StatusCode, readBody(t, guest))
	}
	guest.Body.Close()

	denied := patchSkill(t, server.URL, "B01", `"skill:B01:1"`, skillPatchPayload(), "new-unbound-user")
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("guest write status=%d body=%s", denied.StatusCode, readBody(t, denied))
	}
	denied.Body.Close()

	admin := patchSkill(t, server.URL, "B01", `"skill:B01:1"`, skillPatchPayload(), "logto_01vf9k2")
	if admin.StatusCode != http.StatusOK {
		t.Fatalf("admin status=%d body=%s", admin.StatusCode, readBody(t, admin))
	}
	admin.Body.Close()
}

func TestSkillRoleRecheckedAfterOrganizationSwitch(t *testing.T) {
	server := newTestServer(t, testConfig())
	switchResponse := request(t, server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_northshore"})
	if switchResponse.StatusCode != http.StatusOK {
		t.Fatalf("switch status=%d", switchResponse.StatusCode)
	}
	switchResponse.Body.Close()
	denied := request(t, server, http.MethodGet, "/api/control/ops/v2/skills", nil)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("ops after switch status=%d body=%s", denied.StatusCode, readBody(t, denied))
	}
	denied.Body.Close()
	public := request(t, server, http.MethodGet, "/api/control/market/skills", nil)
	if public.StatusCode != http.StatusOK {
		t.Fatalf("public after switch status=%d", public.StatusCode)
	}
	public.Body.Close()
}

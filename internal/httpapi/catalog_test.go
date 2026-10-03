package httpapi_test

import (
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

func TestPublicCatalogUsesOnlyExplicitlyPublishedDatabaseRecords(t *testing.T) {
	repository := store.NewMemoryBootstrap()
	service := control.NewService(repository)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", DevLoginSubject: "00000000-0000-4000-8000-000000000001"}, service, logger))
	defer server.Close()

	initial := request(t, server, http.MethodGet, "/api/control/public/catalog", nil)
	var empty domain.PublicCatalog
	decode(t, initial, &empty)
	initial.Body.Close()
	if len(empty.Models) != 0 || len(empty.Apps) != 0 {
		t.Fatal("bootstrap catalog must be empty")
	}

	input := domain.PublicModel{ID: "verified-model", Name: "Verified Model", Provider: "Provider", Summary: "Public summary", Categories: []string{"文本生成"}, InputPrice: "12", PriceUnit: "点/百万 tokens", PublicVisible: true}
	created := request(t, server, http.MethodPost, "/api/control/ops/models", input)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create model: %d %s", created.StatusCode, readBody(t, created))
	}
	var draft domain.PublicModel
	decode(t, created, &draft)
	created.Body.Close()
	if draft.PublicVisible {
		t.Fatal("new model must be a private draft")
	}

	listed := request(t, server, http.MethodGet, "/api/control/public/catalog", nil)
	decode(t, listed, &empty)
	listed.Body.Close()
	if len(empty.Models) != 0 {
		t.Fatal("draft leaked to public catalog")
	}

	updated := request(t, server, http.MethodPatch, "/api/control/ops/models/verified-model", input)
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("publish model: %d %s", updated.StatusCode, readBody(t, updated))
	}
	updated.Body.Close()
	public := request(t, server, http.MethodGet, "/api/control/public/catalog", nil)
	var catalog domain.PublicCatalog
	decode(t, public, &catalog)
	public.Body.Close()
	if len(catalog.Models) != 1 || catalog.Models[0].InputPrice != "12" {
		t.Fatalf("unexpected public model: %+v", catalog.Models)
	}

	app := request(t, server, http.MethodPost, "/api/control/ops/apps", map[string]any{"id": "real-app", "name": "Real App", "version": "1.0.0", "publicVisible": true})
	if app.StatusCode != http.StatusCreated {
		t.Fatalf("create app: %d %s", app.StatusCode, readBody(t, app))
	}
	app.Body.Close()
	preview := request(t, server, http.MethodPatch, "/api/control/ops/apps/real-app", map[string]any{"name": "Real App", "version": "1.0.1", "channel": "Preview", "summary": "Approved description", "publicVisible": true})
	if preview.StatusCode != http.StatusOK {
		t.Fatalf("publish app: %d %s", preview.StatusCode, readBody(t, preview))
	}
	preview.Body.Close()
	public = request(t, server, http.MethodGet, "/api/control/public/catalog", nil)
	decode(t, public, &catalog)
	public.Body.Close()
	if len(catalog.Apps) != 1 || catalog.Apps[0].Version != "1.0.1" {
		t.Fatalf("unexpected public app: %+v", catalog.Apps)
	}
}

func TestModelAdminRequiresRoleButPublicReadDoesNot(t *testing.T) {
	server := newTestServer(t, config.Config{})
	public := request(t, server, http.MethodGet, "/api/control/public/catalog", nil)
	defer public.Body.Close()
	if public.StatusCode != http.StatusOK {
		t.Fatalf("public status = %d", public.StatusCode)
	}
	private := request(t, server, http.MethodGet, "/api/control/ops/models", nil)
	defer private.Body.Close()
	if private.StatusCode != http.StatusUnauthorized {
		t.Fatalf("private status = %d", private.StatusCode)
	}
}

func TestModelAdminRoleRecheckedAfterOrganizationSwitch(t *testing.T) {
	server := newTestServer(t, testConfig())
	switchResponse := request(t, server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_northshore"})
	if switchResponse.StatusCode != http.StatusOK {
		t.Fatalf("switch status = %d", switchResponse.StatusCode)
	}
	switchResponse.Body.Close()
	private := request(t, server, http.MethodGet, "/api/control/ops/models", nil)
	defer private.Body.Close()
	if private.StatusCode != http.StatusForbidden {
		t.Fatalf("private status = %d, want 403", private.StatusCode)
	}
}

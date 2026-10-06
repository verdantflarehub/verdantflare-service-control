package httpapi_test

import (
	"context"
	"errors"
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

type catalogGateway struct {
	models     map[string]struct{}
	chatModels map[string]struct{}
	err        error
}

func (g *catalogGateway) ListModels(context.Context) (map[string]struct{}, error) {
	return g.models, g.err
}
func (g *catalogGateway) ListChatModels(context.Context) (map[string]struct{}, error) {
	return g.chatModels, g.err
}

func TestPublicCatalogUsesOnlyExplicitlyPublishedDatabaseRecords(t *testing.T) {
	repository := store.NewMemoryBootstrap()
	service := control.NewService(repository, &catalogGateway{models: map[string]struct{}{"verified-model": {}}})
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

func TestListedAppAppearsInHubWithoutVersionOrInstallEntitlement(t *testing.T) {
	repository := store.NewMemoryBootstrap()
	service := control.NewService(repository)
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", DevLoginSubject: "00000000-0000-4000-8000-000000000001"}, service, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	created := request(t, server, http.MethodPost, "/api/control/ops/apps", map[string]any{
		"id": "blender-mcp", "name": "Blender MCP", "category": "三维创作", "summary": "Blender 场景与动画工具",
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", created.StatusCode, readBody(t, created))
	}
	created.Body.Close()
	listed := request(t, server, http.MethodPatch, "/api/control/ops/apps/blender-mcp", map[string]any{
		"name": "Blender MCP", "category": "三维创作", "summary": "Blender 场景与动画工具", "channel": "Listed",
	})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list in Hub: %d %s", listed.StatusCode, readBody(t, listed))
	}
	listed.Body.Close()

	market := request(t, server, http.MethodGet, "/api/control/market/apps", nil)
	var apps []domain.App
	decode(t, market, &apps)
	market.Body.Close()
	if len(apps) != 1 || apps[0].ID != "blender-mcp" || apps[0].Version != "" || apps[0].Entitled || apps[0].Channel != "Listed" {
		t.Fatalf("unexpected Hub listing: %+v", apps)
	}

	public := request(t, server, http.MethodGet, "/api/control/public/catalog", nil)
	var catalog domain.PublicCatalog
	decode(t, public, &catalog)
	public.Body.Close()
	if len(catalog.Apps) != 0 {
		t.Fatalf("Hub-only listing leaked into WWW catalog: %+v", catalog.Apps)
	}

	invalid := request(t, server, http.MethodPatch, "/api/control/ops/apps/blender-mcp", map[string]any{
		"name": "Blender MCP", "category": "三维创作", "summary": "Blender 场景与动画工具", "channel": "Listed", "version": "0.1.0",
	})
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("versioned listing status = %d, want 400", invalid.StatusCode)
	}
	invalid.Body.Close()

	invalid = request(t, server, http.MethodPatch, "/api/control/ops/apps/blender-mcp", map[string]any{
		"name": "Blender MCP", "category": "三维创作", "summary": "Blender 场景与动画工具", "channel": "Listed", "publicVisible": true,
	})
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("WWW listing status = %d, want 400", invalid.StatusCode)
	}
	invalid.Body.Close()
}

func TestPublishedModelsFollowGatewayForWWWAndHub(t *testing.T) {
	gateway := &catalogGateway{models: map[string]struct{}{
		"verdantflare-sd2": {}, "deepseek-flash": {}, "deepseek-v4-pro": {},
	}, chatModels: map[string]struct{}{"deepseek-flash": {}, "deepseek-v4-pro": {}}}
	service := control.NewService(store.NewMemoryBootstrap(), gateway)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", DevLoginSubject: "00000000-0000-4000-8000-000000000001"}, service, logger))
	defer server.Close()

	for _, id := range []string{"verdantflare-sd2", "deepseek-flash", "deepseek-v4-pro"} {
		input := domain.PublicModel{ID: id, Name: "Model " + id, Provider: "Verified", Summary: "Reviewed description", Categories: []string{"文本生成"}}
		created := request(t, server, http.MethodPost, "/api/control/ops/models", input)
		if created.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: %d %s", id, created.StatusCode, readBody(t, created))
		}
		created.Body.Close()
		input.PublicVisible = true
		published := request(t, server, http.MethodPatch, "/api/control/ops/models/"+id, input)
		if published.StatusCode != http.StatusOK {
			t.Fatalf("publish %s: %d %s", id, published.StatusCode, readBody(t, published))
		}
		published.Body.Close()
	}
	video := domain.PublicModel{ID: "verdantflare-sd2", Name: "Model verdantflare-sd2", Provider: "Verified", Summary: "Reviewed description", Categories: []string{"视频生成"}, PublicVisible: true, ExperienceMode: "chat"}
	invalidExperience := request(t, server, http.MethodPatch, "/api/control/ops/models/verdantflare-sd2", video)
	if invalidExperience.StatusCode != http.StatusConflict {
		t.Fatalf("video model was opened as chat experience: %d %s", invalidExperience.StatusCode, readBody(t, invalidExperience))
	}
	invalidExperience.Body.Close()

	public := request(t, server, http.MethodGet, "/api/control/public/catalog", nil)
	var catalog domain.PublicCatalog
	decode(t, public, &catalog)
	public.Body.Close()
	if len(catalog.Models) != 3 {
		t.Fatalf("WWW models = %d, want 3", len(catalog.Models))
	}
	hub := request(t, server, http.MethodGet, "/api/control/api/models", nil)
	var hubModels []domain.Model
	decode(t, hub, &hubModels)
	hub.Body.Close()
	if len(hubModels) != 3 {
		t.Fatalf("Hub models = %d, want 3", len(hubModels))
	}

	delete(gateway.models, "deepseek-v4-pro")
	public = request(t, server, http.MethodGet, "/api/control/public/catalog", nil)
	decode(t, public, &catalog)
	public.Body.Close()
	if len(catalog.Models) != 2 {
		t.Fatalf("WWW should hide withdrawn model, got %d", len(catalog.Models))
	}
	hub = request(t, server, http.MethodGet, "/api/control/api/models", nil)
	decode(t, hub, &hubModels)
	hub.Body.Close()
	if len(hubModels) != 2 {
		t.Fatalf("Hub should hide withdrawn model, got %d", len(hubModels))
	}

	gateway.err = errors.New("upstream unavailable")
	for _, path := range []string{"/api/control/public/catalog", "/api/control/api/models"} {
		response := request(t, server, http.MethodGet, path, nil)
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("%s must fail closed, got %d", path, response.StatusCode)
		}
		response.Body.Close()
	}
}

func TestCannotPublishModelAbsentFromGateway(t *testing.T) {
	service := control.NewService(store.NewMemoryBootstrap(), &catalogGateway{models: map[string]struct{}{}})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", DevLoginSubject: "00000000-0000-4000-8000-000000000001"}, service, logger))
	defer server.Close()
	input := domain.PublicModel{ID: "not-in-gateway", Name: "Missing Model", Provider: "Provider", Summary: "Description", Categories: []string{"文本生成"}}
	created := request(t, server, http.MethodPost, "/api/control/ops/models", input)
	created.Body.Close()
	input.PublicVisible = true
	response := request(t, server, http.MethodPatch, "/api/control/ops/models/not-in-gateway", input)
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("publish missing model = %d, want 409", response.StatusCode)
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
	gateway := request(t, server, http.MethodGet, "/api/control/ops/gateway-models", nil)
	defer gateway.Body.Close()
	if gateway.StatusCode != http.StatusUnauthorized {
		t.Fatalf("gateway status = %d", gateway.StatusCode)
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
	gateway := request(t, server, http.MethodGet, "/api/control/ops/gateway-models", nil)
	defer gateway.Body.Close()
	if gateway.StatusCode != http.StatusForbidden {
		t.Fatalf("gateway status = %d, want 403", gateway.StatusCode)
	}
}

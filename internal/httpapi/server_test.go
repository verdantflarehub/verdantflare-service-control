package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/config"
	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/httpapi"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

func TestContextRequiresAuthentication(t *testing.T) {
	server := newTestServer(t, config.Config{})
	response := request(t, server, http.MethodGet, "/api/control/context", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}

func TestContextMatchesHubContract(t *testing.T) {
	server := newTestServer(t, testConfig())
	response := request(t, server, http.MethodGet, "/api/control/context", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var context domain.CenterContext
	decode(t, response, &context)
	if context.CenterUserID != "cu_01HUB7C9Q" || context.ActiveOrganizationID != "org_verdantflare" {
		t.Fatalf("unexpected context: %+v", context)
	}
	if len(context.Organizations) != 2 || len(context.Organizations[0].Roles) == 0 {
		t.Fatalf("organization memberships missing: %+v", context.Organizations)
	}
}

func TestOverviewAggregatesCurrentOrganization(t *testing.T) {
	server := newTestServer(t, testConfig())
	response := request(t, server, http.MethodGet, "/api/control/overview", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var overview domain.Overview
	decode(t, response, &overview)
	if overview.AvailableApps != 6 || overview.RunningSessions != 1 || overview.Organization.OrganizationID != "org_verdantflare" {
		t.Fatalf("unexpected overview: %+v", overview)
	}
}

func TestExperienceSessionLifecycle(t *testing.T) {
	server := newTestServer(t, testConfig())
	createdResponse := request(t, server, http.MethodPost, "/api/control/experience/sessions", map[string]any{"appId": "wan-video", "region": "cn-east-1"})
	defer createdResponse.Body.Close()
	if createdResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createdResponse.StatusCode, readBody(t, createdResponse))
	}
	var created domain.ExperienceSession
	decode(t, createdResponse, &created)
	if created.Status != "运行中" || created.AppID != "wan-video" {
		t.Fatalf("unexpected session: %+v", created)
	}

	closeResponse := request(t, server, http.MethodDelete, "/api/control/experience/sessions/"+created.ID, nil)
	defer closeResponse.Body.Close()
	if closeResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("close status = %d, body = %s", closeResponse.StatusCode, readBody(t, closeResponse))
	}
}

func TestAPIKeySecretReturnedOnce(t *testing.T) {
	server := newTestServer(t, testConfig())
	createdResponse := request(t, server, http.MethodPost, "/api/control/api-keys", map[string]any{"name": "CI 测试", "scopes": []string{"models:read", "tasks:write"}})
	defer createdResponse.Body.Close()
	if createdResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createdResponse.StatusCode, readBody(t, createdResponse))
	}
	var created control.CreateAPIKeyResult
	decode(t, createdResponse, &created)
	if !strings.HasPrefix(created.Secret, "vf_live_") {
		t.Fatalf("secret = %q", created.Secret)
	}

	listResponse := request(t, server, http.MethodGet, "/api/control/api-keys", nil)
	defer listResponse.Body.Close()
	body := readBody(t, listResponse)
	if strings.Contains(body, created.Secret) {
		t.Fatal("API key list exposed the full secret")
	}
	if !strings.Contains(body, "••••••••") {
		t.Fatalf("redacted prefix missing: %s", body)
	}
}

func TestOperationsRoleRecheckedAfterOrganizationSwitch(t *testing.T) {
	server := newTestServer(t, testConfig())
	switchResponse := request(t, server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_northshore"})
	defer switchResponse.Body.Close()
	if switchResponse.StatusCode != http.StatusOK {
		t.Fatalf("switch status = %d, body = %s", switchResponse.StatusCode, readBody(t, switchResponse))
	}

	response := request(t, server, http.MethodGet, "/api/control/ops/releases", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusForbidden)
	}
}

func TestUnknownJSONFieldRejected(t *testing.T) {
	server := newTestServer(t, testConfig())
	response := request(t, server, http.MethodPost, "/api/control/experience/sessions", map[string]any{"appId": "wan-video", "region": "cn-east-1", "admin": true})
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}

func newTestServer(t *testing.T, configuration config.Config) *httptest.Server {
	t.Helper()
	repository := store.NewMemorySeeded(time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC))
	service := control.NewService(repository)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.New(configuration, service, logger))
	t.Cleanup(server.Close)
	return server
}

func testConfig() config.Config {
	return config.Config{Environment: "test", DevLoginSubject: "logto_01vf9k2"}
}

func request(t *testing.T, server *httptest.Server, method, path string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequest(method, server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decode(t *testing.T, response *http.Response, target any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

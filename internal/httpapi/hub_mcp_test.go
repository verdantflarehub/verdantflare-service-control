package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func issueMCPToken(t *testing.T, server *httptest.Server) (string, string) {
	t.Helper()
	response := request(t, server, http.MethodPost, "/api/control/settings/mcp-tokens", map[string]any{"name": "AI 发布工具", "expiresInDays": 30})
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("issue token status = %d", response.StatusCode)
	}
	var created struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Secret, "vfhub_") || created.ID == "" {
		t.Fatalf("unexpected token: %+v", created)
	}
	return created.ID, created.Secret
}

func hubMCPPost(t *testing.T, server *httptest.Server, token string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/control/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func hubMCPCall(t *testing.T, server *httptest.Server, token, name string, arguments any) (bool, json.RawMessage) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": name, "method": "tools/call", "params": map[string]any{"name": name, "arguments": arguments}})
	if err != nil {
		t.Fatal(err)
	}
	response := hubMCPPost(t, server, token, body)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s HTTP status = %d", name, response.StatusCode)
	}
	var envelope struct {
		Result struct {
			IsError           bool            `json:"isError"`
			StructuredContent json.RawMessage `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Result.IsError, envelope.Result.StructuredContent
}

func TestHubMCPRequiresValidToken(t *testing.T) {
	server := newTestServer(t, testConfig())
	list := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	for _, token := range []string{"", "vfhub_not-a-real-token"} {
		response := hubMCPPost(t, server, token, list)
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized || response.Header.Get("WWW-Authenticate") == "" {
			t.Fatalf("token %q status = %d", token, response.StatusCode)
		}
	}

	tokenID, token := issueMCPToken(t, server)
	initialize := hubMCPPost(t, server, token, []byte(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`))
	io.Copy(io.Discard, initialize.Body)
	initialize.Body.Close()
	if initialize.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d", initialize.StatusCode)
	}
	response := hubMCPPost(t, server, token, list)
	var tools struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&tools); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(tools.Result.Tools) != 10 {
		t.Fatalf("tools = %+v", tools.Result.Tools)
	}

	// Switching to an organization without app_ops_admin takes effect at once.
	switched := request(t, server, http.MethodPut, "/api/control/context/active-organization", map[string]any{"organizationId": "org_northshore"})
	switched.Body.Close()
	forbidden := hubMCPPost(t, server, token, list)
	io.Copy(io.Discard, forbidden.Body)
	forbidden.Body.Close()
	if forbidden.StatusCode != http.StatusForbidden {
		t.Fatalf("token without app_ops_admin status = %d", forbidden.StatusCode)
	}
	restored := request(t, server, http.MethodPut, "/api/control/context/active-organization", map[string]any{"organizationId": "org_verdantflare"})
	restored.Body.Close()

	revoked := request(t, server, http.MethodDelete, "/api/control/settings/mcp-tokens/"+tokenID, nil)
	revoked.Body.Close()
	if revoked.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke status = %d", revoked.StatusCode)
	}
	afterRevoke := hubMCPPost(t, server, token, list)
	io.Copy(io.Discard, afterRevoke.Body)
	afterRevoke.Body.Close()
	if afterRevoke.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token status = %d", afterRevoke.StatusCode)
	}
}

func TestHubMCPBodyLimit(t *testing.T) {
	server := newTestServer(t, testConfig())
	_, token := issueMCPToken(t, server)
	for _, tc := range []struct{ size, want int }{{2 << 20, http.StatusOK}, {6 << 20, http.StatusRequestEntityTooLarge}} {
		body := []byte(`{"jsonrpc":"2.0","id":"large","method":"tools/call","params":{"name":"app_publish.list","arguments":{"padding":"` + strings.Repeat("x", tc.size) + `"}}}`)
		response := hubMCPPost(t, server, token, body)
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != tc.want {
			t.Fatalf("%d-byte request status = %d, want %d", len(body), response.StatusCode, tc.want)
		}
	}
}

func TestHubMCPPublishRecoversThenManagesLifecycle(t *testing.T) {
	artifact := makeTestOCIArtifact(t, "publication-test", "1.0.0", "1.0.0")
	server := newTestServerWithOCIRegistryConfig(t, testConfig(),
		map[string]testOCIRegistryArtifact{"publication-test:1.0.0": artifact, "publication-test:" + artifact.ManifestDigest: artifact},
		map[string][]byte{"publication-test:" + artifact.PackageDigest: artifact.PackageBytes})
	_, token := issueMCPToken(t, server)
	payload := map[string]any{
		"appId": "publication-test", "name": "Publication Test", "groupId": "desktop",
		"category": "Tool", "summary": "Fixed OCI publishing", "description": "Fixed OCI publishing integration test", "developer": "VerdantFlare",
		"showcase": map[string]any{"screenshots": []string{"https://example.test/screen.png"}, "highlights": []string{"Publishes a fixed chart"}},
		"version": map[string]any{
			"version": "1.0.0", "sourceRevision": "test-revision", "licenseId": "MIT", "licenseUrl": "https://example.test/license",
			"chart": map[string]any{"ociRef": "oci://registry.verdantflare.ai/charts/publication-test", "version": "1.0.0", "manifestDigest": artifact.ManifestDigest, "packageDigest": "sha256:" + strings.Repeat("a", 64)},
		},
		"organizationIds": []string{"org_verdantflare"}, "expectedRevision": 0,
	}
	failed, raw := hubMCPCall(t, server, token, "app_publish.publish", payload)
	var failure struct {
		Status int    `json:"status"`
		Code   string `json:"code"`
	}
	if err := json.Unmarshal(raw, &failure); err != nil {
		t.Fatal(err)
	}
	if !failed || failure.Status != http.StatusBadGateway || failure.Code != "CHART_DIGEST_MISMATCH" {
		t.Fatalf("mismatched artifact result: %s", raw)
	}

	revision := func() int {
		t.Helper()
		failed, raw := hubMCPCall(t, server, token, "app_publish.get", map[string]any{"app_id": "publication-test"})
		var detail struct {
			App struct {
				Revision int `json:"revision"`
			} `json:"app"`
		}
		if err := json.Unmarshal(raw, &detail); failed || err != nil || detail.App.Revision < 1 {
			t.Fatalf("get failed: %s", raw)
		}
		return detail.App.Revision
	}
	payload["expectedRevision"] = revision()
	payload["version"].(map[string]any)["chart"].(map[string]any)["packageDigest"] = artifact.PackageDigest
	if failed, raw = hubMCPCall(t, server, token, "app_publish.publish", payload); failed {
		t.Fatalf("fixed OCI retry failed: %s", raw)
	}
	inCatalog := func() bool {
		t.Helper()
		_, raw := hubMCPCall(t, server, token, "app_publish.list", map[string]any{})
		var list struct {
			CatalogApps []struct {
				AppID string `json:"appId"`
			} `json:"catalogApps"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			t.Fatal(err)
		}
		for _, item := range list.CatalogApps {
			if item.AppID == "publication-test" {
				return true
			}
		}
		return false
	}
	if !inCatalog() {
		t.Fatal("published app missing from Station catalog")
	}

	if failed, raw = hubMCPCall(t, server, token, "app_publish.update", map[string]any{"appId": "publication-test", "expectedRevision": revision() - 1, "summary": "stale"}); !failed || !strings.Contains(string(raw), "409") {
		t.Fatalf("stale update not rejected: %s", raw)
	}
	if failed, raw = hubMCPCall(t, server, token, "app_publish.update", map[string]any{"appId": "publication-test", "expectedRevision": revision(), "summary": "Updated summary"}); failed || !strings.Contains(string(raw), "Updated summary") {
		t.Fatalf("update failed: %s", raw)
	}

	if failed, raw = hubMCPCall(t, server, token, "app_publish.disable", map[string]any{"appId": "publication-test", "expectedRevision": revision()}); !failed || !strings.Contains(string(raw), "DISABLE_REASON_REQUIRED") {
		t.Fatalf("disable without reason accepted: %s", raw)
	}
	if failed, raw = hubMCPCall(t, server, token, "app_publish.disable", map[string]any{"appId": "publication-test", "expectedRevision": revision(), "reason": "维护中"}); failed || inCatalog() {
		t.Fatalf("disable failed or app still in catalog: %s", raw)
	}
	if failed, raw = hubMCPCall(t, server, token, "app_publish.enable", map[string]any{"appId": "publication-test", "expectedRevision": revision()}); failed || !inCatalog() {
		t.Fatalf("enable failed or app missing from catalog: %s", raw)
	}

	if failed, raw = hubMCPCall(t, server, token, "app_publish.revoke_organizations", map[string]any{"appId": "publication-test", "organizationIds": []string{"org_verdantflare"}}); failed {
		t.Fatalf("revoke failed: %s", raw)
	}
	if failed, raw = hubMCPCall(t, server, token, "app_publish.grant_organizations", map[string]any{"appId": "publication-test", "organizationIds": []string{"org_verdantflare"}}); failed {
		t.Fatalf("grant failed: %s", raw)
	}
	if failed, raw = hubMCPCall(t, server, token, "app_publish.grant_organizations", map[string]any{"appId": "publication-test", "organizationIds": []string{}}); !failed {
		t.Fatalf("empty grant accepted: %s", raw)
	}
}

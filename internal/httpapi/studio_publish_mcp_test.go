package httpapi_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/config"
)

func TestStudioPublicationDelegationRejectsUnsignedAndReplay(t *testing.T) {
	key := "test-only-key-with-at-least-thirty-two-characters"
	cfg := testConfig()
	cfg.StudioPublishHMACKey = key
	cfg.StudioPublishBindings = []config.StudioPublishBinding{{StationUserID: "station-user", StationOrganizationID: "station-org", LoginSubject: "logto_01vf9k2", ControlOrganizationID: "org_verdantflare"}}
	server := newTestServer(t, cfg)
	path := "/api/control/internal/studio-publish/mcp"
	requestBody := []byte(`{"jsonrpc":"2.0","id":"request-1","method":"tools/call","params":{"name":"app_publish.list","arguments":{}}}`)
	unsigned, err := http.Post(server.URL+path, "application/json", bytes.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, unsigned.Body)
	unsigned.Body.Close()
	if unsigned.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned status = %d", unsigned.StatusCode)
	}

	nonce := "0123456789abcdef0123456789abcdef"
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	digest := sha256.Sum256(requestBody)
	message := fmt.Sprintf("POST\n%s\n%s\n%s\nstation-user\nstation-org\n%x", path, timestamp, nonce, digest)
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(message))
	call := func() *http.Response {
		req, err := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(requestBody))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-App-Publish-User", "station-user")
		req.Header.Set("X-App-Publish-Organization", "station-org")
		req.Header.Set("X-App-Publish-Timestamp", timestamp)
		req.Header.Set("X-App-Publish-Nonce", nonce)
		req.Header.Set("X-App-Publish-Signature", hex.EncodeToString(mac.Sum(nil)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	first := call()
	var result struct {
		Result struct {
			StructuredContent struct {
				Items       []json.RawMessage `json:"items"`
				CatalogApps []json.RawMessage `json:"catalogApps"`
				CatalogETag string            `json:"catalogETag"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.NewDecoder(first.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("signed status = %d", first.StatusCode)
	}
	if result.Result.StructuredContent.Items == nil || result.Result.StructuredContent.CatalogApps == nil || result.Result.StructuredContent.CatalogETag == "" {
		t.Fatal("signed list did not return managed apps and Station catalog snapshot")
	}
	replayed := call()
	io.Copy(io.Discard, replayed.Body)
	replayed.Body.Close()
	if replayed.StatusCode != http.StatusConflict {
		t.Fatalf("replayed status = %d", replayed.StatusCode)
	}
}

func signedPublicationCall(t *testing.T, server *httptest.Server, key, nonce, name string, arguments any) (bool, json.RawMessage) {
	t.Helper()
	path := "/api/control/internal/studio-publish/mcp"
	requestBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": nonce, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": arguments},
	})
	if err != nil {
		t.Fatal(err)
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	digest := sha256.Sum256(requestBody)
	message := fmt.Sprintf("POST\n%s\n%s\n%s\nstation-user\nstation-org\n%x", path, timestamp, nonce, digest)
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(message))
	req, err := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-App-Publish-User", "station-user")
	req.Header.Set("X-App-Publish-Organization", "station-org")
	req.Header.Set("X-App-Publish-Timestamp", timestamp)
	req.Header.Set("X-App-Publish-Nonce", nonce)
	req.Header.Set("X-App-Publish-Signature", hex.EncodeToString(mac.Sum(nil)))
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("MCP HTTP status = %d", response.StatusCode)
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

func TestStudioPublicationFixedOCIRecoversAfterRegistryMismatch(t *testing.T) {
	key := "test-only-key-with-at-least-thirty-two-characters"
	cfg := testConfig()
	cfg.StudioPublishHMACKey = key
	cfg.StudioPublishBindings = []config.StudioPublishBinding{{StationUserID: "station-user", StationOrganizationID: "station-org", LoginSubject: "logto_01vf9k2", ControlOrganizationID: "org_verdantflare"}}
	artifact := makeTestOCIArtifact(t, "publication-test", "1.0.0", "1.0.0")
	server := newTestServerWithOCIRegistryConfig(t, cfg,
		map[string]testOCIRegistryArtifact{"publication-test:1.0.0": artifact, "publication-test:" + artifact.ManifestDigest: artifact},
		map[string][]byte{"publication-test:" + artifact.PackageDigest: artifact.PackageBytes})
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
	failed, raw := signedPublicationCall(t, server, key, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "app_publish.publish", payload)
	if !failed {
		t.Fatalf("mismatched artifact unexpectedly published: %s", raw)
	}
	var failure struct {
		Status  int    `json:"status"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Status != http.StatusBadGateway || failure.Code != "CHART_DIGEST_MISMATCH" || failure.Message == "" {
		t.Fatalf("wrong failure classification: %+v", failure)
	}

	failed, raw = signedPublicationCall(t, server, key, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "app_publish.get", map[string]any{"app_id": "publication-test"})
	if failed {
		t.Fatalf("failed publication draft is not recoverable: %s", raw)
	}
	var detail struct {
		App struct {
			Revision int `json:"revision"`
		} `json:"app"`
	}
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.App.Revision < 1 {
		t.Fatalf("missing persisted revision: %s", raw)
	}
	payload["expectedRevision"] = detail.App.Revision
	payload["version"].(map[string]any)["chart"].(map[string]any)["packageDigest"] = artifact.PackageDigest
	failed, raw = signedPublicationCall(t, server, key, "cccccccccccccccccccccccccccccccc", "app_publish.publish", payload)
	if failed {
		t.Fatalf("fixed OCI retry failed: %s", raw)
	}
	var published struct {
		App struct {
			PublishedVersion string `json:"publishedVersion"`
		} `json:"app"`
		Version struct {
			Version string `json:"version"`
		} `json:"version"`
		CatalogETag string `json:"catalogETag"`
	}
	if err := json.Unmarshal(raw, &published); err != nil {
		t.Fatal(err)
	}
	if published.App.PublishedVersion != "1.0.0" || published.Version.Version != "1.0.0" || published.CatalogETag == "" {
		t.Fatalf("publication did not return fixed version and catalog readback: %s", raw)
	}
}

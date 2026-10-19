package httpapi_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/httpapi"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
	"gopkg.in/yaml.v3"
)

type testOCIRegistryArtifact struct {
	ManifestBytes  []byte
	ManifestDigest string
	PackageBytes   []byte
	PackageDigest  string
}

func buildTestChartArchive(t *testing.T, chartName, chartVersion, appVersion string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	files := map[string]string{
		chartName + "/Chart.yaml": fmt.Sprintf(`apiVersion: v2
name: %s
version: %s
appVersion: %q
description: "Flux 图像创作工作台 Helm Chart"
icon: file://logo.svg
annotations:
  verdantflare.ai/models: "black-forest-labs/FLUX.1-dev,black-forest-labs/FLUX.1-schnell"
  verdantflare.ai/capabilities: "text_to_image,image_to_image"
  verdantflare.ai/min-station-version: "0.4.0"
  verdantflare.ai/architectures: "linux/amd64"
`, chartName, chartVersion, appVersion),
		chartName + "/values.yaml": `image:
  repository: registry.verdantflare.ai/apps/flux-image
  tag: v0.1.3
  digest: sha256:3333333333333333333333333333333333333333333333333333333333333333
`,
		chartName + "/logo.svg": `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/></svg>`,
	}

	for name, content := range files {
		raw := []byte(content)
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(raw)),
		}); err != nil {
			t.Fatalf("write tar header %s: %v", name, err)
		}
		if _, err := tw.Write(raw); err != nil {
			t.Fatalf("write tar content %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func makeTestOCIArtifact(t *testing.T, chartName, chartVersion, appVersion string) testOCIRegistryArtifact {
	t.Helper()
	pkgBytes := buildTestChartArchive(t, chartName, chartVersion, appVersion)
	pkgSum := sha256.Sum256(pkgBytes)
	pkgDigest := "sha256:" + hex.EncodeToString(pkgSum[:])

	configBytes := []byte(`{"name":"` + chartName + `","version":"` + chartVersion + `"}`)
	cfgSum := sha256.Sum256(configBytes)
	cfgDigest := "sha256:" + hex.EncodeToString(cfgSum[:])

	manifestMap := map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{
			"mediaType": "application/vnd.cncf.helm.config.v1+json",
			"digest":    cfgDigest,
			"size":      len(configBytes),
		},
		"layers": []map[string]any{
			{
				"mediaType": "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
				"digest":    pkgDigest,
				"size":      len(pkgBytes),
			},
		},
	}
	manifestBytes, err := json.Marshal(manifestMap)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	manifestSum := sha256.Sum256(manifestBytes)
	manifestDigest := "sha256:" + hex.EncodeToString(manifestSum[:])

	return testOCIRegistryArtifact{
		ManifestBytes:  manifestBytes,
		ManifestDigest: manifestDigest,
		PackageBytes:   pkgBytes,
		PackageDigest:  pkgDigest,
	}
}

func newTestServerWithOCIRegistry(t *testing.T, artifacts map[string]testOCIRegistryArtifact, blobs map[string][]byte) *httptest.Server {
	t.Helper()
	regMux := http.NewServeMux()
	regMux.HandleFunc("GET /v2/charts/{appID}/manifests/{ref}", func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("appID") + ":" + r.PathValue("ref")
		art, ok := artifacts[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[{"code":"MANIFEST_UNKNOWN"}]}`))
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.Header().Set("Docker-Content-Digest", art.ManifestDigest)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(art.ManifestBytes)
	})
	regMux.HandleFunc("GET /v2/charts/{appID}/blobs/{digest}", func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("appID") + ":" + r.PathValue("digest")
		blob, ok := blobs[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.cncf.helm.chart.content.v1.tar+gzip")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(blob)
	})
	regServer := httptest.NewServer(regMux)
	t.Cleanup(regServer.Close)

	cfg := testConfig()
	cfg.ChartRegistryHost = "registry.verdantflare.ai"
	cfg.ChartRegistryInternalURL = regServer.URL
	service := control.NewService(store.NewMemorySeeded(time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)))
	service.SetChartRegistry("registry.verdantflare.ai", regServer.URL, regServer.Client())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpapi.New(cfg, service, logger))
	t.Cleanup(srv.Close)
	return srv
}

func requestWithHeaders(t *testing.T, server *httptest.Server, method, path string, body any, headers map[string]string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, server.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return res
}

func createTestAppV2(t *testing.T, server *httptest.Server, appID, name, groupID string) domain.App {
	t.Helper()
	res := request(t, server, http.MethodPost, "/api/control/ops/v2/apps", map[string]any{
		"appId":       appID,
		"name":        name,
		"groupId":     groupID,
		"description": name + " 应用说明",
		"sortOrder":   120,
	})
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create app %s status = %d, body = %s", appID, res.StatusCode, readBody(t, res))
	}
	var app domain.App
	decode(t, res, &app)
	return app
}

func TestMarketV2OCIChartRegistrationLifecycleAndIdempotency(t *testing.T) {
	art := makeTestOCIArtifact(t, "flux-image", "0.1.3", "0.1.3")
	mismatchArt := makeTestOCIArtifact(t, "wrong-chart", "0.1.3", "0.1.3")
	badDigestArt := makeTestOCIArtifact(t, "flux-image", "0.1.4", "0.1.4")

	artifacts := map[string]testOCIRegistryArtifact{
		"flux-image:0.1.3":                         art,
		"flux-image:" + art.ManifestDigest:         art,
		"flux-image:0.9.9":                         mismatchArt,
		"flux-image:0.1.4":                         badDigestArt,
		"flux-image:" + badDigestArt.ManifestDigest: badDigestArt,
	}
	blobs := map[string][]byte{
		"flux-image:" + art.PackageDigest:          art.PackageBytes,
		"flux-image:" + mismatchArt.PackageDigest:  mismatchArt.PackageBytes,
		"flux-image:" + badDigestArt.PackageDigest: append([]byte("corrupted"), badDigestArt.PackageBytes...),
	}
	server := newTestServerWithOCIRegistry(t, artifacts, blobs)
	createTestAppV2(t, server, "flux-image", "Flux 图像工作台", "image")

	// 1. Register valid OCI chart version -> 201 Created
	payload := map[string]any{
		"version":    "0.1.3",
		"appVersion": "0.1.3",
		"chart": map[string]any{
			"ociRef":         "oci://registry.verdantflare.ai/charts/flux-image",
			"version":        "0.1.3",
			"manifestDigest": art.ManifestDigest,
			"packageDigest":  art.PackageDigest,
		},
		"changelog": "支持 FLUX.1-dev 与安全 SVG 图标提取",
	}
	res := request(t, server, http.MethodPost, "/api/control/ops/v2/apps/flux-image/versions", payload)
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create version status = %d, body = %s", res.StatusCode, readBody(t, res))
	}
	var created domain.AppVersion
	decode(t, res, &created)
	if created.Chart == nil || created.Chart.ManifestDigest != art.ManifestDigest || created.Chart.PackageDigest != art.PackageDigest {
		t.Fatalf("unexpected chart record: %+v", created.Chart)
	}
	if len(created.Images) != 1 || created.Images[0].Component != "app" || !strings.Contains(created.Images[0].Image, "registry.verdantflare.ai/apps/flux-image") {
		t.Fatalf("expected extracted primary image, got %+v", created.Images)
	}
	if len(created.Models) != 2 || len(created.Capabilities) != 2 {
		t.Fatalf("expected extracted models and capabilities, got models=%v capabilities=%v", created.Models, created.Capabilities)
	}
	if created.Logo == nil || created.Logo.MediaType != "image/svg+xml" || created.Logo.Digest == "" {
		t.Fatalf("expected extracted SVG logo, got %+v", created.Logo)
	}

	// 2. Idempotent retry with identical payload -> 200 OK
	retryRes := request(t, server, http.MethodPost, "/api/control/ops/v2/apps/flux-image/versions", payload)
	defer retryRes.Body.Close()
	if retryRes.StatusCode != http.StatusOK {
		t.Fatalf("idempotent retry status = %d, want 200, body = %s", retryRes.StatusCode, readBody(t, retryRes))
	}

	// 3. Conflict retry with different changelog -> 409 VERSION_CONFLICT
	conflictPayload := map[string]any{
		"version":    "0.1.3",
		"appVersion": "0.1.3",
		"chart": map[string]any{
			"ociRef":         "oci://registry.verdantflare.ai/charts/flux-image",
			"version":        "0.1.3",
			"manifestDigest": art.ManifestDigest,
			"packageDigest":  art.PackageDigest,
		},
		"changelog": "篡改已有版本的变更说明",
	}
	conflictRes := request(t, server, http.MethodPost, "/api/control/ops/v2/apps/flux-image/versions", conflictPayload)
	defer conflictRes.Body.Close()
	if conflictRes.StatusCode != http.StatusConflict {
		t.Fatalf("conflict status = %d, want 409, body = %s", conflictRes.StatusCode, readBody(t, conflictRes))
	}

	// 4. Missing chart in OCI Registry -> 422 CHART_NOT_FOUND
	missingPayload := map[string]any{
		"version": "0.8.8",
		"chart": map[string]any{
			"ociRef":         "oci://registry.verdantflare.ai/charts/flux-image",
			"version":        "0.8.8",
			"manifestDigest": art.ManifestDigest,
		},
	}
	missingRes := request(t, server, http.MethodPost, "/api/control/ops/v2/apps/flux-image/versions", missingPayload)
	defer missingRes.Body.Close()
	if missingRes.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("missing chart status = %d, want 422, body = %s", missingRes.StatusCode, readBody(t, missingRes))
	}

	// 5. Identity mismatch in Chart.yaml -> 422 CHART_IDENTITY_MISMATCH
	mismatchPayload := map[string]any{
		"version": "0.9.9",
		"chart": map[string]any{
			"ociRef":         "oci://registry.verdantflare.ai/charts/flux-image",
			"version":        "0.9.9",
			"manifestDigest": mismatchArt.ManifestDigest,
			"packageDigest":  mismatchArt.PackageDigest,
		},
	}
	mismatchRes := request(t, server, http.MethodPost, "/api/control/ops/v2/apps/flux-image/versions", mismatchPayload)
	defer mismatchRes.Body.Close()
	if mismatchRes.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("identity mismatch status = %d, want 422, body = %s", mismatchRes.StatusCode, readBody(t, mismatchRes))
	}

	// 6. Corrupted blob digest -> 502 CHART_DIGEST_MISMATCH
	badDigestPayload := map[string]any{
		"version": "0.1.4",
		"chart": map[string]any{
			"ociRef":         "oci://registry.verdantflare.ai/charts/flux-image",
			"version":        "0.1.4",
			"manifestDigest": badDigestArt.ManifestDigest,
			"packageDigest":  badDigestArt.PackageDigest,
		},
	}
	badDigestRes := request(t, server, http.MethodPost, "/api/control/ops/v2/apps/flux-image/versions", badDigestPayload)
	defer badDigestRes.Body.Close()
	if badDigestRes.StatusCode != http.StatusBadGateway {
		t.Fatalf("corrupted digest status = %d, want 502, body = %s", badDigestRes.StatusCode, readBody(t, badDigestRes))
	}
}

func TestMarketV2ReleaseSelectionAndAppsYamlContract(t *testing.T) {
	art := makeTestOCIArtifact(t, "flux-image", "0.1.3", "0.1.3")
	artifacts := map[string]testOCIRegistryArtifact{
		"flux-image:0.1.3":                 art,
		"flux-image:" + art.ManifestDigest: art,
	}
	blobs := map[string][]byte{
		"flux-image:" + art.PackageDigest: art.PackageBytes,
	}
	server := newTestServerWithOCIRegistry(t, artifacts, blobs)
	createTestAppV2(t, server, "flux-image", "Flux 图像工作台", "image")

	// Register OCI Chart version 0.1.3 for flux-image
	regRes := request(t, server, http.MethodPost, "/api/control/ops/v2/apps/flux-image/versions", map[string]any{
		"version":    "0.1.3",
		"appVersion": "0.1.3",
		"chart": map[string]any{
			"ociRef":         "oci://registry.verdantflare.ai/charts/flux-image",
			"version":        "0.1.3",
			"manifestDigest": art.ManifestDigest,
			"packageDigest":  art.PackageDigest,
		},
		"changelog": "首次 OCI 制品登记",
	})
	regRes.Body.Close()
	if regRes.StatusCode != http.StatusCreated {
		t.Fatalf("register version status = %d", regRes.StatusCode)
	}

	// Get current app and ETag
	appRes := request(t, server, http.MethodGet, "/api/control/ops/v2/apps/flux-image", nil)
	etag := appRes.Header.Get("ETag")
	appRes.Body.Close()
	if etag == "" {
		t.Fatalf("expected ETag header on GET /api/control/ops/v2/apps/flux-image")
	}

	// Missing If-Match -> 428 Precondition Required
	noIfMatchRes := request(t, server, http.MethodPut, "/api/control/ops/v2/apps/flux-image/release", map[string]any{
		"version": "0.1.3",
	})
	noIfMatchRes.Body.Close()
	if noIfMatchRes.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match status = %d, want 428", noIfMatchRes.StatusCode)
	}

	// Select release 0.1.3 with If-Match -> 200 OK
	pubRes := requestWithHeaders(t, server, http.MethodPut, "/api/control/ops/v2/apps/flux-image/release", map[string]any{
		"version": "0.1.3",
	}, map[string]string{"If-Match": etag})
	defer pubRes.Body.Close()
	if pubRes.StatusCode != http.StatusOK {
		t.Fatalf("select release status = %d, body = %s", pubRes.StatusCode, readBody(t, pubRes))
	}
	var publishedApp domain.App
	decode(t, pubRes, &publishedApp)
	if publishedApp.PublishedVersion != "0.1.3" || !publishedApp.Enabled || publishedApp.DisabledReason != "" {
		t.Fatalf("unexpected published app state: %+v", publishedApp)
	}
	newETag := pubRes.Header.Get("ETag")

	// Fetch /api/control/station/v2/apps.yaml and verify schemaVersion=2, no forbidden v1 fields
	yamlRes := request(t, server, http.MethodGet, "/api/control/station/v2/apps.yaml", nil)
	defer yamlRes.Body.Close()
	if yamlRes.StatusCode != http.StatusOK {
		t.Fatalf("station apps.yaml status = %d, body = %s", yamlRes.StatusCode, readBody(t, yamlRes))
	}
	if ct := yamlRes.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/yaml") {
		t.Fatalf("Content-Type = %q, want application/yaml", ct)
	}
	catalogETag := yamlRes.Header.Get("ETag")
	rawYAML := readBody(t, yamlRes)

	for _, forbidden := range []string{"stationId:", "namespace:", "workloadName:", "source:", "installable:"} {
		if strings.Contains(rawYAML, forbidden) {
			t.Fatalf("apps.yaml contains forbidden v1 field %q:\n%s", forbidden, rawYAML)
		}
	}

	var parsed domain.MarketAppsYaml
	if err := yaml.Unmarshal([]byte(rawYAML), &parsed); err != nil {
		t.Fatalf("unmarshal apps.yaml: %v", err)
	}
	if parsed.SchemaVersion != 2 || len(parsed.Apps) == 0 {
		t.Fatalf("unexpected parsed apps.yaml: %+v", parsed)
	}
	var foundFlux *domain.MarketCatalogItem
	for i := range parsed.Apps {
		if parsed.Apps[i].AppID == "flux-image" {
			foundFlux = &parsed.Apps[i]
			break
		}
	}
	if foundFlux == nil {
		t.Fatalf("flux-image not found in apps.yaml")
	}
	if foundFlux.Version != "0.1.3" || foundFlux.Chart != "oci://registry.verdantflare.ai/charts/flux-image" || foundFlux.ChartVersion != "0.1.3" || foundFlux.DisabledReason != "" {
		t.Fatalf("unexpected flux-image item in apps.yaml: %+v", *foundFlux)
	}

	// Conditional GET with If-None-Match -> 304 Not Modified
	notModRes := requestWithHeaders(t, server, http.MethodGet, "/api/control/station/v2/apps.yaml", nil, map[string]string{
		"If-None-Match": catalogETag,
	})
	notModRes.Body.Close()
	if notModRes.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional GET status = %d, want 304", notModRes.StatusCode)
	}

	// Unpublish release (version: null) -> disabledReason populated, chart/version omitted in apps.yaml
	unpubRes := requestWithHeaders(t, server, http.MethodPut, "/api/control/ops/v2/apps/flux-image/release", map[string]any{
		"version": nil,
	}, map[string]string{"If-Match": newETag})
	defer unpubRes.Body.Close()
	if unpubRes.StatusCode != http.StatusOK {
		t.Fatalf("unpublish status = %d, body = %s", unpubRes.StatusCode, readBody(t, unpubRes))
	}
	var unpublishedApp domain.App
	decode(t, unpubRes, &unpublishedApp)
	if unpublishedApp.PublishedVersion != "" || unpublishedApp.Enabled || unpublishedApp.DisabledReason == "" {
		t.Fatalf("expected unpublished app to have empty publishedVersion and non-empty disabledReason, got %+v", unpublishedApp)
	}
}

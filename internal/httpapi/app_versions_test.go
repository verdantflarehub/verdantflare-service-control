package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func candidateManifestForTest(appID, version, image string) map[string]any {
	return map[string]any{
		"app_id": appID, "version": version, "display_name": "Workflow Demo",
		"capabilities":    []map[string]any{{"capability_id": "image.generate", "version": "1.0.0"}},
		"required_models": []any{}, "required_resources": map[string]any{"gpu": 0, "disk_bytes": 0},
		"group_id": "image",
		"deployment": map[string]any{"template_ref": "image/workflow-demo", "workload_name": appID,
			"images": []map[string]any{{"component": "web", "image": image}}},
		"entrypoints": []map[string]any{{"name": "dashboard", "protocol": "http", "port": 8080, "path": "/"}},
	}
}

func TestCandidateVersionIsContentAddressedImmutableAndNotStationInstallable(t *testing.T) {
	server := newTestServer(t, testConfig())
	const appID = "candidate-version-demo"
	created := request(t, server, http.MethodPost, "/api/control/ops/apps", map[string]any{
		"id": appID, "name": "Workflow Demo", "version": "0.1.0", "category": "Image", "summary": "候选资料",
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create app: %d %s", created.StatusCode, readBody(t, created))
	}
	created.Body.Close()

	path := "/api/control/ops/apps/" + appID + "/versions"
	image := "registry.example.test/workflow:v0.1.0@sha256:" + strings.Repeat("a", 64)
	input := map[string]any{
		"version": "0.1.0", "publisher": "VerdantFlare", "sourceUrl": "https://github.com/example/workflow",
		"sourceRevision": strings.Repeat("b", 40), "licenseId": "Apache-2.0",
		"licenseUrl":   "https://github.com/example/workflow/blob/main/LICENSE",
		"manifest":     candidateManifestForTest(appID, "0.1.0", image),
		"dependencies": []any{}, "permissions": []map[string]any{{"scope": "http:serve", "reason": "受控 Dashboard"}},
	}
	response := request(t, server, http.MethodPost, path, input)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create version: %d %s", response.StatusCode, readBody(t, response))
	}
	var version domain.AppVersion
	decode(t, response, &version)
	response.Body.Close()
	if version.AppID != appID || version.Version != "0.1.0" || len(version.ManifestSHA256) != 64 ||
		version.ManifestRef != "center-manifest://sha256/"+version.ManifestSHA256 || version.Status != "center_recorded" ||
		len(version.Artifacts) != 1 || version.Artifacts[0].SHA256 != strings.Repeat("a", 64) {
		t.Fatalf("invalid immutable version: %+v", version)
	}
	for _, check := range version.Validation {
		if check.Code == "station_registration" && check.Status != "pending" {
			t.Fatalf("Center must not claim Station registration: %+v", version.Validation)
		}
	}

	duplicate := request(t, server, http.MethodPost, path, input)
	if duplicate.StatusCode != http.StatusConflict {
		t.Fatalf("sealed version was overwritten: %d %s", duplicate.StatusCode, readBody(t, duplicate))
	}
	duplicate.Body.Close()
	listed := request(t, server, http.MethodGet, path, nil)
	var versions []domain.AppVersion
	decode(t, listed, &versions)
	listed.Body.Close()
	if len(versions) != 1 || versions[0].ManifestSHA256 != version.ManifestSHA256 {
		t.Fatalf("version history changed: %+v", versions)
	}
	selected := request(t, server, http.MethodGet, path+"/0.1.0", nil)
	var persisted domain.AppVersion
	decode(t, selected, &persisted)
	selected.Body.Close()
	if string(persisted.Manifest) != string(version.Manifest) {
		t.Fatal("manifest changed after creation")
	}
	market := request(t, server, http.MethodGet, "/api/control/market/apps", nil)
	var apps []domain.App
	decode(t, market, &apps)
	market.Body.Close()
	if containsApp(apps, appID) {
		t.Fatal("Center candidate appeared in customer Market")
	}
}

func TestCandidateVersionRejectsMutableImageAndCrossOrganizationRead(t *testing.T) {
	server := newTestServer(t, testConfig())
	const appID = "candidate-secure-demo"
	created := request(t, server, http.MethodPost, "/api/control/ops/apps", map[string]any{
		"id": appID, "name": "Secure Demo", "version": "0.1.0", "category": "Image", "summary": "候选资料",
	})
	created.Body.Close()
	path := "/api/control/ops/apps/" + appID + "/versions"
	input := map[string]any{
		"version": "0.1.0", "publisher": "VerdantFlare", "sourceUrl": "https://github.com/example/workflow",
		"sourceRevision": strings.Repeat("b", 40), "licenseId": "Apache-2.0",
		"licenseUrl": "https://github.com/example/workflow/blob/main/LICENSE",
		"manifest":   candidateManifestForTest(appID, "0.1.0", "registry.example.test/workflow:latest"),
	}
	invalid := request(t, server, http.MethodPost, path, input)
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("mutable image accepted: %d %s", invalid.StatusCode, readBody(t, invalid))
	}
	invalid.Body.Close()

	switched := request(t, server, http.MethodPut, "/api/control/context/active-organization", map[string]any{"organizationId": "org_northshore"})
	if switched.StatusCode != http.StatusOK {
		t.Fatalf("switch organization: %d %s", switched.StatusCode, readBody(t, switched))
	}
	switched.Body.Close()
	for _, target := range []string{path, path + "/0.1.0"} {
		response := request(t, server, http.MethodGet, target, nil)
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("non-operator read %s: %d %s", target, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}
}

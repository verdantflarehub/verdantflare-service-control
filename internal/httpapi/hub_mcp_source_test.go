package httpapi_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
)

type fakeMarketGitHub struct {
	mu         sync.Mutex
	treeBody   []map[string]any
	dispatched string
	runPolls   int
}

func (f *fakeMarketGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer market-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	reply := func(status int, body any) {
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}
	path := strings.TrimPrefix(r.URL.Path, "/repos/verdantflarehub/verdantflare-market")
	switch {
	case r.Method == http.MethodGet && path == "/git/ref/heads/dev":
		reply(200, map[string]any{"object": map[string]string{"sha": "base-commit"}})
	case r.Method == http.MethodGet && path == "/git/commits/base-commit":
		reply(200, map[string]any{"tree": map[string]string{"sha": "base-tree"}})
	case r.Method == http.MethodGet && path == "/git/trees/base-tree":
		reply(200, map[string]any{"tree": []map[string]string{{"path": "charts/source-test/obsolete.yaml", "type": "blob"}, {"path": "charts/other/Chart.yaml", "type": "blob"}}})
	case r.Method == http.MethodPost && path == "/git/blobs":
		reply(201, map[string]string{"sha": "blob"})
	case r.Method == http.MethodPost && path == "/git/trees":
		var body struct {
			Tree []map[string]any `json:"tree"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.treeBody = body.Tree
		reply(201, map[string]string{"sha": "new-tree"})
	case r.Method == http.MethodPost && path == "/git/commits":
		reply(201, map[string]string{"sha": "0123456789abcdef0123456789abcdef01234567"})
	case r.Method == http.MethodPatch && path == "/git/refs/heads/dev":
		reply(200, map[string]string{})
	case r.Method == http.MethodPost && path == "/actions/workflows/publish-charts.yml/dispatches":
		var body struct {
			Ref    string            `json:"ref"`
			Inputs map[string]string `json:"inputs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.dispatched = body.Ref + ":" + body.Inputs["charts"]
		reply(204, nil)
	case r.Method == http.MethodGet && path == "/actions/workflows/publish-charts.yml/runs":
		f.runPolls++
		run := map[string]any{"id": 42, "html_url": "https://github.test/runs/42", "status": "in_progress", "conclusion": ""}
		if f.runPolls > 1 {
			run["status"], run["conclusion"] = "completed", "success"
		}
		reply(200, map[string]any{"workflow_runs": []any{run}})
	default:
		reply(404, map[string]string{"message": r.Method + " " + path})
	}
}

func tarChart(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestHubMCPSubmitChartReleasesThroughMarket(t *testing.T) {
	artifact := makeTestOCIArtifact(t, "source-test", "0.2.0", "1.2.0")
	github := &fakeMarketGitHub{}
	ghServer := httptest.NewServer(github)
	t.Cleanup(ghServer.Close)
	server := newTestServerWithOCIRegistryConfig(t, testConfig(),
		map[string]testOCIRegistryArtifact{"source-test:0.2.0": artifact, "source-test:" + artifact.ManifestDigest: artifact},
		map[string][]byte{"source-test:" + artifact.PackageDigest: artifact.PackageBytes},
		func(s *control.Service) {
			s.SetMarketRepository(&control.GitHubMarket{APIURL: ghServer.URL, Repo: "verdantflarehub/verdantflare-market", Token: "market-token", Client: ghServer.Client()})
		})
	_, token := issueMCPToken(t, server)
	chartYAML := "apiVersion: v2\nname: source-test\nversion: 0.2.0\nappVersion: \"1.2.0\"\n"
	payload := map[string]any{
		"appId": "source-test", "name": "Source Test", "groupId": "desktop", "category": "Tool",
		"summary": "Chart source release", "description": "Releases a chart from source through Market", "developer": "VerdantFlare",
		"showcase":        map[string]any{"screenshots": []string{"https://example.test/screen.png"}, "highlights": []string{"Releases from source"}},
		"version":         map[string]any{"version": "1.2.0"},
		"organizationIds": []string{"org_verdantflare"},
		"chartArchive":    tarChart(t, map[string]string{"source-test/Chart.yaml": chartYAML, "source-test/templates/deployment.yaml": "kind: Deployment\n"}),
	}

	for name, archive := range map[string]string{
		"unsafe path": tarChart(t, map[string]string{"source-test/../evil.yaml": "x", "source-test/Chart.yaml": chartYAML}),
		"wrong name":  tarChart(t, map[string]string{"source-test/Chart.yaml": strings.Replace(chartYAML, "name: source-test", "name: other", 1)}),
	} {
		bad := map[string]any{}
		for k, v := range payload {
			bad[k] = v
		}
		bad["chartArchive"] = archive
		if failed, raw := hubMCPCall(t, server, token, "app_publish.submit_chart", bad); !failed || !strings.Contains(string(raw), "CHART_ARCHIVE_INVALID") {
			t.Fatalf("%s accepted: %s", name, raw)
		}
	}

	failed, raw := hubMCPCall(t, server, token, "app_publish.submit_chart", payload)
	var task struct {
		TaskID, Stage, CommitSHA, ManifestDigest, PackageDigest, Error string
		RunID                                                          int64
	}
	if err := json.Unmarshal(raw, &task); failed || err != nil || task.Stage != "building" || task.CommitSHA == "" {
		t.Fatalf("submit failed: %s", raw)
	}
	if github.dispatched != "dev:source-test" {
		t.Fatalf("dispatch = %q", github.dispatched)
	}
	deleted, written := false, false
	for _, entry := range github.treeBody {
		if entry["path"] == "charts/source-test/obsolete.yaml" && entry["sha"] == nil {
			deleted = true
		}
		if entry["path"] == "charts/source-test/Chart.yaml" {
			written = true
		}
		if strings.HasPrefix(entry["path"].(string), "charts/other/") {
			t.Fatalf("touched another chart: %v", entry)
		}
	}
	if !deleted || !written {
		t.Fatalf("tree entries = %v", github.treeBody)
	}

	if _, raw = hubMCPCall(t, server, token, "app_publish.status", map[string]any{"taskId": task.TaskID}); !strings.Contains(string(raw), `"stage":"building"`) {
		t.Fatalf("first status should still be building: %s", raw)
	}
	_, raw = hubMCPCall(t, server, token, "app_publish.status", map[string]any{"taskId": task.TaskID})
	if err := json.Unmarshal(raw, &task); err != nil || task.Stage != "released" || task.RunID != 42 ||
		task.ManifestDigest != artifact.ManifestDigest || task.PackageDigest != artifact.PackageDigest {
		t.Fatalf("task did not release: %s", raw)
	}
	_, raw = hubMCPCall(t, server, token, "app_publish.list", map[string]any{})
	if !strings.Contains(string(raw), `"appId":"source-test"`) {
		t.Fatalf("released app missing from catalog: %s", raw)
	}
}

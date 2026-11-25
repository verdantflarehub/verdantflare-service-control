package httpapi_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/skillrelease"
)

func sum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

func zipOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write(content)
	}
	w.Close()
	return out.Bytes()
}

// syntheticSkillInput builds a handoff archive in the same layout as Luna's vf-hub-skill-input.
func syntheticSkillInput(t *testing.T, releaseID, installText string) []byte {
	t.Helper()
	outer := map[string][]byte{}
	lock := []byte(`{"lock":true}`)
	outer["candidate/source-lock.json"] = lock
	packages := []map[string]any{}
	for _, id := range skillrelease.SkillIDs() {
		skillMD := []byte("# " + id + "\n")
		pkg := zipOf(t, map[string][]byte{id + "/SKILL.md": skillMD})
		manifest, _ := json.Marshal(map[string]any{"skill_id": id, "files": []map[string]any{{"path": id + "/SKILL.md", "sha256": sum(skillMD), "size": len(skillMD)}}})
		outer["candidate/packages/"+id+".zip"] = pkg
		outer["candidate/manifests/"+id+".json"] = manifest
		requires := []string{}
		if id != "verdantflare-artifact" {
			requires = []string{"verdantflare-artifact"}
		}
		packages = append(packages, map[string]any{"id": id, "version": releaseID, "requires": requires, "status": "assembled", "blockers": []string{},
			"artifact": map[string]any{"path": "packages/" + id + ".zip", "sha256": sum(pkg), "size": len(pkg),
				"file_manifest": "manifests/" + id + ".json", "file_manifest_sha256": sum(manifest)}})
	}
	release, _ := json.Marshal(map[string]any{"schema_version": "vf.skills.handoff/v2", "release_id": releaseID, "release_stage": "integration-candidate",
		"authentication": "unsigned", "source_lock_sha256": sum(lock), "packages": packages})
	outer["candidate/release-manifest.json"] = release
	install, users := []byte(installText), []byte("# 用户技能\n")
	outer["guides/Install.md"], outer["guides/USER-SKILLS.md"] = install, users
	guides, _ := json.Marshal(map[string]any{"release_id": releaseID, "candidate_manifest_sha256": sum(release), "files": map[string]any{
		"Install.md": map[string]any{"sha256": sum(install), "size": len(install)}, "USER-SKILLS.md": map[string]any{"sha256": sum(users), "size": len(users)}}})
	outer["guides/guide-manifest.json"] = guides
	return zipOf(t, outer)
}

func uploadRelease(t *testing.T, server *httptest.Server, archive []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("archive", "input.zip")
	part.Write(archive)
	form.Close()
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/control/ops/v2/skill-releases", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func expectStatus(t *testing.T, res *http.Response, want int) []byte {
	t.Helper()
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != want {
		t.Fatalf("%s %s status=%d want %d body=%s", res.Request.Method, res.Request.URL.Path, res.StatusCode, want, body)
	}
	return body
}

func TestSkillReleaseUploadPublishDownloadWithdraw(t *testing.T) {
	server := newTestServer(t, testConfig())
	archive := syntheticSkillInput(t, "0.3.0-test.1", "# 安装\n")

	before := expectStatus(t, request(t, server, http.MethodGet, "/api/control/market/skill-release", nil), http.StatusOK)
	if !bytes.Contains(before, []byte(`"available":false`)) {
		t.Fatalf("release before upload: %s", before)
	}
	var created domain.SkillRelease
	json.Unmarshal(expectStatus(t, uploadRelease(t, server, archive), http.StatusCreated), &created)
	if created.Status != "candidate" || len(created.Files) != 14 {
		t.Fatalf("created=%+v", created)
	}
	expectStatus(t, uploadRelease(t, server, archive), http.StatusOK)
	expectStatus(t, uploadRelease(t, server, syntheticSkillInput(t, "0.3.0-test.1", "# 改过\n")), http.StatusConflict)
	expectStatus(t, uploadRelease(t, server, []byte("broken")), http.StatusUnprocessableEntity)

	file := "/api/control/market/skill-releases/0.3.0-test.1/files/verdantflare-image.zip"
	expectStatus(t, request(t, server, http.MethodGet, file, nil), http.StatusNotFound)
	expectStatus(t, request(t, server, http.MethodPost, "/api/control/ops/v2/skill-releases/0.3.0-test.1/publish", nil), http.StatusOK)
	expectStatus(t, request(t, server, http.MethodPost, "/api/control/ops/v2/skill-releases/0.3.0-test.1/publish", nil), http.StatusConflict)

	current := expectStatus(t, request(t, server, http.MethodGet, "/api/control/market/skill-release", nil), http.StatusOK)
	if !bytes.Contains(current, []byte(`"releaseId":"0.3.0-test.1"`)) {
		t.Fatalf("current=%s", current)
	}
	res := request(t, server, http.MethodGet, file, nil)
	if res.Header.Get("Cache-Control") != "private, no-store" || res.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("headers=%v", res.Header)
	}
	data := expectStatus(t, res, http.StatusOK)
	if sum(data) != res.Header.Get("X-Content-SHA256") {
		t.Fatal("downloaded digest mismatch")
	}
	guide := expectStatus(t, request(t, server, http.MethodGet, "/api/control/market/skill-releases/0.3.0-test.1/files/Install.md", nil), http.StatusOK)
	if string(guide) != "# 安装\n" {
		t.Fatalf("guide=%q", guide)
	}
	expectStatus(t, request(t, server, http.MethodGet, "/api/control/market/skill-releases/0.3.0-test.1/files/..%2Fsecret", nil), http.StatusNotFound)

	// A member without skill_ops_admin can download but not manage.
	expectStatus(t, request(t, server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_northshore"}), http.StatusOK)
	expectStatus(t, request(t, server, http.MethodGet, file, nil), http.StatusOK)
	expectStatus(t, uploadRelease(t, server, archive), http.StatusForbidden)
	expectStatus(t, request(t, server, http.MethodPost, "/api/control/ops/v2/skill-releases/0.3.0-test.1/withdraw", nil), http.StatusForbidden)
	expectStatus(t, request(t, server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_verdantflare"}), http.StatusOK)

	expectStatus(t, request(t, server, http.MethodPost, "/api/control/ops/v2/skill-releases/0.3.0-test.1/withdraw", nil), http.StatusOK)
	expectStatus(t, request(t, server, http.MethodGet, file, nil), http.StatusGone)
	after := expectStatus(t, request(t, server, http.MethodGet, "/api/control/market/skill-release", nil), http.StatusOK)
	if !bytes.Contains(after, []byte(`"available":false`)) {
		t.Fatalf("release after withdraw: %s", after)
	}
	expectStatus(t, request(t, server, http.MethodPost, "/api/control/ops/v2/skill-releases/0.3.0-test.1/publish", nil), http.StatusConflict)
}

func TestSkillReleaseRequiresCenterMember(t *testing.T) {
	server := newTestServer(t, testConfig())
	expectStatus(t, uploadRelease(t, server, syntheticSkillInput(t, "0.3.0-test.2", "# 安装\n")), http.StatusCreated)
	expectStatus(t, request(t, server, http.MethodPost, "/api/control/ops/v2/skill-releases/0.3.0-test.2/publish", nil), http.StatusOK)

	cfg := testConfig()
	cfg.DevLoginSubject, cfg.TrustAuthHeaders = "", true
	guest := newTestServer(t, cfg)
	req, _ := http.NewRequest(http.MethodGet, guest.URL+"/api/control/market/skill-release", nil)
	req.Header.Set("X-VF-Login-Subject", "new-unbound-user")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	expectStatus(t, res, http.StatusForbidden)
	anon, _ := http.Get(guest.URL + "/api/control/market/skill-release")
	expectStatus(t, anon, http.StatusUnauthorized)
}

// Set VF_SKILL_INPUT_ZIP to Luna's private vf-hub-skill-input archive to run the real-bytes round trip.
func TestSkillReleaseRealInputRoundTrip(t *testing.T) {
	path := os.Getenv("VF_SKILL_INPUT_ZIP")
	if path == "" {
		t.Skip("VF_SKILL_INPUT_ZIP not set")
	}
	archive, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, testConfig())
	var created domain.SkillRelease
	json.Unmarshal(expectStatus(t, uploadRelease(t, server, archive), http.StatusCreated), &created)
	expectStatus(t, request(t, server, http.MethodPost, "/api/control/ops/v2/skill-releases/"+created.ReleaseID+"/publish", nil), http.StatusOK)
	expectStatus(t, request(t, server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_northshore"}), http.StatusOK)
	for _, f := range created.Files {
		data := expectStatus(t, request(t, server, http.MethodGet, "/api/control/market/skill-releases/"+created.ReleaseID+"/files/"+f.Name, nil), http.StatusOK)
		if sum(data) != f.SHA256 || int64(len(data)) != f.Size {
			t.Fatalf("%s digest mismatch", f.Name)
		}
		t.Logf("%s %d %s", f.Name, f.Size, f.SHA256)
	}
}

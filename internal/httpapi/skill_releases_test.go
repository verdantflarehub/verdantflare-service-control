package httpapi_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/httpapi"
	"github.com/verdantflarehub/verdantflare-service-control/internal/skillrelease"
	"github.com/verdantflarehub/verdantflare-service-control/internal/skillsource"
	"github.com/verdantflarehub/verdantflare-service-control/internal/skillsource/skillsourcetest"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

var sourceCommit = strings.Repeat("c", 40)

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

type inputOptions struct {
	install string
	commit  string
	dirty   bool
	schema  string
	ids     []string
}

// syntheticSkillInput builds a handoff archive in the same layout as the
// skills repository's vf-hub-skill-input, built from a clean commit by default.
func syntheticSkillInput(t *testing.T, releaseID string, options ...inputOptions) []byte {
	t.Helper()
	opt := inputOptions{install: "# 安装\n", commit: sourceCommit}
	if len(options) > 0 {
		opt = options[0]
	}
	outer := map[string][]byte{}
	lock, _ := json.Marshal(map[string]any{"release_id": releaseID, "source_repository": map[string]any{"commit": opt.commit, "dirty": opt.dirty}})
	outer["candidate/source-lock.json"] = lock
	packages := []map[string]any{}
	ids := opt.ids
	if ids == nil {
		ids = skillrelease.SkillIDs()
	}
	for _, id := range ids {
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
	schema := opt.schema
	if schema == "" {
		schema = "vf.skills.handoff/v2"
	}
	release, _ := json.Marshal(map[string]any{"schema_version": schema, "release_id": releaseID, "release_stage": "integration-candidate",
		"authentication": "unsigned", "source_lock_sha256": sum(lock), "packages": packages})
	outer["candidate/release-manifest.json"] = release
	install, users := []byte(opt.install), []byte("# 用户技能\n")
	outer["guides/Install.md"], outer["guides/USER-SKILLS.md"] = install, users
	guides, _ := json.Marshal(map[string]any{"release_id": releaseID, "candidate_manifest_sha256": sum(release), "files": map[string]any{
		"Install.md": map[string]any{"sha256": sum(install), "size": len(install)}, "USER-SKILLS.md": map[string]any{"sha256": sum(users), "size": len(users)}}})
	outer["guides/guide-manifest.json"] = guides
	return zipOf(t, outer)
}

type skillHarness struct {
	server     *httptest.Server
	github     *skillsourcetest.GitHub
	repository *store.Memory
}

func newSkillHarness(t *testing.T) skillHarness {
	t.Helper()
	github := skillsourcetest.New(t, "")
	repository := store.NewMemorySeeded(time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC))
	service := control.NewService(repository)
	source, err := skillsource.New(github.API.URL, skillsourcetest.Repository, "")
	if err != nil {
		t.Fatal(err)
	}
	service.SetSkillSource(source)
	server := httptest.NewServer(httpapi.New(testConfig(), service, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
	return skillHarness{server: server, github: github, repository: repository}
}

// publishToGitHub plays the skills repository's release workflow.
func (h skillHarness) publishToGitHub(releaseID string, archive []byte, release ...skillsourcetest.Release) string {
	r := skillsourcetest.Release{}
	if len(release) > 0 {
		r = release[0]
	}
	r.Archive = archive
	if r.Commit == "" {
		r.Commit = sourceCommit
	}
	h.github.Put(releaseID, r)
	return sum(archive)
}

func issueSkillToken(t *testing.T, server *httptest.Server) string {
	t.Helper()
	body := expectStatus(t, request(t, server, http.MethodPost, "/api/control/settings/mcp-tokens",
		map[string]any{"name": "技能发布 Agent", "expiresInDays": 30, "purpose": "skill_publish"}), http.StatusCreated)
	var created struct {
		Secret  string `json:"secret"`
		Purpose string `json:"purpose"`
	}
	json.Unmarshal(body, &created)
	if created.Purpose != "skill_publish" || !strings.HasPrefix(created.Secret, "vfhub_") {
		t.Fatalf("token=%s", body)
	}
	return created.Secret
}

func skillMCPPost(t *testing.T, server *httptest.Server, token string, body []byte, headers ...string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/control/skill-publishing/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

type toolResult struct {
	IsError bool
	Content json.RawMessage
	Failure struct {
		Status    int    `json:"status"`
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	}
}

func skillCall(t *testing.T, server *httptest.Server, token, name string, arguments any) toolResult {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": arguments}})
	data := expectStatus(t, skillMCPPost(t, server, token, body), http.StatusOK)
	var envelope struct {
		Result struct {
			IsError           bool            `json:"isError"`
			StructuredContent json.RawMessage `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("%s: %s", name, data)
	}
	result := toolResult{IsError: envelope.Result.IsError, Content: envelope.Result.StructuredContent}
	if result.IsError {
		json.Unmarshal(result.Content, &result.Failure)
	}
	return result
}

func expectToolError(t *testing.T, result toolResult, status int, code string) {
	t.Helper()
	if !result.IsError || result.Failure.Status != status || (code != "" && result.Failure.Code != code) {
		t.Fatalf("want %d %s, got %s", status, code, result.Content)
	}
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

func TestSkillPublishingMCPRegisterPublishDownloadWithdraw(t *testing.T) {
	h := newSkillHarness(t)
	token := issueSkillToken(t, h.server)
	digest := h.publishToGitHub("0.3.0", syntheticSkillInput(t, "0.3.0"))

	initialize := expectStatus(t, skillMCPPost(t, h.server, token, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)), http.StatusOK)
	if !bytes.Contains(initialize, []byte(`"name":"verdantflare-skill-publishing"`)) {
		t.Fatalf("initialize=%s", initialize)
	}
	tools := expectStatus(t, skillMCPPost(t, h.server, token, []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)), http.StatusOK)
	var listed struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	json.Unmarshal(tools, &listed)
	names := []string{}
	for _, tool := range listed.Result.Tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "skill_publish.list,skill_publish.get,skill_publish.register,skill_publish.readiness,skill_publish.publish,skill_publish.withdraw" {
		t.Fatalf("tools=%v", names)
	}

	registered := skillCall(t, h.server, token, "skill_publish.register", map[string]any{"releaseId": "0.3.0", "expectedArchiveSha256": digest})
	var created struct {
		Created bool                `json:"created"`
		Release domain.SkillRelease `json:"release"`
	}
	json.Unmarshal(registered.Content, &created)
	if registered.IsError || !created.Created || created.Release.Status != "candidate" || len(created.Release.Files) != 14 ||
		created.Release.Source == nil || created.Release.Source.Commit != sourceCommit || created.Release.Source.SHA256 != digest {
		t.Fatalf("register=%s", registered.Content)
	}
	before := h.github.Requests()
	again := skillCall(t, h.server, token, "skill_publish.register", map[string]any{"releaseId": "0.3.0", "expectedArchiveSha256": digest})
	if again.IsError || !bytes.Contains(again.Content, []byte(`"created":false`)) || h.github.Requests() != before {
		t.Fatalf("re-register should return the candidate without fetching again: %s", again.Content)
	}
	expectToolError(t, skillCall(t, h.server, token, "skill_publish.register", map[string]any{"releaseId": "0.3.0", "expectedArchiveSha256": strings.Repeat("0", 64)}), 409, "skill_release_conflict")
	expectToolError(t, skillCall(t, h.server, token, "skill_publish.register", map[string]any{"releaseId": "0.3.0", "expectedArchiveSha256": digest, "url": "https://evil.example"}), 400, "INVALID_ARGUMENTS")

	ready := skillCall(t, h.server, token, "skill_publish.readiness", map[string]any{"releaseId": "0.3.0"})
	if ready.IsError || !bytes.Contains(ready.Content, []byte(`"ready":true`)) || !bytes.Contains(ready.Content, []byte(`"sourceVerified":true`)) {
		t.Fatalf("readiness=%s", ready.Content)
	}
	file := "/api/control/market/skill-releases/0.3.0/files/verdantflare-image.zip"
	expectStatus(t, request(t, h.server, http.MethodGet, file, nil), http.StatusNotFound)

	expectToolError(t, skillCall(t, h.server, token, "skill_publish.publish", map[string]any{"releaseId": "0.3.0"}), 400, "skill_release_digest_invalid")
	expectToolError(t, skillCall(t, h.server, token, "skill_publish.publish", map[string]any{"releaseId": "0.3.0", "expectedArchiveSha256": strings.Repeat("0", 64)}), 409, "skill_release_digest_mismatch")
	published := skillCall(t, h.server, token, "skill_publish.publish", map[string]any{"releaseId": "0.3.0", "expectedArchiveSha256": digest})
	if published.IsError || !bytes.Contains(published.Content, []byte(`"currentReleaseId":"0.3.0"`)) {
		t.Fatalf("publish=%s", published.Content)
	}
	expectToolError(t, skillCall(t, h.server, token, "skill_publish.publish", map[string]any{"releaseId": "0.3.0", "expectedArchiveSha256": digest}), 409, "skill_release_unchanged")

	// A member without skill_ops_admin downloads the published bytes.
	expectStatus(t, request(t, h.server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_northshore"}), http.StatusOK)
	res := request(t, h.server, http.MethodGet, file, nil)
	data := expectStatus(t, res, http.StatusOK)
	if sum(data) != res.Header.Get("X-Content-SHA256") || res.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("downloaded digest mismatch")
	}
	// The token follows its owner's live role: without skill_ops_admin it stops working.
	expectStatus(t, skillMCPPost(t, h.server, token, []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)), http.StatusForbidden)
	expectStatus(t, request(t, h.server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_verdantflare"}), http.StatusOK)

	expectToolError(t, skillCall(t, h.server, token, "skill_publish.withdraw", map[string]any{"releaseId": "0.3.0", "expectedArchiveSha256": digest}), 400, "skill_release_reason_invalid")
	withdrawn := skillCall(t, h.server, token, "skill_publish.withdraw", map[string]any{"releaseId": "0.3.0", "expectedArchiveSha256": digest, "reason": "指南有误"})
	if withdrawn.IsError || !bytes.Contains(withdrawn.Content, []byte(`"status":"withdrawn"`)) || !bytes.Contains(withdrawn.Content, []byte(`"currentReleaseId":""`)) {
		t.Fatalf("withdraw=%s", withdrawn.Content)
	}
	expectStatus(t, request(t, h.server, http.MethodGet, file, nil), http.StatusGone)
	expectToolError(t, skillCall(t, h.server, token, "skill_publish.publish", map[string]any{"releaseId": "0.3.0", "expectedArchiveSha256": digest}), 409, "skill_release_withdrawn")

	detail := skillCall(t, h.server, token, "skill_publish.get", map[string]any{"releaseId": "0.3.0"})
	var got control.SkillReleaseDetail
	json.Unmarshal(detail.Content, &got)
	actions := []string{}
	for _, event := range got.Audit {
		if event.TokenID == "" || event.Tool == "" || event.OrganizationID != "org_verdantflare" {
			t.Fatalf("audit event without MCP attribution: %+v", event)
		}
		actions = append(actions, event.Action+":"+event.Reason)
	}
	if strings.Join(actions, ",") != "withdraw:指南有误,publish:,register:" {
		t.Fatalf("audit=%v", actions)
	}
	if strings.Contains(string(detail.Content), "vfhub_") {
		t.Fatal("token secret leaked into the release detail")
	}
}

func TestSkillPublishingMCPRefusesUntrustedInputs(t *testing.T) {
	h := newSkillHarness(t)
	token := issueSkillToken(t, h.server)
	other := strings.Repeat("d", 40)
	cases := []struct {
		name    string
		id      string
		archive []byte
		release skillsourcetest.Release
		status  int
		code    string
	}{
		{"built from a dirty work tree", "0.4.0", syntheticSkillInput(t, "0.4.0", inputOptions{install: "#\n", commit: sourceCommit, dirty: true}), skillsourcetest.Release{}, 422, "skill_source_untrusted"},
		{"lock commit differs from tag", "0.4.1", syntheticSkillInput(t, "0.4.1", inputOptions{install: "#\n", commit: other}), skillsourcetest.Release{}, 422, "skill_source_untrusted"},
		{"lock without source state", "0.4.2", nil, skillsourcetest.Release{}, 422, "skill_source_untrusted"},
		{"package of another release", "0.4.3", syntheticSkillInput(t, "0.4.9"), skillsourcetest.Release{}, 422, "skill_release_invalid"},
		{"not a handoff archive", "0.4.4", []byte("broken"), skillsourcetest.Release{}, 422, "skill_release_invalid"},
		{"uploaded by hand", "0.4.5", syntheticSkillInput(t, "0.4.5"), skillsourcetest.Release{Uploader: "luna"}, 422, "skill_source_untrusted"},
		{"commit off the release branch", "0.4.6", syntheticSkillInput(t, "0.4.6", inputOptions{install: "#\n", commit: strings.Repeat("e", 40)}),
			skillsourcetest.Release{Commit: strings.Repeat("e", 40), CompareState: "ahead"}, 422, "skill_source_untrusted"},
	}
	for _, tc := range cases {
		archive := tc.archive
		if archive == nil {
			// A lock with no source_repository counts as dirty.
			archive = syntheticSkillInput(t, tc.id, inputOptions{install: "#\n", commit: "", dirty: false})
		}
		digest := h.publishToGitHub(tc.id, archive, tc.release)
		result := skillCall(t, h.server, token, "skill_publish.register", map[string]any{"releaseId": tc.id, "expectedArchiveSha256": digest})
		if !result.IsError || result.Failure.Status != tc.status || result.Failure.Code != tc.code {
			t.Errorf("%s: %s", tc.name, result.Content)
		}
	}
	missing := skillCall(t, h.server, token, "skill_publish.register", map[string]any{"releaseId": "9.9.9", "expectedArchiveSha256": strings.Repeat("0", 64)})
	expectToolError(t, missing, 404, "skill_source_not_found")
	h.github.API.Close()
	down := skillCall(t, h.server, token, "skill_publish.register", map[string]any{"releaseId": "0.5.0", "expectedArchiveSha256": strings.Repeat("0", 64)})
	expectToolError(t, down, 502, "skill_source_unavailable")
	if !down.Failure.Retryable {
		t.Fatal("an unreachable source should be retryable")
	}
	list := skillCall(t, h.server, token, "skill_publish.list", map[string]any{})
	if !bytes.Contains(list.Content, []byte(`"items":[]`)) {
		t.Fatalf("refused inputs must not create candidates: %s", list.Content)
	}
}

func TestSkillPublishingMCPEntryIsolation(t *testing.T) {
	h := newSkillHarness(t)
	skillToken := issueSkillToken(t, h.server)
	_, appToken := issueMCPToken(t, h.server)
	list := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	// A skill token does not open the market /mcp, and an app token does not open this entry.
	expectStatus(t, hubMCPPost(t, h.server, skillToken, list), http.StatusForbidden)
	expectStatus(t, skillMCPPost(t, h.server, appToken, list), http.StatusForbidden)
	// The Hub session alone is not enough: only a Bearer token counts.
	res := skillMCPPost(t, h.server, "", list, "Cookie", "vf_session=ok")
	if res.Header.Get("WWW-Authenticate") != `Bearer realm="skill-publishing-mcp"` {
		t.Fatalf("WWW-Authenticate=%q", res.Header.Get("WWW-Authenticate"))
	}
	expectStatus(t, res, http.StatusUnauthorized)
	expectStatus(t, skillMCPPost(t, h.server, skillToken, append([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{"pad":"`), append(bytes.Repeat([]byte("x"), 70<<10), []byte(`"}}`)...)...)), http.StatusRequestEntityTooLarge)
	// The market /mcp keeps its own tools.
	expectStatus(t, hubMCPPost(t, h.server, appToken, list), http.StatusOK)

	// Skill tokens are listed and issued only for skill operators.
	tokens := expectStatus(t, request(t, h.server, http.MethodGet, "/api/control/settings/mcp-tokens?purpose=skill_publish", nil), http.StatusOK)
	if !bytes.Contains(tokens, []byte(`"purpose":"skill_publish"`)) || bytes.Contains(tokens, []byte(`"purpose":"app_publish"`)) {
		t.Fatalf("tokens=%s", tokens)
	}
	expectStatus(t, request(t, h.server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_northshore"}), http.StatusOK)
	expectStatus(t, request(t, h.server, http.MethodPost, "/api/control/settings/mcp-tokens", map[string]any{"name": "技能发布 Agent", "purpose": "skill_publish"}), http.StatusForbidden)
}

func TestSkillReleasePageListsPublishesAndWithdrawsThroughTheGate(t *testing.T) {
	h := newSkillHarness(t)
	token := issueSkillToken(t, h.server)
	digest := h.publishToGitHub("0.3.0", syntheticSkillInput(t, "0.3.0"))
	skillCall(t, h.server, token, "skill_publish.register", map[string]any{"releaseId": "0.3.0", "expectedArchiveSha256": digest})

	// Manual upload is gone; only GET remains on the collection.
	expectStatus(t, request(t, h.server, http.MethodPost, "/api/control/ops/v2/skill-releases", nil), http.StatusMethodNotAllowed)

	// A candidate with no source receipt can never be published from the page.
	input, err := skillrelease.ParseHandoffInput(syntheticSkillInput(t, "0.2.0"))
	if err != nil {
		t.Fatal(err)
	}
	unsourced := domain.SkillRelease{ReleaseID: input.ReleaseID, Stage: input.Stage, Authentication: input.Authentication, ManifestSHA256: input.ManifestSHA256, ArchiveSHA256: input.ArchiveSHA256}
	for _, f := range input.Files {
		unsourced.Files = append(unsourced.Files, domain.SkillReleaseFile{Name: f.Name, Kind: f.Kind, SkillID: f.SkillID, SHA256: f.SHA256, Size: f.Size, Data: f.Data})
	}
	if _, _, err := h.repository.CreateSkillRelease(context.Background(), unsourced, domain.SkillReleaseActor{Subject: "legacy"}); err != nil {
		t.Fatal(err)
	}
	body := expectStatus(t, request(t, h.server, http.MethodPost, "/api/control/ops/v2/skill-releases/0.2.0/publish", nil), http.StatusUnprocessableEntity)
	if !bytes.Contains(body, []byte("source_receipt_missing")) {
		t.Fatalf("gate=%s", body)
	}
	detail := expectStatus(t, request(t, h.server, http.MethodGet, "/api/control/ops/v2/skill-releases/0.2.0", nil), http.StatusOK)
	if !bytes.Contains(detail, []byte(`"ready":false`)) {
		t.Fatalf("detail=%s", detail)
	}

	expectStatus(t, request(t, h.server, http.MethodPost, "/api/control/ops/v2/skill-releases/0.3.0/publish", map[string]string{"expectedArchiveSha256": strings.Repeat("0", 64)}), http.StatusConflict)
	expectStatus(t, request(t, h.server, http.MethodPost, "/api/control/ops/v2/skill-releases/0.3.0/publish", nil), http.StatusOK)
	listed := expectStatus(t, request(t, h.server, http.MethodGet, "/api/control/ops/v2/skill-releases", nil), http.StatusOK)
	if !bytes.Contains(listed, []byte(`"currentReleaseId":"0.3.0"`)) {
		t.Fatalf("list=%s", listed)
	}
	current := expectStatus(t, request(t, h.server, http.MethodGet, "/api/control/market/skill-release", nil), http.StatusOK)
	if !bytes.Contains(current, []byte(`"releaseId":"0.3.0"`)) {
		t.Fatalf("current=%s", current)
	}
	guide := expectStatus(t, request(t, h.server, http.MethodGet, "/api/control/market/skill-releases/0.3.0/files/Install.md", nil), http.StatusOK)
	if string(guide) != "# 安装\n" {
		t.Fatalf("guide=%q", guide)
	}
	expectStatus(t, request(t, h.server, http.MethodGet, "/api/control/market/skill-releases/0.3.0/files/..%2Fsecret", nil), http.StatusNotFound)

	// Non-operators cannot manage releases from the page either.
	expectStatus(t, request(t, h.server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_northshore"}), http.StatusOK)
	expectStatus(t, request(t, h.server, http.MethodPost, "/api/control/ops/v2/skill-releases/0.3.0/withdraw", nil), http.StatusForbidden)
	expectStatus(t, request(t, h.server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_verdantflare"}), http.StatusOK)

	// Withdrawal is never gated on the source.
	expectStatus(t, request(t, h.server, http.MethodPost, "/api/control/ops/v2/skill-releases/0.3.0/withdraw", nil), http.StatusOK)
	expectStatus(t, request(t, h.server, http.MethodGet, "/api/control/market/skill-releases/0.3.0/files/Install.md", nil), http.StatusGone)
}

func TestSkillReleaseRegisterWithoutSourceIsRefused(t *testing.T) {
	server := newTestServer(t, testConfig())
	token := issueSkillToken(t, server)
	result := skillCall(t, server, token, "skill_publish.register", map[string]any{"releaseId": "0.3.0", "expectedArchiveSha256": strings.Repeat("0", 64)})
	expectToolError(t, result, 503, "skill_source_unconfigured")
}

func TestSkillReleaseRequiresCenterMember(t *testing.T) {
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

// Set VF_SKILL_INPUT_ZIP to a vf-hub-skill-input archive built by the skills
// repository's release workflow to run the real-bytes round trip.
func TestSkillReleaseRealInputRoundTrip(t *testing.T) {
	path := os.Getenv("VF_SKILL_INPUT_ZIP")
	if path == "" {
		t.Skip("VF_SKILL_INPUT_ZIP not set")
	}
	archive, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	input, err := skillrelease.ParseHandoffInput(archive)
	if err != nil {
		t.Fatal(err)
	}
	h := newSkillHarness(t)
	token := issueSkillToken(t, h.server)
	digest := h.publishToGitHub(input.ReleaseID, archive, skillsourcetest.Release{Commit: input.SourceCommit})
	registered := skillCall(t, h.server, token, "skill_publish.register", map[string]any{"releaseId": input.ReleaseID, "expectedArchiveSha256": digest})
	if registered.IsError {
		t.Fatalf("register=%s", registered.Content)
	}
	expectStatus(t, request(t, h.server, http.MethodPost, "/api/control/ops/v2/skill-releases/"+input.ReleaseID+"/publish", nil), http.StatusOK)
	expectStatus(t, request(t, h.server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_northshore"}), http.StatusOK)
	for _, f := range input.Files {
		data := expectStatus(t, request(t, h.server, http.MethodGet, "/api/control/market/skill-releases/"+input.ReleaseID+"/files/"+f.Name, nil), http.StatusOK)
		if sum(data) != f.SHA256 || int64(len(data)) != f.Size {
			t.Fatalf("%s digest mismatch", f.Name)
		}
	}
}

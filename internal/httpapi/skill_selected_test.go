package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/skillrelease"
	"github.com/verdantflarehub/verdantflare-service-control/internal/skillsource/skillsourcetest"
)

func TestSelectedSkillMCPRoundTrip(t *testing.T) {
	h := newSkillHarness(t)
	token := issueSkillToken(t, h.server)
	id := "0.1.0-artifact-test.1"
	data := syntheticSkillInput(t, id, inputOptions{install: "# Test guide\n", commit: sourceCommit, schema: "vf.skills.handoff/v3", ids: []string{"verdantflare-artifact"}})
	digest := h.publishToGitHub(id, data)
	args := map[string]any{"releaseId": id, "expectedArchiveSha256": digest}
	for _, tool := range []string{"skill_publish.register", "skill_publish.readiness", "skill_publish.publish"} {
		call := args
		if tool == "skill_publish.readiness" {
			call = map[string]any{"releaseId": id}
		}
		result := skillCall(t, h.server, token, tool, call)
		if result.IsError {
			t.Fatalf("%s: %s", tool, result.Content)
		}
	}
	expectStatus(t, request(t, h.server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_northshore"}), http.StatusOK)
	for _, name := range []string{"verdantflare-artifact.zip", "Install.md", "USER-SKILLS.md", "release-manifest.json"} {
		res := request(t, h.server, http.MethodGet, "/api/control/market/skill-releases/"+id+"/files/"+name, nil)
		data := expectStatus(t, res, http.StatusOK)
		if sum(data) != res.Header.Get("X-Content-SHA256") {
			t.Fatal("digest mismatch")
		}
	}
	expectStatus(t, request(t, h.server, http.MethodGet, "/api/control/market/skill-releases/"+id+"/files/verdantflare-image.zip", nil), http.StatusNotFound)
	expectStatus(t, request(t, h.server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_verdantflare"}), http.StatusOK)
	args["reason"] = "单技能闭环测试结束"
	if result := skillCall(t, h.server, token, "skill_publish.withdraw", args); result.IsError {
		t.Fatal(string(result.Content))
	}
	expectStatus(t, request(t, h.server, http.MethodGet, "/api/control/market/skill-releases/"+id+"/files/verdantflare-artifact.zip", nil), http.StatusGone)
}

func TestSelectedSkillRequiresCatalogRecord(t *testing.T) {
	for _, id := range []string{"unknown-skill", "b01-image-character"} {
		t.Run(id, func(t *testing.T) {
			h := newSkillHarness(t)
			token := issueSkillToken(t, h.server)
			// Include the dependency so this case reaches the catalog checks.
			data := syntheticSkillInput(t, "0.1.0-unknown", inputOptions{install: "#\n", commit: sourceCommit, schema: "vf.skills.handoff/v3", ids: []string{id, "verdantflare-artifact"}})
			digest := h.publishToGitHub("0.1.0-unknown", data)
			result := skillCall(t, h.server, token, "skill_publish.register", map[string]any{"releaseId": "0.1.0-unknown", "expectedArchiveSha256": digest})
			if !result.IsError || result.Failure.Status != 422 {
				t.Fatalf("catalog accepted: %s", result.Content)
			}
			list := skillCall(t, h.server, token, "skill_publish.list", map[string]any{})
			var got struct {
				Items []json.RawMessage `json:"items"`
			}
			if json.Unmarshal(list.Content, &got) != nil || len(got.Items) != 0 || bytes.Contains(list.Content, []byte("0.1.0-unknown")) {
				t.Fatal("partial candidate persisted")
			}
		})
	}
}

func TestSelectedSkillAcceptsNewCatalogID(t *testing.T) {
	h := newSkillHarness(t)
	token := issueSkillToken(t, h.server)
	expectStatus(t, request(t, h.server, http.MethodPost, "/api/control/ops/v2/skills", map[string]any{
		"entryId": "vf-release-test", "name": "Release test", "summary": "Test summary", "description": "Test description", "category": "Test",
	}), http.StatusCreated)
	data := syntheticSkillInput(t, "0.1.0-new", inputOptions{install: "# Test\n", commit: sourceCommit,
		schema: "vf.skills.handoff/v3", ids: []string{"vf-release-test", "verdantflare-artifact"}})
	digest := h.publishToGitHub("0.1.0-new", data)
	result := skillCall(t, h.server, token, "skill_publish.register", map[string]any{"releaseId": "0.1.0-new", "expectedArchiveSha256": digest})
	if result.IsError {
		t.Fatal(string(result.Content))
	}
}

func TestSelectedDevelopmentInputRefused(t *testing.T) {
	path := os.Getenv("VF_SELECTED_SKILL_INPUT_ZIP")
	if path == "" {
		t.Skip("VF_SELECTED_SKILL_INPUT_ZIP not set")
	}
	archive, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	input, err := skillrelease.ParseHandoffInput(archive)
	if err != nil {
		t.Fatal(err)
	}
	if !input.SourceDirty {
		t.Skip("this guard requires a dirty development archive")
	}
	h := newSkillHarness(t)
	token := issueSkillToken(t, h.server)
	digest := h.publishToGitHub(input.ReleaseID, archive, skillsourcetest.Release{Commit: input.SourceCommit})
	expectToolError(t, skillCall(t, h.server, token, "skill_publish.register", map[string]any{"releaseId": input.ReleaseID, "expectedArchiveSha256": digest}), 422, "skill_source_untrusted")
}

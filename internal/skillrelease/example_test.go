package skillrelease

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestSafePath(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "a/../b", "a\\b", "C:/file", "a/", ""} {
		if safePath(name) {
			t.Errorf("accepted unsafe path %q", name)
		}
	}
	if !safePath("candidate/packages/verdantflare-project.zip") {
		t.Fatal("rejected normal package path")
	}
}

func TestDependencyAndSupportGuards(t *testing.T) {
	if err := verifyDependencies(map[string]candidatePack{
		"a": {skill: skill{Requires: []string{"b"}}},
		"b": {skill: skill{Requires: []string{"a"}}},
	}); err == nil {
		t.Fatal("accepted dependency cycle")
	}
	if err := verifyDependencies(map[string]candidatePack{
		"a": {skill: skill{Requires: []string{"missing"}}},
	}); err == nil {
		t.Fatal("accepted missing dependency")
	}
	if err := verifySupport([]supportStatus{{Host: "codex", OS: "macos", Status: "passed"}}); err == nil {
		t.Fatal("accepted forged support status")
	}
}

func TestDuplicateArchiveEntry(t *testing.T) {
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for range 2 {
		entry, err := w.Create("candidate/release-manifest.json")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("{}")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := archiveEntries(out.Bytes()); err == nil {
		t.Fatal("accepted duplicate archive entry")
	}
}

func TestSyntheticEngineeringExample(t *testing.T) {
	rows := make([]supportStatus, 0, 10)
	for _, host := range []string{"codex", "claude-code", "antigravity", "workbuddy", "deepseek-desktop"} {
		for _, system := range []string{"windows", "macos"} {
			rows = append(rows, supportStatus{Host: host, OS: system, Status: "not_run"})
		}
	}
	releaseID := "0.2.0-integration.6"
	c := candidate{
		SchemaVersion: "vf.skills.handoff/v2", ReleaseID: releaseID,
		ReleaseStage: "integration-candidate", Authentication: "unsigned",
		ContentReview: "pending", SupportMatrix: rows,
		SourceLockSHA256: hash([]byte("{}")),
		Limitations:      []string{"no_signature_or_download_authorization", "no_desktop_acceptance"},
	}
	c.Components.Installer = "missing"
	c.Components.Profiles = "missing"
	c.Components.UserGuides = "missing"
	e := example{
		Schema: "vf.hub-development-example/v1", Sample: true,
		CandidateReleaseID: releaseID, Distribution: "unavailable",
		SupportMatrix: rows,
	}
	e.Installer.Status = "missing"
	files := map[string][]byte{"candidate/source-lock.json": []byte("{}")}
	for id := range skillIDs {
		skillFile := []byte("# " + id + "\n")
		packageBytes := makeZIP(t, map[string][]byte{id + "/SKILL.md": skillFile})
		manifestBytes := mustJSON(t, map[string]any{
			"skill_id": id,
			"files": []map[string]any{{
				"path": id + "/SKILL.md", "sha256": hash(skillFile), "size": len(skillFile),
			}},
		})
		candidateArtifact := artifact{
			Path: "packages/" + id + ".zip", SHA256: hash(packageBytes),
			Size: uint64(len(packageBytes)), FileManifest: "manifests/" + id + ".json",
			FileManifestSHA256: hash(manifestBytes),
		}
		files["candidate/"+candidateArtifact.Path] = packageBytes
		files["candidate/"+candidateArtifact.FileManifest] = manifestBytes
		c.Packages = append(c.Packages, candidatePack{
			skill:  skill{ID: id, Version: releaseID, Artifact: candidateArtifact},
			Status: "assembled",
		})
		publicArtifact := candidateArtifact
		publicArtifact.Path = "candidate/" + publicArtifact.Path
		e.Skills = append(e.Skills, skill{ID: id, Version: releaseID, Artifact: publicArtifact})
	}
	manifestBytes := mustJSON(t, c)
	e.CandidateManifestSHA256 = hash(manifestBytes)
	files["candidate/release-manifest.json"] = manifestBytes
	files["hub-example.json"] = mustJSON(t, e)
	data := makeZIP(t, files)
	receipt, err := VerifyEngineeringExample(data, hash(data))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.SkillCount != 11 || receipt.Installable || receipt.Distribution != "unavailable" {
		t.Fatalf("unsafe synthetic receipt: %+v", receipt)
	}
	assertRejectedMutations(t, data)
}

// The real fixture is intentionally not committed: it contains executable
// skill scripts. Set CONTROL_TEST_SKILL_EXAMPLE_ZIP to the verified local copy.
func TestLunaEngineeringExample(t *testing.T) {
	fixture := os.Getenv("CONTROL_TEST_SKILL_EXAMPLE_ZIP")
	if fixture == "" {
		t.Skip("Luna engineering archive not provided")
	}
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	expected := os.Getenv("CONTROL_TEST_SKILL_EXAMPLE_SHA256")
	if expected == "" {
		expected = "4b25e83038c12b596f2a9d2a2c4a46f24d3bb1be51bfbaa739ee208daae3f721"
	}
	receipt, err := VerifyEngineeringExample(data, expected)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.SkillCount != 11 || receipt.CandidateReleaseID != "0.2.0-integration.6" ||
		receipt.Distribution != "unavailable" || receipt.Installable {
		t.Fatalf("unsafe receipt: %+v", receipt)
	}
	if _, err := VerifyEngineeringExample(data, hash([]byte("wrong"))); err == nil {
		t.Fatal("accepted incorrect external digest")
	}
	assertRejectedMutations(t, data)
}

func assertRejectedMutations(t *testing.T, data []byte) {
	t.Helper()
	tests := []struct {
		name   string
		mutate func(string, []byte) (string, []byte, bool)
	}{
		{"path traversal", func(name string, body []byte) (string, []byte, bool) {
			if name == "hub-example.json" {
				return "../hub-example.json", body, true
			}
			return name, body, true
		}},
		{"missing package", func(name string, body []byte) (string, []byte, bool) {
			return name, body, name != "candidate/packages/verdantflare-project.zip"
		}},
		{"corrupt package", func(name string, body []byte) (string, []byte, bool) {
			if name == "candidate/packages/verdantflare-project.zip" {
				body = append([]byte(nil), body...)
				body[len(body)-1] ^= 1
			}
			return name, body, true
		}},
		{"forged installable", func(name string, body []byte) (string, []byte, bool) {
			if name == "hub-example.json" {
				var item map[string]any
				if err := json.Unmarshal(body, &item); err != nil {
					t.Fatal(err)
				}
				item["installable"] = true
				body, _ = json.Marshal(item)
			}
			return name, body, true
		}},
		{"forged host support", func(name string, body []byte) (string, []byte, bool) {
			if name == "hub-example.json" {
				var item map[string]any
				if err := json.Unmarshal(body, &item); err != nil {
					t.Fatal(err)
				}
				matrix := item["supportMatrix"].([]any)
				matrix[0].(map[string]any)["status"] = "passed"
				body, _ = json.Marshal(item)
			}
			return name, body, true
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := rewriteArchive(t, data, tt.mutate)
			if _, err := VerifyEngineeringExample(changed, hash(changed)); err == nil {
				t.Fatal("accepted mutated archive")
			}
		})
	}
}

func rewriteArchive(t *testing.T, data []byte, mutate func(string, []byte) (string, []byte, bool)) []byte {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body := new(bytes.Buffer)
		if _, err := body.ReadFrom(r); err != nil {
			t.Fatal(err)
		}
		r.Close()
		name, content, include := mutate(f.Name, body.Bytes())
		if !include {
			continue
		}
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func makeZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for name, data := range files {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

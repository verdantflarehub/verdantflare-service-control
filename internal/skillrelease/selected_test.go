package skillrelease

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func selectedArchive(t *testing.T, schema string, ids []string, deps map[string][]string, skillMD bool) []byte {
	t.Helper()
	lock := mustJSON(t, map[string]any{"source_repository": map[string]any{"commit": strings.Repeat("c", 40), "dirty": false}})
	files := map[string][]byte{"candidate/source-lock.json": lock}
	c := candidate{SchemaVersion: schema, ReleaseID: "0.1.0-test.1", SourceLockSHA256: hash(lock)}
	for _, id := range ids {
		leaf := "SKILL.md"
		if !skillMD {
			leaf = "README.md"
		}
		content := []byte("# " + id + "\n")
		pkg := makeZIP(t, map[string][]byte{id + "/" + leaf: content})
		manifest := mustJSON(t, map[string]any{"skill_id": id, "files": []map[string]any{{"path": id + "/" + leaf, "size": len(content), "sha256": hash(content)}}})
		a := artifact{Path: "packages/" + id + ".zip", SHA256: hash(pkg), Size: uint64(len(pkg)), FileManifest: "manifests/" + id + ".json", FileManifestSHA256: hash(manifest)}
		files["candidate/"+a.Path], files["candidate/"+a.FileManifest] = pkg, manifest
		c.Packages = append(c.Packages, candidatePack{skill: skill{ID: id, Version: c.ReleaseID, Requires: deps[id], Artifact: a}, Status: "assembled"})
	}
	raw := mustJSON(t, c)
	files["candidate/release-manifest.json"] = raw
	g := map[string]any{}
	for _, name := range guideNames {
		data := []byte("# Guide " + c.ReleaseID + "\n")
		files["guides/"+name] = data
		g[name] = map[string]any{"sha256": hash(data), "size": len(data)}
	}
	files["guides/guide-manifest.json"] = mustJSON(t, map[string]any{"release_id": c.ReleaseID, "candidate_manifest_sha256": hash(raw), "files": g})
	return makeZIP(t, files)
}

func TestSelectedSkillCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, schema string
		ids          []string
		deps         map[string][]string
		md, want     bool
	}{
		{"single", "vf.skills.handoff/v3", []string{"verdantflare-artifact"}, nil, true, true},
		{"closure", "vf.skills.handoff/v3", []string{"verdantflare-artifact", "verdantflare-project"}, map[string][]string{"verdantflare-project": {"verdantflare-artifact"}}, true, true},
		{"legacy-full", "vf.skills.handoff/v2", SkillIDs(), nil, true, true},
		{"legacy-truncated", "vf.skills.handoff/v2", []string{"verdantflare-artifact"}, nil, true, false},
		{"unknown-schema", "vf.skills.handoff/v4", []string{"verdantflare-artifact"}, nil, true, false},
		{"empty", "vf.skills.handoff/v3", nil, nil, true, false},
		{"duplicate", "vf.skills.handoff/v3", []string{"verdantflare-artifact", "verdantflare-artifact"}, nil, true, false},
		{"invalid-id", "vf.skills.handoff/v3", []string{"Bad_ID"}, nil, true, false},
		{"missing-skill-md", "vf.skills.handoff/v3", []string{"verdantflare-artifact"}, nil, false, false},
		{"missing-dependency", "vf.skills.handoff/v3", []string{"verdantflare-project"}, map[string][]string{"verdantflare-project": {"verdantflare-artifact"}}, true, false},
		{"duplicate-dependency", "vf.skills.handoff/v3", []string{"verdantflare-artifact", "verdantflare-project"}, map[string][]string{"verdantflare-project": {"verdantflare-artifact", "verdantflare-artifact"}}, true, false},
		{"cycle", "vf.skills.handoff/v3", []string{"verdantflare-artifact", "verdantflare-project"}, map[string][]string{"verdantflare-project": {"verdantflare-artifact"}, "verdantflare-artifact": {"verdantflare-project"}}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := selectedArchive(t, tc.schema, tc.ids, tc.deps, tc.md)
			in, err := ParseHandoffInput(data)
			if (err == nil) != tc.want {
				t.Fatalf("want %v got %v", tc.want, err)
			}
			if tc.want && len(in.Files) != len(tc.ids)+3 {
				t.Fatal("unexpected files")
			}
			if tc.want {
				for _, name := range []string{"guides/Install.md", "candidate/packages/" + tc.ids[0] + ".zip"} {
					if _, err := ParseHandoffInput(rewriteEntry(t, data, name)); err == nil {
						t.Fatal("accepted tampered bytes")
					}
				}
			}
		})
	}
}

func TestSelectedInputFromBuilder(t *testing.T) {
	name := os.Getenv("VF_SELECTED_SKILL_INPUT_ZIP")
	if name == "" {
		t.Skip("VF_SELECTED_SKILL_INPUT_ZIP not set")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	in, err := ParseHandoffInput(data)
	if err != nil {
		t.Fatal(err)
	}
	if in.SchemaVersion != "vf.skills.handoff/v3" || len(in.Files) != 4 {
		t.Fatalf("unexpected input: %s %d", in.SchemaVersion, len(in.Files))
	}
	for _, f := range in.Files {
		if hash(f.Data) != f.SHA256 {
			t.Fatal("bad bytes")
		}
	}
	var manifest map[string]any
	for _, f := range in.Files {
		if f.Kind == "manifest" {
			_ = json.Unmarshal(f.Data, &manifest)
		}
	}
	if manifest["authentication"] != "unsigned" {
		t.Fatal("test candidate must remain unsigned")
	}
}

func TestSelectedSkillCountLimit(t *testing.T) {
	ids := make([]string, 33)
	for i := range ids {
		ids[i] = fmt.Sprintf("skill-%02d", i)
	}
	for _, count := range []int{32, 33} {
		_, err := ParseHandoffInput(selectedArchive(t, "vf.skills.handoff/v3", ids[:count], nil, true))
		if (err == nil) != (count == 32) {
			t.Fatalf("count=%d err=%v", count, err)
		}
	}
}

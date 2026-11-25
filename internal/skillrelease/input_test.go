package skillrelease

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"testing"
)

// The 0.2.9 handoff archive is private; set VF_SKILL_INPUT_ZIP to run this check.
func TestParseHandoffInputFromLunaArchive(t *testing.T) {
	path := os.Getenv("VF_SKILL_INPUT_ZIP")
	if path == "" {
		t.Skip("VF_SKILL_INPUT_ZIP not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	in, err := ParseHandoffInput(data)
	if err != nil {
		t.Fatal(err)
	}
	if in.ReleaseID == "" || len(in.Files) != 14 {
		t.Fatalf("release=%q files=%d", in.ReleaseID, len(in.Files))
	}
	for _, f := range in.Files {
		if f.SHA256 != hash(f.Data) || f.Size != int64(len(f.Data)) {
			t.Fatalf("%s digest mismatch", f.Name)
		}
	}
	for _, target := range []string{"candidate/packages/verdantflare-artifact.zip", "guides/Install.md"} {
		if _, err := ParseHandoffInput(rewriteEntry(t, data, target)); err == nil {
			t.Fatalf("tampered %s accepted", target)
		}
	}
}

func rewriteEntry(t *testing.T, data []byte, target string) []byte {
	t.Helper()
	src, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, f := range src.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, _ := io.ReadAll(r)
		r.Close()
		if f.Name == target {
			content = append(content, 'x')
		}
		dst, err := w.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		dst.Write(content)
	}
	w.Close()
	return out.Bytes()
}

func TestParseHandoffInputRejectsGarbage(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("not a zip")} {
		if _, err := ParseHandoffInput(data); err == nil {
			t.Fatal("garbage accepted")
		}
	}
}

func TestValidReleaseID(t *testing.T) {
	for id, want := range map[string]bool{"0.2.9-integration.1": true, "1.0.0": true, "": false, "../x": false, "A1": false, "a/b": false} {
		if validReleaseID(id) != want {
			t.Fatalf("%q want %v", id, want)
		}
	}
}

// Package skillrelease checks untrusted engineering handoff bytes without
// granting publication or download authority.
package skillrelease

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

const (
	maxArchiveBytes = 32 << 20
	maxEntries      = 256
	maxEntryBytes   = 16 << 20
	maxTotalBytes   = 64 << 20
)

var skillIDs = map[string]bool{
	"verdantflare-artifact": true, "verdantflare-project": true,
	"verdantflare-blender": true, "verdantflare-image": true,
	"verdantflare-video": true, "verdantflare-music": true,
	"verdantflare-speech": true, "verdantflare-character-face": true,
	"verdantflare-reference-learning": true, "verdantflare-music-mv": true,
	"x-verdantflare-short-drama": true,
}

type artifact struct {
	Path               string `json:"path"`
	SHA256             string `json:"sha256"`
	Size               uint64 `json:"size"`
	FileManifest       string `json:"file_manifest"`
	FileManifestSHA256 string `json:"file_manifest_sha256"`
}

type skill struct {
	ID       string   `json:"id"`
	Version  string   `json:"version"`
	Requires []string `json:"requires"`
	Artifact artifact `json:"artifact"`
}

type example struct {
	Schema                  string  `json:"schema"`
	Sample                  bool    `json:"sample"`
	CandidateReleaseID      string  `json:"candidateReleaseId"`
	CandidateManifestSHA256 string  `json:"candidateManifestSha256"`
	PublishedReleaseID      *string `json:"publishedReleaseId"`
	Distribution            string  `json:"distribution"`
	Installable             bool    `json:"installable"`
	ProductionUsable        bool    `json:"production_usable"`
	Installer               struct {
		Status string  `json:"status"`
		Path   *string `json:"path"`
		SHA256 *string `json:"sha256"`
	} `json:"installer"`
	Skills        []skill         `json:"skills"`
	SupportMatrix []supportStatus `json:"supportMatrix"`
}

type candidate struct {
	SchemaVersion      string   `json:"schema_version"`
	ReleaseID          string   `json:"release_id"`
	ReleaseStage       string   `json:"release_stage"`
	Authentication     string   `json:"authentication"`
	ContentReview      string   `json:"content_review"`
	PublicDistribution bool     `json:"public_distribution"`
	Installable        bool     `json:"installable"`
	SourceLockSHA256   string   `json:"source_lock_sha256"`
	Limitations        []string `json:"limitations"`
	Components         struct {
		Installer  string `json:"installer"`
		Profiles   string `json:"profiles"`
		UserGuides string `json:"user_guides"`
	} `json:"components"`
	Packages      []candidatePack `json:"packages"`
	SupportMatrix []supportStatus `json:"support_matrix"`
}

type candidatePack struct {
	skill
	Status   string   `json:"status"`
	Blockers []string `json:"blockers"`
}

type supportStatus struct {
	Host       string  `json:"host"`
	OS         string  `json:"os"`
	Status     string  `json:"status"`
	AppVersion *string `json:"app_version"`
}

type fileManifest struct {
	SkillID string `json:"skill_id"`
	Files   []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
		Size   uint64 `json:"size"`
	} `json:"files"`
}

// EngineeringReceipt is an integrity result only. It must never be used as a
// publication approval or an authorization to serve package bytes.
type EngineeringReceipt struct {
	CandidateReleaseID string
	ArchiveSHA256      string
	ManifestSHA256     string
	SkillCount         int
	Distribution       string
	Installable        bool
}

// VerifyEngineeringExample verifies Luna's unsigned example from already
// obtained bytes. expectedSHA256 must come from a separately authenticated
// source; this function does not authenticate the publisher or fetch a URL.
func VerifyEngineeringExample(data []byte, expectedSHA256 string) (EngineeringReceipt, error) {
	var receipt EngineeringReceipt
	if len(data) == 0 || len(data) > maxArchiveBytes || !validDigest(expectedSHA256) {
		return receipt, errors.New("skill handoff: invalid archive size or expected digest")
	}
	digest := hash(data)
	if digest != strings.ToLower(expectedSHA256) {
		return receipt, errors.New("skill handoff: outer digest mismatch")
	}
	outer, err := archiveEntries(data)
	if err != nil {
		return receipt, err
	}
	var e example
	if err := decodeEntry(outer, "hub-example.json", &e); err != nil {
		return receipt, err
	}
	if e.Schema != "vf.hub-development-example/v1" || !e.Sample || e.CandidateReleaseID == "" ||
		e.PublishedReleaseID != nil || e.Distribution != "unavailable" || e.Installable ||
		e.ProductionUsable || e.Installer.Status != "missing" ||
		e.Installer.Path != nil || e.Installer.SHA256 != nil {
		return receipt, errors.New("skill handoff: example claims unsupported distribution")
	}
	if err := verifySupport(e.SupportMatrix); err != nil {
		return receipt, err
	}
	manifestBytes, err := readEntry(outer, "candidate/release-manifest.json")
	if err != nil {
		return receipt, err
	}
	if hash(manifestBytes) != e.CandidateManifestSHA256 {
		return receipt, errors.New("skill handoff: candidate manifest digest mismatch")
	}
	var c candidate
	if err := json.Unmarshal(manifestBytes, &c); err != nil {
		return receipt, fmt.Errorf("skill handoff: invalid candidate manifest: %w", err)
	}
	if c.SchemaVersion != "vf.skills.handoff/v2" || c.ReleaseID != e.CandidateReleaseID ||
		c.ReleaseStage != "integration-candidate" || c.Authentication != "unsigned" ||
		c.ContentReview != "pending" || c.PublicDistribution || c.Installable ||
		c.Components.Installer != "missing" || c.Components.Profiles != "missing" ||
		c.Components.UserGuides != "missing" ||
		!contains(c.Limitations, "no_signature_or_download_authorization") ||
		!contains(c.Limitations, "no_desktop_acceptance") {
		return receipt, errors.New("skill handoff: candidate claims unsupported distribution")
	}
	if err := verifySupport(c.SupportMatrix); err != nil {
		return receipt, err
	}
	if !validDigest(c.SourceLockSHA256) {
		return receipt, errors.New("skill handoff: invalid source lock digest")
	}
	lockBytes, err := readEntry(outer, "candidate/source-lock.json")
	if err != nil || hash(lockBytes) != c.SourceLockSHA256 {
		return receipt, errors.New("skill handoff: source lock digest mismatch")
	}
	if len(e.Skills) != len(skillIDs) || len(c.Packages) != len(skillIDs) {
		return receipt, errors.New("skill handoff: expected exactly 11 skills")
	}
	byID := make(map[string]candidatePack, len(c.Packages))
	for _, p := range c.Packages {
		if !skillIDs[p.ID] || p.Version != c.ReleaseID || p.Status != "assembled" || len(p.Blockers) != 0 {
			return receipt, errors.New("skill handoff: invalid candidate package")
		}
		if _, exists := byID[p.ID]; exists {
			return receipt, errors.New("skill handoff: duplicate skill")
		}
		byID[p.ID] = p
		if err := verifyPackage(outer, p); err != nil {
			return receipt, err
		}
	}
	seen := make(map[string]bool, len(e.Skills))
	for _, s := range e.Skills {
		p, ok := byID[s.ID]
		if !ok || seen[s.ID] || s.Version != p.Version || !sameStrings(s.Requires, p.Requires) ||
			s.Artifact != (artifact{Path: "candidate/" + p.Artifact.Path, SHA256: p.Artifact.SHA256,
				Size: p.Artifact.Size, FileManifest: p.Artifact.FileManifest,
				FileManifestSHA256: p.Artifact.FileManifestSHA256}) {
			return receipt, errors.New("skill handoff: example and candidate package disagree")
		}
		seen[s.ID] = true
	}
	if err := verifyDependencies(byID); err != nil {
		return receipt, err
	}
	return EngineeringReceipt{
		CandidateReleaseID: c.ReleaseID, ArchiveSHA256: digest,
		ManifestSHA256: e.CandidateManifestSHA256, SkillCount: len(c.Packages),
		Distribution: "unavailable", Installable: false,
	}, nil
}

func archiveEntries(data []byte) (map[string]*zip.File, error) {
	if len(data) > maxArchiveBytes {
		return nil, errors.New("skill handoff: archive too large")
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(z.File) > maxEntries {
		return nil, errors.New("skill handoff: invalid archive")
	}
	entries := make(map[string]*zip.File, len(z.File))
	var total uint64
	for _, f := range z.File {
		name := f.Name
		if !safePath(name) || f.FileInfo().IsDir() || f.Mode()&os.ModeSymlink != 0 ||
			f.UncompressedSize64 > maxEntryBytes || total+f.UncompressedSize64 > maxTotalBytes {
			return nil, errors.New("skill handoff: unsafe archive entry")
		}
		if _, exists := entries[name]; exists {
			return nil, errors.New("skill handoff: duplicate archive entry")
		}
		total += f.UncompressedSize64
		entries[name] = f
	}
	return entries, nil
}

func safePath(name string) bool {
	return name != "" && !strings.HasPrefix(name, "/") && !strings.Contains(name, "\\") &&
		!strings.Contains(name, ":") && path.Clean(name) == name &&
		name != ".." && !strings.HasPrefix(name, "../") &&
		!strings.HasSuffix(name, "/")
}

func readEntry(entries map[string]*zip.File, name string) ([]byte, error) {
	f, ok := entries[name]
	if !ok {
		return nil, fmt.Errorf("skill handoff: missing %s", name)
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, maxEntryBytes+1))
	if err != nil || len(data) > maxEntryBytes || uint64(len(data)) != f.UncompressedSize64 {
		return nil, errors.New("skill handoff: entry read failed or exceeded limit")
	}
	return data, nil
}

func decodeEntry(entries map[string]*zip.File, name string, out any) error {
	data, err := readEntry(entries, name)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func verifyPackage(outer map[string]*zip.File, p candidatePack) error {
	a := p.Artifact
	if a.Path != "packages/"+p.ID+".zip" || a.FileManifest != "manifests/"+p.ID+".json" ||
		!validDigest(a.SHA256) || !validDigest(a.FileManifestSHA256) || a.Size == 0 {
		return errors.New("skill handoff: invalid package artifact")
	}
	manifestBytes, err := readEntry(outer, "candidate/"+a.FileManifest)
	if err != nil || hash(manifestBytes) != a.FileManifestSHA256 {
		return errors.New("skill handoff: file manifest digest mismatch")
	}
	var m fileManifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil || m.SkillID != p.ID || len(m.Files) == 0 {
		return errors.New("skill handoff: invalid file manifest")
	}
	packageBytes, err := readEntry(outer, "candidate/"+a.Path)
	if err != nil || uint64(len(packageBytes)) != a.Size || hash(packageBytes) != a.SHA256 {
		return errors.New("skill handoff: package digest or size mismatch")
	}
	inner, err := archiveEntries(packageBytes)
	if err != nil || len(inner) != len(m.Files) {
		return errors.New("skill handoff: package file count mismatch")
	}
	seen := make(map[string]bool, len(m.Files))
	for _, file := range m.Files {
		if !strings.HasPrefix(file.Path, p.ID+"/") || seen[file.Path] || !validDigest(file.SHA256) {
			return errors.New("skill handoff: invalid package file path or digest")
		}
		seen[file.Path] = true
		content, err := readEntry(inner, file.Path)
		if err != nil || uint64(len(content)) != file.Size || hash(content) != file.SHA256 {
			return errors.New("skill handoff: package file digest or size mismatch")
		}
	}
	return nil
}

func verifyDependencies(packages map[string]candidatePack) error {
	state := map[string]uint8{}
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return errors.New("skill handoff: dependency cycle")
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		for _, dep := range packages[id].Requires {
			if dep == id {
				return errors.New("skill handoff: self dependency")
			}
			if _, ok := packages[dep]; !ok {
				return errors.New("skill handoff: missing dependency")
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for id := range packages {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func verifySupport(rows []supportStatus) error {
	hosts := map[string]bool{"codex": true, "claude-code": true, "antigravity": true, "workbuddy": true, "deepseek-desktop": true}
	seen := map[string]bool{}
	for _, row := range rows {
		key := row.Host + "/" + row.OS
		if !hosts[row.Host] || (row.OS != "windows" && row.OS != "macos") ||
			row.Status != "not_run" || row.AppVersion != nil || seen[key] {
			return errors.New("skill handoff: unsupported host evidence claim")
		}
		seen[key] = true
	}
	if len(seen) != 10 {
		return errors.New("skill handoff: incomplete host evidence matrix")
	}
	return nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func validDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

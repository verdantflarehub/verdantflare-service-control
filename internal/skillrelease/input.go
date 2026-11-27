package skillrelease

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// File is one fixed byte object of a skill release that members can download.
type File struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	SkillID  string   `json:"skillId,omitempty"`
	Requires []string `json:"requires,omitempty"`
	SHA256   string   `json:"sha256"`
	Size     int64    `json:"size"`
	Data     []byte   `json:"-"`
}

// Input is a verified handoff archive: 11 skill ZIPs plus the same-release guides.
type Input struct {
	ReleaseID      string
	Stage          string
	Authentication string
	ManifestSHA256 string
	ArchiveSHA256  string
	// SourceCommit and SourceDirty come from the source lock; they say which
	// skills repository commit the packages were built from.
	SourceCommit string
	SourceDirty  bool
	Files        []File
}

type guideManifest struct {
	ReleaseID               string `json:"release_id"`
	CandidateManifestSHA256 string `json:"candidate_manifest_sha256"`
	Files                   map[string]struct {
		SHA256 string `json:"sha256"`
		Size   uint64 `json:"size"`
	} `json:"files"`
}

var guideNames = []string{"Install.md", "USER-SKILLS.md"}

// ParseHandoffInput checks a `vf-hub-skill-input-*` archive and returns the
// fixed files to store. It verifies integrity only; it does not authenticate
// the publisher. Publication remains an explicit skill_ops_admin action.
func ParseHandoffInput(data []byte) (Input, error) {
	var in Input
	if len(data) == 0 || len(data) > maxArchiveBytes {
		return in, errors.New("skill input: archive is empty or too large")
	}
	outer, err := archiveEntries(data)
	if err != nil {
		return in, err
	}
	manifestBytes, err := readEntry(outer, "candidate/release-manifest.json")
	if err != nil {
		return in, err
	}
	var c candidate
	if err := json.Unmarshal(manifestBytes, &c); err != nil {
		return in, errors.New("skill input: invalid release manifest")
	}
	if c.SchemaVersion != "vf.skills.handoff/v2" || !validReleaseID(c.ReleaseID) {
		return in, errors.New("skill input: unsupported manifest schema or release id")
	}
	if !validDigest(c.SourceLockSHA256) {
		return in, errors.New("skill input: invalid source lock digest")
	}
	lockBytes, err := readEntry(outer, "candidate/source-lock.json")
	if err != nil || hash(lockBytes) != c.SourceLockSHA256 {
		return in, errors.New("skill input: source lock digest mismatch")
	}
	var lock struct {
		SourceRepository *struct {
			Commit string `json:"commit"`
			Dirty  *bool  `json:"dirty"`
		} `json:"source_repository"`
	}
	_ = json.Unmarshal(lockBytes, &lock)
	sourceCommit, sourceDirty := "", true
	if lock.SourceRepository != nil && lock.SourceRepository.Dirty != nil {
		sourceCommit, sourceDirty = lock.SourceRepository.Commit, *lock.SourceRepository.Dirty
	}
	if len(c.Packages) != len(skillIDs) {
		return in, errors.New("skill input: expected exactly 11 skills")
	}
	byID := make(map[string]candidatePack, len(c.Packages))
	for _, p := range c.Packages {
		if !skillIDs[p.ID] || p.Version != c.ReleaseID || p.Status != "assembled" || len(p.Blockers) != 0 {
			return in, errors.New("skill input: invalid package " + p.ID)
		}
		if _, exists := byID[p.ID]; exists {
			return in, errors.New("skill input: duplicate skill")
		}
		if err := verifyPackage(outer, p); err != nil {
			return in, err
		}
		byID[p.ID] = p
	}
	if err := verifyDependencies(byID); err != nil {
		return in, err
	}
	manifestDigest := hash(manifestBytes)
	var g guideManifest
	if err := decodeEntry(outer, "guides/guide-manifest.json", &g); err != nil {
		return in, errors.New("skill input: missing or invalid guide manifest")
	}
	if g.ReleaseID != c.ReleaseID || g.CandidateManifestSHA256 != manifestDigest {
		return in, errors.New("skill input: guides belong to another release")
	}
	files := make([]File, 0, len(byID)+len(guideNames)+1)
	for _, name := range guideNames {
		want, ok := g.Files[name]
		content, err := readEntry(outer, "guides/"+name)
		if !ok || err != nil || uint64(len(content)) != want.Size || hash(content) != want.SHA256 {
			return in, errors.New("skill input: guide digest mismatch " + name)
		}
		files = append(files, File{Name: name, Kind: "guide", SHA256: want.SHA256, Size: int64(len(content)), Data: content})
	}
	files = append(files, File{Name: "release-manifest.json", Kind: "manifest", SHA256: manifestDigest, Size: int64(len(manifestBytes)), Data: manifestBytes})
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := byID[id]
		content, err := readEntry(outer, "candidate/"+p.Artifact.Path)
		if err != nil {
			return in, err
		}
		files = append(files, File{Name: id + ".zip", Kind: "package", SkillID: id, Requires: append([]string(nil), p.Requires...),
			SHA256: p.Artifact.SHA256, Size: int64(len(content)), Data: content})
	}
	return Input{ReleaseID: c.ReleaseID, Stage: c.ReleaseStage, Authentication: c.Authentication,
		ManifestSHA256: manifestDigest, ArchiveSHA256: hash(data), SourceCommit: sourceCommit, SourceDirty: sourceDirty, Files: files}, nil
}

// ValidReleaseID reports whether id is an acceptable skill release id.
func ValidReleaseID(id string) bool { return validReleaseID(id) }

func validReleaseID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r == '.' || r == '-') {
			return false
		}
	}
	return !strings.HasPrefix(id, ".") && !strings.Contains(id, "..")
}

// SkillIDs returns the 11 user skill ids accepted in a release.
func SkillIDs() []string {
	ids := make([]string, 0, len(skillIDs))
	for id := range skillIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

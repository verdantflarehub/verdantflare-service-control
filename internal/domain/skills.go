package domain

import "time"

// SkillCatalogEntry is editorial directory data, not an installable package.
// A release and download contract will be added only after verified artifacts exist.
type SkillCatalogEntry struct {
	EntryID            string    `json:"entryId"`
	Kind               string    `json:"kind"`
	Name               string    `json:"name"`
	Summary            string    `json:"summary"`
	Description        string    `json:"description"`
	Category           string    `json:"category"`
	SortOrder          int       `json:"sortOrder"`
	Visibility         string    `json:"visibility"`
	Sample             bool      `json:"sample"`
	Revision           int       `json:"revision"`
	Distribution       string    `json:"distribution"`
	PublishedReleaseID *string   `json:"publishedReleaseId"`
	PreviewSkillIDs    []string  `json:"previewSkillIds,omitempty"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type SkillCatalogAuditEvent struct {
	EntryID        string    `json:"entryId"`
	Action         string    `json:"action"`
	ActorSubject   string    `json:"actorSubject"`
	OrganizationID string    `json:"organizationId"`
	Revision       int       `json:"revision"`
	CreatedAt      time.Time `json:"createdAt"`
}

// SkillRelease is one fixed set of skill packages and guides stored by Control.
// Status moves candidate -> published -> withdrawn; bytes never change.
type SkillRelease struct {
	ReleaseID      string              `json:"releaseId"`
	Stage          string              `json:"stage"`
	Authentication string              `json:"authentication"`
	ManifestSHA256 string              `json:"manifestSha256"`
	ArchiveSHA256  string              `json:"archiveSha256"`
	Status         string              `json:"status"`
	Source         *SkillReleaseSource `json:"source"`
	Files          []SkillReleaseFile  `json:"files"`
	CreatedBy      string              `json:"createdBy,omitempty"`
	CreatedAt      time.Time           `json:"createdAt"`
	PublishedAt    *time.Time          `json:"publishedAt"`
	WithdrawnAt    *time.Time          `json:"withdrawnAt"`
}

type SkillReleaseFile struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	SkillID  string   `json:"skillId,omitempty"`
	Requires []string `json:"requires,omitempty"`
	SHA256   string   `json:"sha256"`
	Size     int64    `json:"size"`
	Data     []byte   `json:"-"`
}

// SkillReleaseSource is the receipt of where Control fetched a release from:
// one workflow-built GitHub Release asset of the skills repository.
type SkillReleaseSource struct {
	Repository      string    `json:"repository"`
	Tag             string    `json:"tag"`
	Commit          string    `json:"commit"`
	GitHubReleaseID int64     `json:"githubReleaseId"`
	AssetID         int64     `json:"assetId"`
	Uploader        string    `json:"uploader"`
	Size            int64     `json:"size"`
	SHA256          string    `json:"sha256"`
	FetchedAt       time.Time `json:"fetchedAt"`
}

// SkillReleaseActor says who changed a release and through which entry.
// TokenID and Tool are set only for calls from the skill publishing MCP.
type SkillReleaseActor struct {
	Subject        string
	OrganizationID string
	TokenID        string
	Tool           string
}

type SkillReleaseAuditEvent struct {
	ReleaseID      string    `json:"releaseId"`
	Action         string    `json:"action"`
	FileName       string    `json:"fileName,omitempty"`
	ActorSubject   string    `json:"actorSubject"`
	OrganizationID string    `json:"organizationId"`
	TokenID        string    `json:"tokenId,omitempty"`
	Tool           string    `json:"tool,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}

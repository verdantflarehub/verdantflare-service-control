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

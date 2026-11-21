package domain

import "time"

type CreativeResourceItem struct {
	ItemID      string    `json:"itemId"`
	ResourceID  string    `json:"resourceId"`
	SortOrder   int       `json:"sortOrder"`
	StepLabel   string    `json:"stepLabel,omitempty"`
	Label       string    `json:"label"`
	Engine      string    `json:"engine,omitempty"`
	ModelName   string    `json:"modelName,omitempty"`
	AspectRatio string    `json:"aspectRatio,omitempty"`
	Resolution  string    `json:"resolution,omitempty"`
	EditPrompt  string    `json:"editPrompt,omitempty"`
	Note        string    `json:"note,omitempty"`
	ImageMime   string    `json:"imageMime"`
	ImageSHA256 string    `json:"imageSha256"`
	ImageURL    string    `json:"imageUrl"`
	CreatedAt   time.Time `json:"createdAt"`
}

type CreativeResource struct {
	ResourceID     string                 `json:"resourceId"`
	OrganizationID string                 `json:"organizationId"`
	OwnerSubject   string                 `json:"ownerSubject"`
	Title           string                 `json:"title"`
	Category        string                 `json:"category"`
	SubjectCategory string                 `json:"subjectCategory"`
	MediaType       string                 `json:"mediaType"`
	PostKind       string                 `json:"postKind"`
	Prompt         string                 `json:"prompt"`
	PromptOrigin   string                 `json:"promptOrigin"`
	SourceURL      string                 `json:"sourceUrl,omitempty"`
	SourceAuthor   string                 `json:"sourceAuthor,omitempty"`
	Engine         string                 `json:"engine,omitempty"`
	ModelName      string                 `json:"modelName,omitempty"`
	AspectRatio    string                 `json:"aspectRatio,omitempty"`
	Resolution     string                 `json:"resolution,omitempty"`
	DrivePath      string                 `json:"drivePath,omitempty"`
	ImageMime      string                 `json:"imageMime"`
	ImageSHA256    string                 `json:"imageSha256"`
	ImageURL       string                 `json:"imageUrl"`
	ItemCount      int                    `json:"itemCount"`
	Items          []CreativeResourceItem `json:"items,omitempty"`
	Revision       int                    `json:"revision"`
	Status         string                 `json:"status"`
	CreatedAt      time.Time              `json:"createdAt"`
	UpdatedAt      time.Time              `json:"updatedAt"`
}

type CreativeResourceImage struct {
	Mime   string
	Data   []byte
	SHA256 string
}

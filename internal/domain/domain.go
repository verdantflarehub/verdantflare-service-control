package domain

import (
	"encoding/json"
	"time"
)

type CenterUser struct {
	CenterUserID         string
	LoginSubject         string
	DisplayName          string
	Email                string
	ActiveOrganizationID string
	Memberships          []Membership
}

type Membership struct {
	OrganizationID string   `json:"organizationId"`
	Roles          []string `json:"roles"`
	Status         string   `json:"status,omitempty"`
}

type Organization struct {
	OrganizationID     string   `json:"organizationId"`
	Name               string   `json:"name"`
	ShortName          string   `json:"shortName"`
	Roles              []string `json:"roles,omitempty"`
	Plan               string   `json:"plan"`
	EntitlementVersion int      `json:"entitlementVersion"`
	ExperienceCredits  int      `json:"experienceCredits"`
	APICredits         int      `json:"apiCredits"`
	DefaultRegion      string   `json:"defaultRegion,omitempty"`
	Industry           string   `json:"industry,omitempty"`
	BillingEmail       string   `json:"billingEmail,omitempty"`
	Status             string   `json:"status,omitempty"`
}

type CenterContext struct {
	CenterUserID         string         `json:"centerUserId"`
	LoginSubject         string         `json:"loginSubject"`
	DisplayName          string         `json:"displayName"`
	Email                string         `json:"email"`
	Organizations        []Organization `json:"organizations"`
	ActiveOrganizationID string         `json:"activeOrganizationId"`
}

type Overview struct {
	Organization  Organization `json:"organization"`
	AvailableApps int          `json:"availableApps"`
	PreviewApps   int          `json:"previewApps"`
}

type AppImageRef struct {
	Component string `json:"component" yaml:"component"`
	Image     string `json:"image" yaml:"image"`
}

type LogoRef struct {
	URL       string `json:"url" yaml:"url"`
	Digest    string `json:"digest" yaml:"digest"`
	MediaType string `json:"mediaType" yaml:"mediaType"`
}

type MarketCatalogItem struct {
	AppID          string        `yaml:"appId" json:"appId"`
	DisplayName    string        `yaml:"displayName" json:"displayName"`
	SortOrder      int           `yaml:"sortOrder" json:"sortOrder"`
	GroupID        string        `yaml:"groupId" json:"groupId"`
	Brand          string        `yaml:"brand,omitempty" json:"brand,omitempty"`
	IconRef        string        `yaml:"iconRef,omitempty" json:"iconRef,omitempty"`
	Version        string        `yaml:"version,omitempty" json:"version,omitempty"`
	Description    string        `yaml:"description" json:"description"`
	Models         []string      `yaml:"models" json:"models"`
	Capabilities   []string      `yaml:"capabilities" json:"capabilities"`
	Images         []AppImageRef `yaml:"images" json:"images"`
	Chart          string        `yaml:"chart,omitempty" json:"chart,omitempty"`
	ChartVersion   string        `yaml:"chartVersion,omitempty" json:"chartVersion,omitempty"`
	DisabledReason string        `yaml:"disabledReason,omitempty" json:"disabledReason,omitempty"`
}

type MarketAppsYaml struct {
	SchemaVersion int                 `yaml:"schemaVersion" json:"schemaVersion"`
	Apps          []MarketCatalogItem `yaml:"apps" json:"apps"`
}

type App struct {
	ID                   string        `json:"id"`
	AppID                string        `json:"appId,omitempty"`
	Name                 string        `json:"name"`
	GroupID              string        `json:"groupId,omitempty"`
	SortOrder            int           `json:"sortOrder"`
	Brand                string        `json:"brand,omitempty"`
	IconRef              string        `json:"iconRef,omitempty"`
	Visibility           string        `json:"visibility,omitempty"`
	Enabled              bool          `json:"enabled"`
	DisabledReason       string        `json:"disabledReason,omitempty"`
	PublishedVersion     string        `json:"publishedVersion,omitempty"`
	InstallGrantRequired bool          `json:"installGrantRequired,omitempty"`
	Models               []string      `json:"models,omitempty"`
	Capabilities         []string      `json:"capabilities,omitempty"`
	Images               []AppImageRef `json:"images,omitempty"`
	Logo                 *LogoRef      `json:"logo,omitempty"`
	Revision             int           `json:"revision,omitempty"`
	CreatedAt            string        `json:"createdAt,omitempty"`
	UpdatedAt            string        `json:"updatedAt,omitempty"`
	Category             string        `json:"category"`
	Summary              string        `json:"summary"`
	Version              string        `json:"version"`
	Channel              string        `json:"channel"`
	Status               string        `json:"status"`
	Tone                 string        `json:"tone"`
	Icon                 string        `json:"icon"`
	GPU                  string        `json:"gpu"`
	Duration             string        `json:"duration"`
	Developer            string        `json:"developer,omitempty"`
	Description          string        `json:"description,omitempty"`
	Memory               string        `json:"memory,omitempty"`
	Disk                 string        `json:"disk,omitempty"`
	CPU                  string        `json:"cpu,omitempty"`
	PublicIconURL        string        `json:"publicIconUrl,omitempty"`
	PublicVisible        bool          `json:"publicVisible"`
	Entitled             bool          `json:"entitled"`
	Showcase             AppShowcase   `json:"showcase"`
}

// AppShowcase contains editorial Market content. It is never runtime evidence.
type AppShowcase struct {
	Screenshots []string `json:"screenshots"`
	Highlights  []string `json:"highlights"`
	WhatsNew    string   `json:"whatsNew"`
	Permissions []string `json:"permissions"`
	WebsiteURL  string   `json:"websiteUrl"`
	DocsURL     string   `json:"docsUrl"`
	SourceURL   string   `json:"sourceUrl"`
	License     string   `json:"license"`
	Languages   string   `json:"languages"`
	Platforms   string   `json:"platforms"`
}

// PublicModel contains editorial, public-facing model facts. It is not a
// gateway entitlement, routing, metering or billing record.
type PublicModel struct {
	ID              string   `json:"id"`
	Revision        int      `json:"revision"`
	Name            string   `json:"name"`
	Provider        string   `json:"provider"`
	ProviderIconURL string   `json:"providerIconUrl,omitempty"`
	Summary         string   `json:"summary"`
	Categories      []string `json:"categories"`
	Context         string   `json:"context,omitempty"`
	MaxInput        string   `json:"maxInput,omitempty"`
	MaxOutput       string   `json:"maxOutput,omitempty"`
	InputPrice      string   `json:"inputPrice,omitempty"`
	OutputPrice     string   `json:"outputPrice,omitempty"`
	CachePrice      string   `json:"cachePrice,omitempty"`
	PriceUnit       string   `json:"priceUnit,omitempty"`
	PublicVisible   bool     `json:"publicVisible"`
	ExperienceMode  string   `json:"experienceMode"`
}

type PublicCatalog struct {
	Models []PublicModel `json:"models"`
	Apps   []PublicApp   `json:"apps"`
}

// GuestCatalog exposes editorial Hub directory facts to authenticated users
// who have not yet joined an organization. It carries no entitlement state.
type GuestCatalog struct {
	Models []PublicModel `json:"models"`
	Apps   []GuestApp    `json:"apps"`
}

type GuestApp struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Category    string      `json:"category"`
	Summary     string      `json:"summary"`
	Version     string      `json:"version,omitempty"`
	Channel     string      `json:"channel"`
	Developer   string      `json:"developer,omitempty"`
	Description string      `json:"description,omitempty"`
	Brand       string      `json:"brand,omitempty"`
	IconRef     string      `json:"iconRef,omitempty"`
	SortOrder   int         `json:"sortOrder,omitempty"`
	IconURL     string      `json:"iconUrl,omitempty"`
	Showcase    AppShowcase `json:"showcase"`
}

type ModelProviderIcon struct {
	SHA256      string `json:"sha256"`
	ContentType string `json:"-"`
	Data        []byte `json:"-"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}

type PublicApp struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Summary     string `json:"summary"`
	Version     string `json:"version"`
	Developer   string `json:"developer,omitempty"`
	Description string `json:"description,omitempty"`
	Brand       string `json:"brand,omitempty"`
	IconRef     string `json:"iconRef,omitempty"`
	SortOrder   int    `json:"sortOrder,omitempty"`
	Memory      string `json:"memory,omitempty"`
	Disk        string `json:"disk,omitempty"`
	CPU         string `json:"cpu,omitempty"`
	GPU         string `json:"gpu,omitempty"`
	IconURL     string `json:"iconUrl,omitempty"`
}

type ExperienceSession struct {
	ID             string     `json:"id"`
	OrganizationID string     `json:"organizationId"`
	CenterUserID   string     `json:"centerUserId"`
	AppID          string     `json:"appId"`
	App            string     `json:"app"`
	Region         string     `json:"region"`
	StartedAt      time.Time  `json:"startedAt"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	ClosedAt       *time.Time `json:"closedAt,omitempty"`
	Remaining      string     `json:"remaining"`
	Status         string     `json:"status"`
	Usage          string     `json:"usage"`
	CleanupStatus  string     `json:"cleanupStatus"`
}

// ModelExperienceRun is a short-lived, user-owned paid inference record.
// Prompt and response are removed with the record after ExpiresAt.
type ModelExperienceRun struct {
	ID             string     `json:"id"`
	RequestID      string     `json:"requestId"`
	OrganizationID string     `json:"organizationId"`
	CenterUserID   string     `json:"centerUserId"`
	ModelID        string     `json:"modelId"`
	KeyID          string     `json:"keyId,omitempty"`
	Prompt         string     `json:"prompt"`
	Response       string     `json:"response,omitempty"`
	Status         string     `json:"status"`
	ErrorCode      string     `json:"errorCode,omitempty"`
	PromptTokens   int        `json:"promptTokens,omitempty"`
	OutputTokens   int        `json:"outputTokens,omitempty"`
	TotalTokens    int        `json:"totalTokens,omitempty"`
	BilledQuota    *int       `json:"billedQuota,omitempty"`
	BilledYuan     string     `json:"billedYuan,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
	ExpiresAt      time.Time  `json:"expiresAt"`
}

type Model struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Provider        string `json:"provider"`
	ProviderIconURL string `json:"providerIconUrl,omitempty"`
	Type            string `json:"type"`
	Context         string `json:"context"`
	Latency         string `json:"latency"`
	Price           string `json:"price"`
	Status          string `json:"status"`
	InputPrice      string `json:"inputPrice,omitempty"`
	OutputPrice     string `json:"outputPrice,omitempty"`
	CachePrice      string `json:"cachePrice,omitempty"`
	PriceUnit       string `json:"priceUnit,omitempty"`
	ExperienceMode  string `json:"experienceMode,omitempty"`
}

type APIKey struct {
	ID             string     `json:"id"`
	Source         string     `json:"source,omitempty"`
	OrganizationID string     `json:"organizationId,omitempty"`
	Name           string     `json:"name"`
	Prefix         string     `json:"prefix"`
	Scopes         []string   `json:"scopes"`
	CreatedAt      time.Time  `json:"createdAt"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	LastUsedAt     *time.Time `json:"lastUsedAt,omitempty"`
	Status         string     `json:"status"`
	SecretHash     [32]byte   `json:"-"`
}

// MCPToken lets an AI tool call the Hub Market MCP as the Login user who
// issued it. Only the SHA-256 of the secret is kept.
type MCPToken struct {
	ID           string     `json:"id"`
	LoginSubject string     `json:"-"`
	Purpose      string     `json:"purpose,omitempty"`
	Name         string     `json:"name"`
	Prefix       string     `json:"prefix"`
	CreatedAt    time.Time  `json:"createdAt"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	LastUsedAt   *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt    *time.Time `json:"revokedAt,omitempty"`
	Status       string     `json:"status"`
	SecretHash   [32]byte   `json:"-"`
}

type APITask struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organizationId,omitempty"`
	Model          string `json:"model"`
	Created        string `json:"created"`
	Duration       string `json:"duration"`
	Usage          string `json:"usage"`
	Status         string `json:"status"`
}

type Member struct {
	ID           string `json:"id,omitempty"`
	CenterUserID string `json:"centerUserId,omitempty"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	Joined       string `json:"joined"`
	Status       string `json:"status"`
	Avatar       string `json:"avatar"`
}

type BillingSummary struct {
	Plan           string    `json:"plan"`
	CycleStart     time.Time `json:"cycleStart"`
	CycleEnd       time.Time `json:"cycleEnd"`
	APIBudget      int       `json:"apiBudget"`
	APIUsed        int       `json:"apiUsed"`
	ExperienceUsed int       `json:"experienceUsed"`
	MemberLimit    int       `json:"memberLimit"`
	Invoices       []Invoice `json:"invoices"`
}

// BillingView never represents a credit grant as a paid invoice.
type BillingView struct {
	Plan         string        `json:"plan"`
	Usage        UsageSummary  `json:"usage"`
	CreditGrants []CreditGrant `json:"creditGrants"`
}

type CreditGrant struct {
	ID         string    `json:"id"`
	Currency   string    `json:"currency"`
	AmountYuan string    `json:"amountYuan"`
	CreatedAt  time.Time `json:"createdAt"`
}

type Invoice struct {
	Period string `json:"period"`
	ID     string `json:"id"`
	Amount string `json:"amount"`
	Status string `json:"status"`
}

type Release struct {
	AppID            string `json:"appId"`
	App              string `json:"app"`
	GroupID          string `json:"groupId,omitempty"`
	Brand            string `json:"brand,omitempty"`
	IconRef          string `json:"iconRef,omitempty"`
	PublicIconURL    string `json:"publicIconUrl,omitempty"`
	SortOrder        int    `json:"sortOrder,omitempty"`
	Version          string `json:"version"`
	PublishedVersion string `json:"publishedVersion,omitempty"`
	Channel          string `json:"channel"`
	Visibility       string `json:"visibility,omitempty"`
	Enabled          bool   `json:"enabled"`
	DisabledReason   string `json:"disabledReason,omitempty"`
	Chart            string `json:"chart,omitempty"`
	ChartVersion     string `json:"chartVersion,omitempty"`
	Revision         int    `json:"revision,omitempty"`
	Validation       string `json:"validation"`
	Audience         string `json:"audience"`
	Updated          string `json:"updated"`
	Status           string `json:"status"`
}

type ManagedApp struct {
	App       App          `json:"app"`
	Release   Release      `json:"release"`
	Published *AppVersion  `json:"published,omitempty"`
	Versions  []AppVersion `json:"versions,omitempty"`
}

type AppVersionChart struct {
	OCIRef           string  `json:"ociRef"`
	Name             string  `json:"name"`
	Version          string  `json:"version"`
	AppVersion       *string `json:"appVersion"`
	ManifestDigest   string  `json:"manifestDigest"`
	PackageDigest    string  `json:"packageDigest"`
	PackageSizeBytes int64   `json:"packageSizeBytes"`
}

type AppCompatibility struct {
	KubernetesVersionRange *string  `json:"kubernetesVersionRange"`
	MinimumRuntimeVersion  *string  `json:"minimumRuntimeVersion"`
	Architectures          []string `json:"architectures"`
}

type AppDependencyRef = AppDependency

// AppVersion is an immutable OCI installation input or sealed Center candidate.
// Manifest, chart and image references are content-addressed at creation; publishing
// and Station installation remain separate decisions.
type AppVersion struct {
	AppID           string            `json:"appId"`
	Version         string            `json:"version"`
	Chart           *AppVersionChart  `json:"chart,omitempty"`
	Images          []AppImageRef     `json:"images"`
	Models          []string          `json:"models"`
	Capabilities    []string          `json:"capabilities"`
	Dependencies    []AppDependency   `json:"dependencies"`
	Compatibility   AppCompatibility  `json:"compatibility"`
	ReleaseNotes    string            `json:"releaseNotes"`
	Logo            *LogoRef          `json:"logo"`
	SourceRevision  string            `json:"sourceRevision"`
	VerifiedAt      string            `json:"verifiedAt,omitempty"`
	UpstreamVersion string            `json:"upstreamVersion,omitempty"`
	Publisher       string            `json:"publisher,omitempty"`
	SourceURL       string            `json:"sourceUrl,omitempty"`
	LicenseID       string            `json:"licenseId,omitempty"`
	LicenseURL      string            `json:"licenseUrl,omitempty"`
	ManifestRef     string            `json:"manifestRef,omitempty"`
	ManifestSHA256  string            `json:"manifestSha256,omitempty"`
	Manifest        json.RawMessage   `json:"manifest,omitempty"`
	Artifacts       []AppArtifact     `json:"artifacts,omitempty"`
	Permissions     []AppPermission   `json:"permissions,omitempty"`
	Validation      []ValidationCheck `json:"validation,omitempty"`
	Status          string            `json:"status,omitempty"`
	CreatedAt       time.Time         `json:"createdAt"`
}

// StationTestGrant exposes a Center candidate only to one authenticated
// internal Station. It does not authorize installation or imply validation.
type StationTestGrant struct {
	StationID         string    `json:"stationId"`
	OrganizationID    string    `json:"organizationId"`
	AppID             string    `json:"appId"`
	Version           string    `json:"version"`
	CertificateSHA256 string    `json:"certificateSha256"`
	ExpiresAt         time.Time `json:"expiresAt"`
	Revoked           bool      `json:"revoked"`
	Revision          int       `json:"revision"`
	CreatedAt         time.Time `json:"createdAt"`
}

type AppArtifact struct {
	Component string `json:"component"`
	Ref       string `json:"ref"`
	SHA256    string `json:"sha256"`
}

type AppDependency struct {
	AppID        string `json:"appId,omitempty"`
	VersionRange string `json:"versionRange,omitempty"`
	Kind         string `json:"kind,omitempty"`
	ID           string `json:"id,omitempty"`
	Version      string `json:"version,omitempty"`
	SourceURL    string `json:"sourceUrl,omitempty"`
	LicenseID    string `json:"licenseId,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
}

type AppPermission struct {
	Scope  string `json:"scope"`
	Reason string `json:"reason"`
}

type ValidationCheck struct {
	Code   string `json:"code"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type ManagedOrganization struct {
	Organization Organization `json:"organization"`
	Apps         []App        `json:"apps"`
	AppIDs       []string     `json:"appIds"`
	Members      []Member     `json:"members"`
}

type OperationsOrganization struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Plan     string `json:"plan"`
	Members  int    `json:"members"`
	Apps     int    `json:"apps"`
	APIUsage string `json:"apiUsage"`
	Expires  string `json:"expires"`
	Status   string `json:"status"`
}

type UsageSummary struct {
	BudgetQuota    int            `json:"budgetQuota"`
	UsedQuota      int            `json:"usedQuota"`
	RemainingQuota int            `json:"remainingQuota"`
	Currency       string         `json:"currency"`
	ExchangeRate   string         `json:"exchangeRateCnyPerUsd"`
	BudgetYuan     string         `json:"budgetYuan"`
	UsedYuan       string         `json:"usedYuan"`
	RemainingYuan  string         `json:"remainingYuan"`
	Enabled        bool           `json:"enabled"`
	Percentage     float64        `json:"percentage"`
	ByModel        map[string]int `json:"byModel"`
}

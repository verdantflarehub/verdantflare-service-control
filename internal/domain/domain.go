package domain

import "time"

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

type App struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	Summary       string `json:"summary"`
	Version       string `json:"version"`
	Channel       string `json:"channel"`
	Status        string `json:"status"`
	Tone          string `json:"tone"`
	Icon          string `json:"icon"`
	GPU           string `json:"gpu"`
	Duration      string `json:"duration"`
	Developer     string `json:"developer,omitempty"`
	Description   string `json:"description,omitempty"`
	Memory        string `json:"memory,omitempty"`
	Disk          string `json:"disk,omitempty"`
	CPU           string `json:"cpu,omitempty"`
	PublicIconURL string `json:"publicIconUrl,omitempty"`
	PublicVisible bool   `json:"publicVisible"`
}

// PublicModel contains editorial, public-facing model facts. It is not a
// gateway entitlement, routing, metering or billing record.
type PublicModel struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Provider      string   `json:"provider"`
	Summary       string   `json:"summary"`
	Categories    []string `json:"categories"`
	Context       string   `json:"context,omitempty"`
	MaxInput      string   `json:"maxInput,omitempty"`
	MaxOutput     string   `json:"maxOutput,omitempty"`
	InputPrice    string   `json:"inputPrice,omitempty"`
	OutputPrice   string   `json:"outputPrice,omitempty"`
	CachePrice    string   `json:"cachePrice,omitempty"`
	PriceUnit     string   `json:"priceUnit,omitempty"`
	PublicVisible bool     `json:"publicVisible"`
}

type PublicCatalog struct {
	Models []PublicModel `json:"models"`
	Apps   []PublicApp   `json:"apps"`
}

type PublicApp struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Summary     string `json:"summary"`
	Version     string `json:"version"`
	Developer   string `json:"developer,omitempty"`
	Description string `json:"description,omitempty"`
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

type Model struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	Type        string `json:"type"`
	Context     string `json:"context"`
	Latency     string `json:"latency"`
	Price       string `json:"price"`
	Status      string `json:"status"`
	InputPrice  string `json:"inputPrice,omitempty"`
	OutputPrice string `json:"outputPrice,omitempty"`
	CachePrice  string `json:"cachePrice,omitempty"`
	PriceUnit   string `json:"priceUnit,omitempty"`
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

type Invoice struct {
	Period string `json:"period"`
	ID     string `json:"id"`
	Amount string `json:"amount"`
	Status string `json:"status"`
}

type Release struct {
	AppID      string `json:"appId"`
	App        string `json:"app"`
	Version    string `json:"version"`
	Channel    string `json:"channel"`
	Validation string `json:"validation"`
	Audience   string `json:"audience"`
	Updated    string `json:"updated"`
	Status     string `json:"status"`
}

type ManagedApp struct {
	App     App     `json:"app"`
	Release Release `json:"release"`
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
	Budget     float64        `json:"budget"`
	Used       float64        `json:"used"`
	Remaining  float64        `json:"remaining"`
	Percentage float64        `json:"percentage"`
	ByModel    map[string]int `json:"byModel"`
}

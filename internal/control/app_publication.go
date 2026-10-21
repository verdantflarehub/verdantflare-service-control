package control

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

// PublicationInput is a complete, single-request publication using a
// Chart that has already been released to the controlled OCI registry.
// System-maintained fields (status, revision, channel and publishedVersion)
// are intentionally absent.
type PublicationInput struct {
	AppID            string                   `json:"appId"`
	Name             string                   `json:"name"`
	GroupID          string                   `json:"groupId"`
	Category         string                   `json:"category"`
	Summary          string                   `json:"summary"`
	Description      string                   `json:"description"`
	Developer        string                   `json:"developer"`
	Brand            *string                  `json:"brand,omitempty"`
	IconRef          *string                  `json:"iconRef,omitempty"`
	PublicIconURL    *string                  `json:"publicIconUrl,omitempty"`
	SortOrder        *int                     `json:"sortOrder,omitempty"`
	Showcase         *domain.AppShowcase      `json:"showcase,omitempty"`
	Version          CreateOCIAppVersionInput `json:"version"`
	OrganizationIDs  []string                 `json:"organizationIds"`
	ExpectedRevision int                      `json:"expectedRevision,omitempty"`
}

// selfDeveloper marks apps built and distributed by VerdantFlare itself.
const selfDeveloper = "VerdantFlare"

type PublicationResult struct {
	App             domain.App        `json:"app"`
	Version         domain.AppVersion `json:"version"`
	OrganizationIDs []string          `json:"organizationIds"`
	CatalogETag     string            `json:"catalogETag"`
}

type PublicationList struct {
	Items       []domain.App               `json:"items"`
	CatalogApps []domain.MarketCatalogItem `json:"catalogApps"`
	CatalogETag string                     `json:"catalogETag"`
}

// PublicationDirectory combines the editable Control records with the
// current Station feed so the management page never guesses installability.
func (s *Service) PublicationDirectory(ctx context.Context, subject string) (PublicationList, error) {
	apps, err := s.ListManagedAppsV2(ctx, subject)
	if err != nil {
		return PublicationList{}, err
	}
	catalog, _, etag, err := s.MarketAppsYAML(ctx, "hub-mcp", true)
	if err != nil {
		return PublicationList{}, err
	}
	if apps == nil {
		apps = []domain.App{}
	}
	if catalog.Apps == nil {
		catalog.Apps = []domain.MarketCatalogItem{}
	}
	return PublicationList{Items: apps, CatalogApps: catalog.Apps, CatalogETag: etag}, nil
}

func (s *Service) PublicationOrganizations(ctx context.Context, subject string) ([]domain.OperationsOrganization, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return nil, err
	}
	return s.repository.ListOperationsOrganizations(ctx)
}

// PublishRegisteredOCI refuses incomplete or unverified releases before the
// public pointer is moved. A failed intermediate step leaves the candidate
// recoverable in Control; retrying the same immutable version is idempotent.
func (s *Service) PublishRegisteredOCI(ctx context.Context, subject string, input PublicationInput) (PublicationResult, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return PublicationResult{}, err
	}
	if err := s.preflightPublication(ctx, &input); err != nil {
		return PublicationResult{}, err
	}
	var app domain.App
	managed, err := s.repository.ManagedApp(ctx, input.AppID)
	if err == nil {
		if input.ExpectedRevision < 1 || managed.App.Revision != input.ExpectedRevision {
			return PublicationResult{}, domain.NewError(409, "APP_REVISION_CHANGED", "应用资料已被更新，请刷新后重试")
		}
		if managed.App.GroupID != input.GroupID {
			return PublicationResult{}, domain.NewError(409, "APP_GROUP_IMMUTABLE", "已登记应用的分组不能修改")
		}
		if managed.App.PublishedVersion != "" && managed.App.PublishedVersion != input.Version.Version && strings.TrimSpace(input.Version.ReleaseNotes) == "" {
			return PublicationResult{}, domain.NewError(422, "RELEASE_NOTES_REQUIRED", "替换已发布版本须提供更新说明")
		}
		app = managed.App
	} else {
		var domainError *domain.Error
		if !errors.As(err, &domainError) || domainError.Status != 404 {
			return PublicationResult{}, err
		}
		if input.ExpectedRevision != 0 {
			return PublicationResult{}, domain.NewError(409, "APP_REVISION_CHANGED", "应用不存在，不能按已有修订发布")
		}
		brand, iconRef := "", ""
		if input.Brand != nil {
			brand = *input.Brand
		}
		if input.IconRef != nil {
			iconRef = *input.IconRef
		}
		app, err = s.CreateManagedAppV2(ctx, subject, CreateAppV2Input{AppID: input.AppID, Name: input.Name, GroupID: input.GroupID, Category: input.Category, Summary: input.Summary, Description: input.Description, Brand: brand, IconRef: iconRef, SortOrder: input.SortOrder})
		if err != nil {
			return PublicationResult{}, err
		}
	}
	patch := PatchAppV2Input{Name: &input.Name, Category: &input.Category, Summary: &input.Summary, Description: &input.Description, Developer: &input.Developer, Brand: input.Brand, IconRef: input.IconRef, PublicIconURL: input.PublicIconURL, SortOrder: input.SortOrder, Showcase: input.Showcase}
	app, err = s.PatchManagedAppV2(ctx, subject, input.AppID, fmt.Sprintf("app:%s:%d", app.ID, app.Revision), patch)
	if err != nil {
		return PublicationResult{}, err
	}
	if readiness := publicationReadiness(app); !readiness.Ready {
		return PublicationResult{}, domain.NewError(422, "LISTING_INCOMPLETE", "发布资料不完整："+strings.Join(readiness.MissingFields, ", "))
	}
	version, _, err := s.CreateOCIAppVersion(ctx, subject, input.AppID, input.Version)
	if err != nil {
		return PublicationResult{}, err
	}
	for _, organizationID := range input.OrganizationIDs {
		organization, err := s.repository.ManagedOrganization(ctx, organizationID)
		if err != nil {
			return PublicationResult{}, err
		}
		if _, err = s.repository.GrantAppToOrganization(ctx, organizationID, input.AppID, organization.Organization.EntitlementVersion); err != nil {
			return PublicationResult{}, err
		}
	}
	selected, err := s.selectAppReleaseV2(ctx, subject, input.AppID, fmt.Sprintf("app:%s:%d", app.ID, app.Revision), &version.Version, true)
	if err != nil {
		return PublicationResult{}, err
	}
	catalog, _, etag, err := s.MarketAppsYAML(ctx, "hub-mcp", true)
	if err != nil {
		return PublicationResult{}, err
	}
	for _, item := range catalog.Apps {
		if item.AppID == input.AppID && item.Version == version.Version && item.Chart == version.Chart.OCIRef && item.ChartVersion == version.Chart.Version {
			return PublicationResult{App: selected, Version: version, OrganizationIDs: input.OrganizationIDs, CatalogETag: etag}, nil
		}
	}
	return PublicationResult{}, domain.NewError(502, "CATALOG_READBACK_MISMATCH", "发布指针已更新，但 Station 目录回读不一致；请按应用 ID 与版本检查后重试")
}

// PublicationUpdateInput changes editorial fields only. Omitted fields keep
// their values; versions, enablement and grants have dedicated tools.
type PublicationUpdateInput struct {
	AppID            string              `json:"appId"`
	ExpectedRevision int                 `json:"expectedRevision"`
	Name             *string             `json:"name,omitempty"`
	Category         *string             `json:"category,omitempty"`
	Summary          *string             `json:"summary,omitempty"`
	Description      *string             `json:"description,omitempty"`
	Developer        *string             `json:"developer,omitempty"`
	Brand            *string             `json:"brand,omitempty"`
	IconRef          *string             `json:"iconRef,omitempty"`
	PublicIconURL    *string             `json:"publicIconUrl,omitempty"`
	SortOrder        *int                `json:"sortOrder,omitempty"`
	Showcase         *domain.AppShowcase `json:"showcase,omitempty"`
}

type PublicationStateInput struct {
	AppID            string `json:"appId"`
	ExpectedRevision int    `json:"expectedRevision"`
	Reason           string `json:"reason,omitempty"`
}

type PublicationGrantInput struct {
	AppID           string   `json:"appId"`
	OrganizationIDs []string `json:"organizationIds"`
}

type PublicationStateResult struct {
	App         domain.App `json:"app"`
	CatalogETag string     `json:"catalogETag"`
}

type PublicationGrantResult struct {
	AppID           string   `json:"appId"`
	OrganizationIDs []string `json:"organizationIds"`
}

func (s *Service) UpdatePublishedApp(ctx context.Context, subject string, input PublicationUpdateInput) (PublicationStateResult, error) {
	patch := PatchAppV2Input{Name: input.Name, Category: input.Category, Summary: input.Summary, Description: input.Description, Developer: input.Developer,
		Brand: input.Brand, IconRef: input.IconRef, PublicIconURL: input.PublicIconURL, SortOrder: input.SortOrder, Showcase: input.Showcase}
	app, err := s.PatchManagedAppV2(ctx, subject, input.AppID, fmt.Sprintf("app:%s:%d", input.AppID, input.ExpectedRevision), patch)
	if err != nil {
		return PublicationStateResult{}, err
	}
	_, _, etag, err := s.MarketAppsYAML(ctx, "hub-mcp", true)
	if err != nil {
		return PublicationStateResult{}, err
	}
	return PublicationStateResult{App: app, CatalogETag: etag}, nil
}

// DisablePublishedApp stops distribution without touching installed
// instances; the published version and grants are kept for EnablePublishedApp.
func (s *Service) DisablePublishedApp(ctx context.Context, subject string, input PublicationStateInput) (PublicationStateResult, error) {
	reason := strings.TrimSpace(input.Reason)
	if reason == "" || utf8.RuneCountInString(reason) > 256 {
		return PublicationStateResult{}, domain.NewError(422, "DISABLE_REASON_REQUIRED", "停用须提供 1–256 字的原因")
	}
	return s.setPublishedAppEnabled(ctx, subject, input, false, reason)
}

func (s *Service) EnablePublishedApp(ctx context.Context, subject string, input PublicationStateInput) (PublicationStateResult, error) {
	return s.setPublishedAppEnabled(ctx, subject, input, true, "")
}

func (s *Service) setPublishedAppEnabled(ctx context.Context, subject string, input PublicationStateInput, enabled bool, reason string) (PublicationStateResult, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return PublicationStateResult{}, err
	}
	managed, err := s.repository.ManagedApp(ctx, input.AppID)
	if err != nil {
		return PublicationStateResult{}, err
	}
	app := managed.App
	if app.Enabled != enabled || (!enabled && app.DisabledReason != reason) {
		if enabled {
			if readiness := publicationReadiness(app); !readiness.Ready {
				return PublicationStateResult{}, domain.NewError(422, "LISTING_INCOMPLETE", "发布资料不完整："+strings.Join(readiness.MissingFields, ", "))
			}
		}
		patch := PatchAppV2Input{Enabled: &enabled}
		if !enabled {
			patch.DisabledReason = &reason
		}
		if app, err = s.PatchManagedAppV2(ctx, subject, input.AppID, fmt.Sprintf("app:%s:%d", input.AppID, input.ExpectedRevision), patch); err != nil {
			return PublicationStateResult{}, err
		}
	}
	catalog, _, etag, err := s.MarketAppsYAML(ctx, "hub-mcp", true)
	if err != nil {
		return PublicationStateResult{}, err
	}
	listed := false
	for _, item := range catalog.Apps {
		if item.AppID == app.ID && item.Version == app.PublishedVersion {
			listed = true
		}
	}
	if listed != enabled {
		return PublicationStateResult{}, domain.NewError(502, "CATALOG_READBACK_MISMATCH", "启停状态已写入，但 Station 目录回读不一致；请按应用 ID 检查后重试")
	}
	return PublicationStateResult{App: app, CatalogETag: etag}, nil
}

func (s *Service) GrantPublishedApp(ctx context.Context, subject string, input PublicationGrantInput) (PublicationGrantResult, error) {
	return s.changePublishedAppGrants(ctx, subject, input, s.repository.GrantAppToOrganization)
}

func (s *Service) RevokePublishedApp(ctx context.Context, subject string, input PublicationGrantInput) (PublicationGrantResult, error) {
	return s.changePublishedAppGrants(ctx, subject, input, s.repository.RevokeAppFromOrganization)
}

func (s *Service) changePublishedAppGrants(ctx context.Context, subject string, input PublicationGrantInput, change func(context.Context, string, string, int) (domain.ManagedOrganization, error)) (PublicationGrantResult, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return PublicationGrantResult{}, err
	}
	if _, err := s.repository.ManagedApp(ctx, input.AppID); err != nil {
		return PublicationGrantResult{}, err
	}
	seen := map[string]bool{}
	for _, organizationID := range input.OrganizationIDs {
		if organizationID == "" || seen[organizationID] {
			return PublicationGrantResult{}, domain.NewError(422, "ORGANIZATION_SCOPE_INVALID", "授权组织不能为空或重复")
		}
		seen[organizationID] = true
	}
	if len(seen) == 0 {
		return PublicationGrantResult{}, domain.NewError(422, "ORGANIZATION_SCOPE_INVALID", "至少选择一个组织")
	}
	for _, organizationID := range input.OrganizationIDs {
		organization, err := s.repository.ManagedOrganization(ctx, organizationID)
		if err != nil {
			return PublicationGrantResult{}, domain.NewError(422, "ORGANIZATION_SCOPE_INVALID", "授权组织不存在")
		}
		if _, err := change(ctx, organizationID, input.AppID, organization.Organization.EntitlementVersion); err != nil {
			return PublicationGrantResult{}, err
		}
	}
	return PublicationGrantResult{AppID: input.AppID, OrganizationIDs: input.OrganizationIDs}, nil
}

// preflightPublication rejects deterministic listing, version and scope errors
// before any record is created or any external system is touched.
func (s *Service) preflightPublication(ctx context.Context, input *PublicationInput) error {
	input.AppID = strings.TrimSpace(input.AppID)
	if !appIDPattern.MatchString(input.AppID) || len(input.OrganizationIDs) == 0 {
		return domain.NewError(422, "PUBLICATION_INPUT_INVALID", "应用 ID 或授权组织范围无效")
	}
	if strings.TrimSpace(input.Category) == "" || strings.TrimSpace(input.Developer) == "" || strings.TrimSpace(input.Summary) == "" || strings.TrimSpace(input.Description) == "" {
		return domain.NewError(422, "LISTING_INCOMPLETE", "缺少分类、开发者、简介、介绍、截图或功能亮点")
	}
	// VerdantFlare's own apps need no third-party license evidence; when a
	// license is supplied it must still be complete.
	licenseOptional := strings.TrimSpace(input.Developer) == selfDeveloper && input.Version.LicenseID == "" && input.Version.LicenseURL == ""
	if input.Version.Chart == nil || input.Version.SourceRevision == "" || (input.Version.SourceURL != "" && !validEditorialURL(input.Version.SourceURL)) ||
		(!licenseOptional && (input.Version.LicenseID == "" || !validEditorialURL(input.Version.LicenseURL))) {
		return domain.NewError(422, "VERSION_EVIDENCE_REQUIRED", "固定 OCI 包与源码 revision 为必填；第三方应用还须提供许可证标识与 HTTPS 证据")
	}
	if input.Version.Chart.PackageDigest == "" || input.Version.Chart.ManifestDigest == "" {
		return domain.NewError(422, "CHART_DIGEST_REQUIRED", "须提供已发行 OCI Chart 的双摘要")
	}
	// Reject deterministic listing and artifact input errors before creating a
	// managed app. OCI readback can still fail later; the caller then resumes
	// against the persisted revision instead of assuming another create.
	preview := domain.App{ID: input.AppID, Name: input.Name, Category: input.Category, Summary: input.Summary, Description: input.Description, Developer: input.Developer}
	if input.PublicIconURL != nil {
		preview.PublicIconURL = *input.PublicIconURL
	}
	if input.Showcase != nil {
		preview.Showcase = *input.Showcase
	}
	if err := validateEditorialFields(preview); err != nil {
		return err
	}
	if readiness := publicationReadiness(preview); !readiness.Ready {
		return domain.NewError(422, "LISTING_INCOMPLETE", "发布资料不完整："+strings.Join(readiness.MissingFields, ", "))
	}
	if !chartVersionPattern.MatchString(strings.TrimSpace(input.Version.Version)) || !chartVersionPattern.MatchString(strings.TrimSpace(input.Version.Chart.Version)) ||
		!imageDigestPattern.MatchString(strings.ToLower(strings.TrimSpace(input.Version.Chart.ManifestDigest))) || !imageDigestPattern.MatchString(strings.ToLower(strings.TrimSpace(input.Version.Chart.PackageDigest))) {
		return domain.NewError(422, "CHART_DIGEST_INVALID", "发行版本、Chart 版本或摘要格式无效")
	}
	expectedOCIRef := "oci://" + s.chartRegistryHost + "/charts/" + input.AppID
	if strings.TrimSpace(input.Version.Chart.OCIRef) != expectedOCIRef {
		return domain.NewError(422, "CHART_OCI_REF_INVALID", "Chart OCI 引用必须指向受控 Registry 中同名应用")
	}
	seen := map[string]bool{}
	for _, organizationID := range input.OrganizationIDs {
		if organizationID == "" || seen[organizationID] {
			return domain.NewError(422, "ORGANIZATION_SCOPE_INVALID", "授权组织不能为空或重复")
		}
		seen[organizationID] = true
		organization, err := s.repository.ManagedOrganization(ctx, organizationID)
		if err != nil || organization.Organization.Status != "正常" {
			return domain.NewError(422, "ORGANIZATION_SCOPE_INVALID", "授权组织不存在或不可用")
		}
	}
	return nil
}

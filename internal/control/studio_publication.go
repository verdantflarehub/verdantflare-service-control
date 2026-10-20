package control

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

// StudioPublicationInput is a complete, single-request publication using a
// Chart that has already been released to the controlled OCI registry.
// System-maintained fields (status, revision, channel and publishedVersion)
// are intentionally absent.
type StudioPublicationInput struct {
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

type StudioPublicationResult struct {
	App             domain.App        `json:"app"`
	Version         domain.AppVersion `json:"version"`
	OrganizationIDs []string          `json:"organizationIds"`
	CatalogETag     string            `json:"catalogETag"`
}

func (s *Service) StudioPublicationOrganizations(ctx context.Context, subject string) ([]domain.OperationsOrganization, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return nil, err
	}
	return s.repository.ListOperationsOrganizations(ctx)
}

// PublishRegisteredOCI refuses incomplete or unverified releases before the
// public pointer is moved. A failed intermediate step leaves the candidate
// recoverable in Control; retrying the same immutable version is idempotent.
func (s *Service) PublishRegisteredOCI(ctx context.Context, subject string, input StudioPublicationInput) (StudioPublicationResult, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return StudioPublicationResult{}, err
	}
	input.AppID = strings.TrimSpace(input.AppID)
	if !appIDPattern.MatchString(input.AppID) || len(input.OrganizationIDs) == 0 {
		return StudioPublicationResult{}, domain.NewError(422, "PUBLICATION_INPUT_INVALID", "应用 ID 或授权组织范围无效")
	}
	if strings.TrimSpace(input.Category) == "" || strings.TrimSpace(input.Developer) == "" || strings.TrimSpace(input.Summary) == "" || strings.TrimSpace(input.Description) == "" {
		return StudioPublicationResult{}, domain.NewError(422, "LISTING_INCOMPLETE", "缺少分类、开发者、简介、介绍、截图或功能亮点")
	}
	if input.Version.Chart == nil || input.Version.SourceRevision == "" || input.Version.LicenseID == "" || input.Version.LicenseURL == "" || !validEditorialURL(input.Version.LicenseURL) || (input.Version.SourceURL != "" && !validEditorialURL(input.Version.SourceURL)) {
		return StudioPublicationResult{}, domain.NewError(422, "VERSION_EVIDENCE_REQUIRED", "固定 OCI 包、源码 revision、许可证标识与 HTTPS 证据均为必填")
	}
	if input.Version.Chart.PackageDigest == "" || input.Version.Chart.ManifestDigest == "" {
		return StudioPublicationResult{}, domain.NewError(422, "CHART_DIGEST_REQUIRED", "须提供已发行 OCI Chart 的双摘要")
	}
	seen := map[string]bool{}
	for _, organizationID := range input.OrganizationIDs {
		if organizationID == "" || seen[organizationID] {
			return StudioPublicationResult{}, domain.NewError(422, "ORGANIZATION_SCOPE_INVALID", "授权组织不能为空或重复")
		}
		seen[organizationID] = true
		organization, err := s.repository.ManagedOrganization(ctx, organizationID)
		if err != nil || organization.Organization.Status != "正常" {
			return StudioPublicationResult{}, domain.NewError(422, "ORGANIZATION_SCOPE_INVALID", "授权组织不存在或不可用")
		}
	}
	var app domain.App
	managed, err := s.repository.ManagedApp(ctx, input.AppID)
	if err == nil {
		if input.ExpectedRevision < 1 || managed.App.Revision != input.ExpectedRevision {
			return StudioPublicationResult{}, domain.NewError(409, "APP_REVISION_CHANGED", "应用资料已被更新，请刷新后重试")
		}
		if managed.App.GroupID != input.GroupID {
			return StudioPublicationResult{}, domain.NewError(409, "APP_GROUP_IMMUTABLE", "已登记应用的分组不能修改")
		}
		app = managed.App
	} else {
		var domainError *domain.Error
		if !errors.As(err, &domainError) || domainError.Status != 404 {
			return StudioPublicationResult{}, err
		}
		if input.ExpectedRevision != 0 {
			return StudioPublicationResult{}, domain.NewError(409, "APP_REVISION_CHANGED", "应用不存在，不能按已有修订发布")
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
			return StudioPublicationResult{}, err
		}
	}
	patch := PatchAppV2Input{Name: &input.Name, Category: &input.Category, Summary: &input.Summary, Description: &input.Description, Developer: &input.Developer, Brand: input.Brand, IconRef: input.IconRef, PublicIconURL: input.PublicIconURL, SortOrder: input.SortOrder, Showcase: input.Showcase}
	app, err = s.PatchManagedAppV2(ctx, subject, input.AppID, fmt.Sprintf("app:%s:%d", app.ID, app.Revision), patch)
	if err != nil {
		return StudioPublicationResult{}, err
	}
	if readiness := publicationReadiness(app); !readiness.Ready {
		return StudioPublicationResult{}, domain.NewError(422, "LISTING_INCOMPLETE", "发布资料不完整："+strings.Join(readiness.MissingFields, ", "))
	}
	if app.PublishedVersion != "" && app.PublishedVersion != input.Version.Version && strings.TrimSpace(input.Version.ReleaseNotes) == "" {
		return StudioPublicationResult{}, domain.NewError(422, "RELEASE_NOTES_REQUIRED", "替换已发布版本须提供更新说明")
	}
	version, _, err := s.CreateOCIAppVersion(ctx, subject, input.AppID, input.Version)
	if err != nil {
		return StudioPublicationResult{}, err
	}
	for _, organizationID := range input.OrganizationIDs {
		organization, err := s.repository.ManagedOrganization(ctx, organizationID)
		if err != nil {
			return StudioPublicationResult{}, err
		}
		if _, err = s.repository.GrantAppToOrganization(ctx, organizationID, input.AppID, organization.Organization.EntitlementVersion); err != nil {
			return StudioPublicationResult{}, err
		}
	}
	selected, err := s.selectAppReleaseV2(ctx, subject, input.AppID, fmt.Sprintf("app:%s:%d", app.ID, app.Revision), &version.Version, true)
	if err != nil {
		return StudioPublicationResult{}, err
	}
	catalog, _, etag, err := s.MarketAppsYAML(ctx, "studio-publish", true)
	if err != nil {
		return StudioPublicationResult{}, err
	}
	for _, item := range catalog.Apps {
		if item.AppID == input.AppID && item.Version == version.Version && item.Chart == version.Chart.OCIRef && item.ChartVersion == version.Chart.Version {
			return StudioPublicationResult{App: selected, Version: version, OrganizationIDs: input.OrganizationIDs, CatalogETag: etag}, nil
		}
	}
	return StudioPublicationResult{}, domain.NewError(502, "CATALOG_READBACK_MISMATCH", "发布指针已更新，但 Station 目录回读不一致；请按应用 ID 与版本检查后重试")
}

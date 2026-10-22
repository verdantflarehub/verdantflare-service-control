package store

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func appStatus(channel string) string {
	switch channel {
	case "Preview":
		return "预览发布"
	case "Listed":
		return "目录已上架·待交付"
	case "Stable":
		return "可用"
	case "Paused":
		return "已暂停"
	default:
		return "候选"
	}
}

func published(app domain.App) bool {
	return app.Channel == "Preview" || app.Channel == "Stable"
}

// Paused apps retain historical grants, but are not new grant options.
func entitlementSelectable(app domain.App) bool {
	return published(app) && app.Enabled && app.DisabledReason == ""
}

func normalizeApp(app domain.App) domain.App {
	if app.ID == "" {
		app.ID = app.AppID
	}
	app.AppID = app.ID
	if app.Revision < 1 {
		app.Revision = 1
	}
	if app.Visibility == "" {
		if app.Channel == "Listed" || app.Channel == "Preview" || app.Channel == "Stable" {
			app.Visibility = "listed"
		} else {
			app.Visibility = "hidden"
		}
	}
	if app.CreatedAt == "" {
		app.CreatedAt = "2026-07-01T00:00:00Z"
	}
	if app.UpdatedAt == "" {
		app.UpdatedAt = app.CreatedAt
	}
	if app.Models != nil {
		app.Models = append([]string(nil), app.Models...)
	}
	if app.Capabilities != nil {
		app.Capabilities = append([]string(nil), app.Capabilities...)
	}
	if app.Images != nil {
		app.Images = append([]domain.AppImageRef(nil), app.Images...)
	}
	if app.Logo != nil {
		logoCopy := *app.Logo
		app.Logo = &logoCopy
	}
	return app
}

func (m *Memory) hasVerifiedChartVersionLocked(appID, version string) bool {
	if version == "" || m.appVersions == nil || m.appVersions[appID] == nil {
		return false
	}
	v, ok := m.appVersions[appID][version]
	return ok && v.Chart != nil && v.Chart.ManifestDigest != "" && v.Chart.PackageDigest != ""
}

func (m *Memory) releaseForLocked(app domain.App) domain.Release {
	app = normalizeApp(app)
	release := domain.Release{AppID: app.ID, App: app.Name, Version: app.Version, Channel: app.Channel, Validation: "0 / 6"}
	for _, candidate := range m.releases {
		if candidate.AppID == app.ID || (candidate.AppID == "" && candidate.App == app.Name) {
			release = candidate
			break
		}
	}
	release.AppID, release.App, release.GroupID, release.Version, release.Channel = app.ID, app.Name, app.GroupID, app.Version, app.Channel
	release.Brand = app.Brand
	release.IconRef = app.IconRef
	release.PublicIconURL = app.PublicIconURL
	release.SortOrder = app.SortOrder
	release.PublishedVersion = app.PublishedVersion
	release.Visibility = app.Visibility
	release.Enabled = app.Enabled
	release.DisabledReason = app.DisabledReason
	release.Revision = app.Revision
	targetVer := app.PublishedVersion
	if targetVer == "" {
		targetVer = app.Version
	}
	if targetVer != "" && m.appVersions != nil && m.appVersions[app.ID] != nil {
		if ver, ok := m.appVersions[app.ID][targetVer]; ok && ver.Chart != nil {
			release.Chart = ver.Chart.OCIRef
			release.ChartVersion = ver.Chart.Version
			release.Validation = "OCI Verified"
		}
	}
	release.Status = map[string]string{"Candidate": "候选", "Listed": "目录已上架", "Preview": "灰度中", "Stable": "已发布", "Paused": "已暂停"}[app.Channel]
	audience := 0
	for _, entitlements := range m.entitlements {
		if entitlements[app.ID] {
			audience++
		}
	}
	release.Audience = fmt.Sprintf("%d 个组织", audience)
	return release
}

func (m *Memory) managedAppLocked(id string) (domain.ManagedApp, error) {
	app, exists := m.apps[id]
	if !exists {
		return domain.ManagedApp{}, domain.NewError(404, "app_not_found", "应用不存在")
	}
	app = normalizeApp(app)
	var publishedVer *domain.AppVersion
	if app.PublishedVersion != "" && m.appVersions != nil && m.appVersions[id] != nil {
		if v, ok := m.appVersions[id][app.PublishedVersion]; ok {
			cloned := cloneAppVersion(v)
			publishedVer = &cloned
		}
	}
	return domain.ManagedApp{
		App:       app,
		Release:   m.releaseForLocked(app),
		Published: publishedVer,
		Versions:  m.listAppVersionsLocked(id),
	}, nil
}

func (m *Memory) ListManagedApps(_ context.Context) ([]domain.App, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]domain.App, 0, len(m.apps))
	for _, app := range m.apps {
		result = append(result, normalizeApp(app))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SortOrder != result[j].SortOrder {
			return result[i].SortOrder < result[j].SortOrder
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (m *Memory) CreateManagedApp(_ context.Context, app domain.App) (domain.ManagedApp, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if app.ID == "" {
		app.ID = app.AppID
	}
	if _, exists := m.apps[app.ID]; exists {
		return domain.ManagedApp{}, domain.NewError(409, "app_exists", "应用 ID 已存在")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if app.Channel == "" {
		app.Channel = "Candidate"
	}
	app.Status = appStatus(app.Channel)
	if app.Visibility == "" {
		app.Visibility = "hidden"
	}
	if !app.Enabled && app.DisabledReason == "" {
		app.DisabledReason = "资料尚未上架"
	}
	app.Revision = 1
	app.CreatedAt = now
	app.UpdatedAt = now
	app = normalizeApp(app)
	m.apps[app.ID] = app
	m.releases = append(m.releases, domain.Release{AppID: app.ID, App: app.Name, Version: app.Version, Channel: app.Channel, Validation: "0 / 6", Updated: now, Status: "候选"})
	return m.managedAppLocked(app.ID)
}

func (m *Memory) ManagedApp(_ context.Context, id string) (domain.ManagedApp, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.managedAppLocked(id)
}

func (m *Memory) UpdateManagedApp(_ context.Context, id string, update domain.App) (domain.ManagedApp, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateManagedAppLocked(id, update, 0)
}

func (m *Memory) UpdateManagedAppIfRevision(_ context.Context, id string, update domain.App, expectedRevision int) (domain.ManagedApp, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateManagedAppLocked(id, update, expectedRevision)
}

func (m *Memory) updateManagedAppLocked(id string, update domain.App, expectedRevision int) (domain.ManagedApp, error) {
	current, exists := m.apps[id]
	if !exists {
		return domain.ManagedApp{}, domain.NewError(404, "app_not_found", "应用不存在")
	}
	current = normalizeApp(current)
	if expectedRevision > 0 && current.Revision != expectedRevision {
		return domain.ManagedApp{}, domain.NewError(409, "APP_REVISION_CHANGED", "应用资料或发布版本已被更新，请刷新后重试")
	}
	release := m.releaseForLocked(current)
	hasVerified := update.PublishedVersion != "" || m.hasVerifiedChartVersionLocked(id, update.Version)
	if update.Channel == "Stable" && release.Validation != "6 / 6" && !hasVerified {
		return domain.ManagedApp{}, domain.NewError(409, "app_validation_incomplete", "六项标准验证未全部通过，不能发布 Stable")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	update.ID = id
	update.AppID = id
	update.Status = appStatus(update.Channel)
	update.CreatedAt = current.CreatedAt
	update.UpdatedAt = now
	update.Revision = current.Revision + 1
	update = normalizeApp(update)
	m.apps[id] = update
	release.App, release.Version, release.Channel = update.Name, update.Version, update.Channel
	release.Brand = update.Brand
	release.IconRef = update.IconRef
	release.PublicIconURL = update.PublicIconURL
	release.SortOrder = update.SortOrder
	release.PublishedVersion = update.PublishedVersion
	release.Visibility = update.Visibility
	release.Enabled = update.Enabled
	release.DisabledReason = update.DisabledReason
	release.Revision = update.Revision
	if hasVerified {
		release.Validation = "OCI Verified"
	}
	release.Updated = now
	found := false
	for index, candidate := range m.releases {
		if candidate.AppID == id || (candidate.AppID == "" && candidate.App == current.Name) {
			m.releases[index] = release
			found = true
			break
		}
	}
	if !found {
		m.releases = append(m.releases, release)
	}
	return m.managedAppLocked(id)
}

func (m *Memory) managedOrganizationLocked(id string) (domain.ManagedOrganization, error) {
	organization, exists := m.organizations[id]
	if !exists {
		return domain.ManagedOrganization{}, domain.NewError(404, "organization_not_found", "组织不存在")
	}
	result := domain.ManagedOrganization{Organization: organization, Apps: []domain.App{}, AppIDs: []string{}, Members: m.membersForLocked(id)}
	for _, app := range m.apps {
		if !entitlementSelectable(app) {
			continue
		}
		result.Apps = append(result.Apps, app)
		if m.entitlements[id][app.ID] {
			result.AppIDs = append(result.AppIDs, app.ID)
		}
	}
	sort.Slice(result.Apps, func(i, j int) bool { return result.Apps[i].Name < result.Apps[j].Name })
	sort.Strings(result.AppIDs)
	return result, nil
}

func (m *Memory) CreateManagedOrganization(_ context.Context, organization domain.Organization) (domain.ManagedOrganization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.organizations[organization.OrganizationID]; exists {
		return domain.ManagedOrganization{}, domain.NewError(409, "organization_exists", "组织 ID 已存在")
	}
	organization.EntitlementVersion = 1
	organization.Status = "正常"
	m.organizations[organization.OrganizationID] = organization
	m.entitlements[organization.OrganizationID] = map[string]bool{}
	m.members[organization.OrganizationID] = []domain.Member{}
	return m.managedOrganizationLocked(organization.OrganizationID)
}

func (m *Memory) ManagedOrganization(_ context.Context, id string) (domain.ManagedOrganization, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.managedOrganizationLocked(id)
}

func (m *Memory) UpdateManagedOrganization(_ context.Context, id string, update domain.Organization, appIDs []string, expectedVersion int) (domain.ManagedOrganization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	organization, exists := m.organizations[id]
	if !exists {
		return domain.ManagedOrganization{}, domain.NewError(404, "organization_not_found", "组织不存在")
	}
	if organization.EntitlementVersion != expectedVersion {
		return domain.ManagedOrganization{}, domain.NewError(409, "entitlement_version_conflict", "权益已被其他管理员修改，请刷新后重试")
	}
	entitlements := make(map[string]bool, len(appIDs))
	// The editor only lists distributable apps. Keep grants for paused
	// apps so changing the plan does not silently revoke them.
	for appID, enabled := range m.entitlements[id] {
		if app, exists := m.apps[appID]; exists && enabled && !entitlementSelectable(app) {
			entitlements[appID] = true
		}
	}
	for _, appID := range appIDs {
		app, ok := m.apps[appID]
		if !ok || !entitlementSelectable(app) {
			return domain.ManagedOrganization{}, domain.NewError(400, "app_unpublished", "只能授权正在分发的应用")
		}
		entitlements[appID] = true
	}
	organization.Plan, organization.Status = update.Plan, update.Status
	if update.Name != "" {
		organization.Name = update.Name
	}
	if update.ShortName != "" {
		organization.ShortName = update.ShortName
	}
	if update.DefaultRegion != "" {
		organization.DefaultRegion = update.DefaultRegion
	}
	if update.Industry != "" {
		organization.Industry = update.Industry
	}
	if update.BillingEmail != "" {
		organization.BillingEmail = update.BillingEmail
	}
	organization.EntitlementVersion++
	m.organizations[id] = organization
	m.entitlements[id] = entitlements
	if billing, ok := m.billing[id]; ok {
		billing.Plan = update.Plan
		m.billing[id] = billing
	}
	return m.managedOrganizationLocked(id)
}

// GrantAppToOrganization prepares a grant before the public release pointer is
// moved. It is used only after the OCI version has been verified by Control.
func (m *Memory) GrantAppToOrganization(_ context.Context, organizationID, appID string, expectedVersion int) (domain.ManagedOrganization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	org, ok := m.organizations[organizationID]
	if !ok || org.Status != "正常" {
		return domain.ManagedOrganization{}, domain.NewError(422, "organization_unavailable", "目标组织不存在或已冻结")
	}
	if _, ok := m.apps[appID]; !ok {
		return domain.ManagedOrganization{}, domain.NewError(404, "app_not_found", "应用不存在")
	}
	if org.EntitlementVersion != expectedVersion {
		return domain.ManagedOrganization{}, domain.NewError(409, "entitlement_version_conflict", "组织权益已改变，请刷新后重试")
	}
	if m.entitlements[organizationID] == nil {
		m.entitlements[organizationID] = map[string]bool{}
	}
	if !m.entitlements[organizationID][appID] {
		m.entitlements[organizationID][appID] = true
		org.EntitlementVersion++
		m.organizations[organizationID] = org
	}
	return m.managedOrganizationLocked(organizationID)
}

func (m *Memory) RevokeAppFromOrganization(_ context.Context, organizationID, appID string, expectedVersion int) (domain.ManagedOrganization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	org, ok := m.organizations[organizationID]
	if !ok {
		return domain.ManagedOrganization{}, domain.NewError(404, "organization_not_found", "组织不存在")
	}
	if org.EntitlementVersion != expectedVersion {
		return domain.ManagedOrganization{}, domain.NewError(409, "entitlement_version_conflict", "组织权益已改变，请刷新后重试")
	}
	if m.entitlements[organizationID][appID] {
		delete(m.entitlements[organizationID], appID)
		org.EntitlementVersion++
		m.organizations[organizationID] = org
	}
	return m.managedOrganizationLocked(organizationID)
}

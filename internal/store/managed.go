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
		return "可体验"
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

func (m *Memory) releaseForLocked(app domain.App) domain.Release {
	release := domain.Release{AppID: app.ID, App: app.Name, Version: app.Version, Channel: app.Channel, Validation: "0 / 6"}
	for _, candidate := range m.releases {
		if candidate.AppID == app.ID || (candidate.AppID == "" && candidate.App == app.Name) {
			release = candidate
			break
		}
	}
	release.AppID, release.App, release.Version, release.Channel = app.ID, app.Name, app.Version, app.Channel
	release.Status = map[string]string{"Candidate": "候选", "Preview": "灰度中", "Stable": "已发布", "Paused": "已暂停"}[app.Channel]
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
	return domain.ManagedApp{App: app, Release: m.releaseForLocked(app)}, nil
}

func (m *Memory) CreateManagedApp(_ context.Context, app domain.App) (domain.ManagedApp, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.apps[app.ID]; exists {
		return domain.ManagedApp{}, domain.NewError(409, "app_exists", "应用 ID 已存在")
	}
	app.Channel, app.Status = "Candidate", appStatus("Candidate")
	m.apps[app.ID] = app
	m.releases = append(m.releases, domain.Release{AppID: app.ID, App: app.Name, Version: app.Version, Channel: app.Channel, Validation: "0 / 6", Updated: time.Now().UTC().Format(time.RFC3339), Status: "候选"})
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
	current, exists := m.apps[id]
	if !exists {
		return domain.ManagedApp{}, domain.NewError(404, "app_not_found", "应用不存在")
	}
	release := m.releaseForLocked(current)
	if update.Channel == "Stable" && release.Validation != "6 / 6" {
		return domain.ManagedApp{}, domain.NewError(409, "app_validation_incomplete", "六项标准验证未全部通过，不能发布 Stable")
	}
	update.ID, update.Status = id, appStatus(update.Channel)
	m.apps[id] = update
	release.App, release.Version, release.Channel = update.Name, update.Version, update.Channel
	release.Updated = time.Now().UTC().Format(time.RFC3339)
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
		if !published(app) {
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
	// The editor only lists published apps. Keep grants for temporarily paused
	// apps so changing the plan does not silently revoke them.
	for appID, enabled := range m.entitlements[id] {
		if app, exists := m.apps[appID]; exists && enabled && !published(app) {
			entitlements[appID] = true
		}
	}
	for _, appID := range appIDs {
		app, ok := m.apps[appID]
		if !ok || !published(app) {
			return domain.ManagedOrganization{}, domain.NewError(400, "app_unpublished", "只能授权已发布的应用")
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

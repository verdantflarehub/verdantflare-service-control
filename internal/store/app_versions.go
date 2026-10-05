package store

import (
	"context"
	"slices"
	"sort"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func cloneAppVersion(version domain.AppVersion) domain.AppVersion {
	version.Manifest = slices.Clone(version.Manifest)
	version.Artifacts = slices.Clone(version.Artifacts)
	version.Dependencies = slices.Clone(version.Dependencies)
	version.Permissions = slices.Clone(version.Permissions)
	version.Validation = slices.Clone(version.Validation)
	return version
}

func (m *Memory) CreateAppVersion(_ context.Context, version domain.AppVersion) (domain.AppVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.apps[version.AppID]; !exists {
		return domain.AppVersion{}, domain.NewError(404, "app_not_found", "应用不存在")
	}
	if m.appVersions == nil {
		m.appVersions = make(map[string]map[string]domain.AppVersion)
	}
	if m.appVersions[version.AppID] == nil {
		m.appVersions[version.AppID] = make(map[string]domain.AppVersion)
	}
	if _, exists := m.appVersions[version.AppID][version.Version]; exists {
		return domain.AppVersion{}, domain.NewError(409, "app_version_exists", "该应用版本已冻结，不能覆盖")
	}
	stored := cloneAppVersion(version)
	m.appVersions[version.AppID][version.Version] = stored
	return cloneAppVersion(stored), nil
}

func (m *Memory) ListAppVersions(_ context.Context, appID string) ([]domain.AppVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, exists := m.apps[appID]; !exists {
		return nil, domain.NewError(404, "app_not_found", "应用不存在")
	}
	versions := make([]domain.AppVersion, 0, len(m.appVersions[appID]))
	for _, version := range m.appVersions[appID] {
		versions = append(versions, cloneAppVersion(version))
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].CreatedAt.After(versions[j].CreatedAt) })
	return versions, nil
}

func (m *Memory) AppVersion(_ context.Context, appID, version string) (domain.AppVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	item, exists := m.appVersions[appID][version]
	if !exists {
		return domain.AppVersion{}, domain.NewError(404, "app_version_not_found", "应用版本不存在")
	}
	return cloneAppVersion(item), nil
}

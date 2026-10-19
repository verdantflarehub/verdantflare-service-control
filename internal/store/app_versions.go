package store

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func cloneAppVersion(version domain.AppVersion) domain.AppVersion {
	if version.Chart != nil {
		chartCopy := *version.Chart
		if version.Chart.AppVersion != nil {
			appVer := *version.Chart.AppVersion
			chartCopy.AppVersion = &appVer
		}
		version.Chart = &chartCopy
	}
	if version.Logo != nil {
		logoCopy := *version.Logo
		version.Logo = &logoCopy
	}
	if version.Images != nil {
		version.Images = slices.Clone(version.Images)
	} else {
		version.Images = []domain.AppImageRef{}
	}
	if version.Models != nil {
		version.Models = slices.Clone(version.Models)
	} else {
		version.Models = []string{}
	}
	if version.Capabilities != nil {
		version.Capabilities = slices.Clone(version.Capabilities)
	} else {
		version.Capabilities = []string{}
	}
	if version.Dependencies != nil {
		version.Dependencies = slices.Clone(version.Dependencies)
	} else {
		version.Dependencies = []domain.AppDependency{}
	}
	if version.Compatibility.Architectures != nil {
		version.Compatibility.Architectures = slices.Clone(version.Compatibility.Architectures)
	} else {
		version.Compatibility.Architectures = []string{"amd64"}
	}
	if version.Compatibility.KubernetesVersionRange != nil {
		s := *version.Compatibility.KubernetesVersionRange
		version.Compatibility.KubernetesVersionRange = &s
	}
	if version.Compatibility.MinimumRuntimeVersion != nil {
		s := *version.Compatibility.MinimumRuntimeVersion
		version.Compatibility.MinimumRuntimeVersion = &s
	}
	version.Manifest = slices.Clone(version.Manifest)
	version.Artifacts = slices.Clone(version.Artifacts)
	version.Permissions = slices.Clone(version.Permissions)
	version.Validation = slices.Clone(version.Validation)
	return version
}

func compareSemVer(a, b string) int {
	cleanA := strings.SplitN(strings.SplitN(strings.TrimPrefix(a, "v"), "+", 2)[0], "-", 2)[0]
	cleanB := strings.SplitN(strings.SplitN(strings.TrimPrefix(b, "v"), "+", 2)[0], "-", 2)[0]
	partsA := strings.Split(cleanA, ".")
	partsB := strings.Split(cleanB, ".")
	for i := 0; i < 3; i++ {
		var numA, numB int
		if i < len(partsA) {
			numA, _ = strconv.Atoi(partsA[i])
		}
		if i < len(partsB) {
			numB, _ = strconv.Atoi(partsB[i])
		}
		if numA != numB {
			if numA > numB {
				return 1
			}
			return -1
		}
	}
	return 0
}

func (m *Memory) listAppVersionsLocked(appID string) []domain.AppVersion {
	versions := make([]domain.AppVersion, 0, len(m.appVersions[appID]))
	for _, version := range m.appVersions[appID] {
		versions = append(versions, cloneAppVersion(version))
	}
	sort.Slice(versions, func(i, j int) bool {
		if cmp := compareSemVer(versions[i].Version, versions[j].Version); cmp != 0 {
			return cmp > 0
		}
		return versions[i].CreatedAt.After(versions[j].CreatedAt)
	})
	return versions
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
	return m.listAppVersionsLocked(appID), nil
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

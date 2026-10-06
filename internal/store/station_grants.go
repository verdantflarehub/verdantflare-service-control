package store

import (
	"context"
	"sort"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func stationGrantKey(grant domain.StationTestGrant) string {
	return grant.StationID + "/" + grant.AppID + "/" + grant.Version + "/" + grant.CertificateSHA256 + "/" + grant.CreatedAt.UTC().Format(time.RFC3339Nano)
}

func (m *Memory) CreateStationTestGrant(_ context.Context, grant domain.StationTestGrant) (domain.StationTestGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[grant.AppID]; !ok {
		return domain.StationTestGrant{}, domain.NewError(404, "app_not_found", "应用不存在")
	}
	if _, ok := m.appVersions[grant.AppID][grant.Version]; !ok {
		return domain.StationTestGrant{}, domain.NewError(404, "app_version_not_found", "应用版本不存在")
	}
	if m.stationGrants == nil {
		m.stationGrants = make(map[string]domain.StationTestGrant)
	}
	maxRevision := 0
	for _, existing := range m.stationGrants {
		if existing.StationID == grant.StationID && existing.AppID == grant.AppID && existing.Version == grant.Version {
			if existing.Revision > maxRevision {
				maxRevision = existing.Revision
			}
			if !existing.Revoked && time.Now().UTC().Before(existing.ExpiresAt) {
				return domain.StationTestGrant{}, domain.NewError(409, "station_grant_active", "该 Station 已有有效的同版本授权；请先撤销")
			}
		}
	}
	if _, ok := m.stationGrants[stationGrantKey(grant)]; ok {
		return domain.StationTestGrant{}, domain.NewError(409, "station_grant_exists", "该设备授权记录已存在")
	}
	grant.Revision = maxRevision + 1
	m.stationGrants[stationGrantKey(grant)] = grant
	return grant, nil
}

func (m *Memory) ListStationTestGrants(_ context.Context, appID string) ([]domain.StationTestGrant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.apps[appID]; !ok {
		return nil, domain.NewError(404, "app_not_found", "应用不存在")
	}
	result := make([]domain.StationTestGrant, 0)
	for _, grant := range m.stationGrants {
		if grant.AppID == appID {
			result = append(result, grant)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

func (m *Memory) StationTestGrantsForStation(_ context.Context, stationID string) ([]domain.StationTestGrant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]domain.StationTestGrant, 0)
	for _, grant := range m.stationGrants {
		if grant.StationID == stationID {
			result = append(result, grant)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].AppID == result[j].AppID {
			return result[i].Version < result[j].Version
		}
		return result[i].AppID < result[j].AppID
	})
	return result, nil
}

func (m *Memory) RevokeStationTestGrant(_ context.Context, appID, version, stationID string) (domain.StationTestGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, grant := range m.stationGrants {
		if grant.AppID == appID && grant.Version == version && grant.StationID == stationID && !grant.Revoked && time.Now().UTC().Before(grant.ExpiresAt) {
			grant.Revoked = true
			grant.Revision++
			m.stationGrants[key] = grant
			return grant, nil
		}
	}
	return domain.StationTestGrant{}, domain.NewError(404, "station_grant_not_found", "有效授权不存在")
}

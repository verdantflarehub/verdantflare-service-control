package store

import (
	"context"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func (p *Postgres) CreateManagedApp(ctx context.Context, app domain.App) (result domain.ManagedApp, err error) {
	err = p.mutate(ctx, func(m *Memory) error { var e error; result, e = m.CreateManagedApp(ctx, app); return e })
	return
}

func (p *Postgres) ManagedApp(ctx context.Context, id string) (domain.ManagedApp, error) {
	m, err := p.read(ctx)
	if err != nil {
		return domain.ManagedApp{}, err
	}
	return m.ManagedApp(ctx, id)
}

func (p *Postgres) UpdateManagedApp(ctx context.Context, id string, app domain.App) (result domain.ManagedApp, err error) {
	err = p.mutate(ctx, func(m *Memory) error { var e error; result, e = m.UpdateManagedApp(ctx, id, app); return e })
	return
}

func (p *Postgres) CreateAppVersion(ctx context.Context, version domain.AppVersion) (result domain.AppVersion, err error) {
	err = p.mutate(ctx, func(m *Memory) error { var e error; result, e = m.CreateAppVersion(ctx, version); return e })
	return
}

func (p *Postgres) ListAppVersions(ctx context.Context, appID string) ([]domain.AppVersion, error) {
	m, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	return m.ListAppVersions(ctx, appID)
}

func (p *Postgres) AppVersion(ctx context.Context, appID, version string) (domain.AppVersion, error) {
	m, err := p.read(ctx)
	if err != nil {
		return domain.AppVersion{}, err
	}
	return m.AppVersion(ctx, appID, version)
}

func (p *Postgres) CreateStationTestGrant(ctx context.Context, grant domain.StationTestGrant) (result domain.StationTestGrant, err error) {
	err = p.mutate(ctx, func(m *Memory) error { var e error; result, e = m.CreateStationTestGrant(ctx, grant); return e })
	return
}

func (p *Postgres) ListStationTestGrants(ctx context.Context, appID string) ([]domain.StationTestGrant, error) {
	m, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	return m.ListStationTestGrants(ctx, appID)
}

func (p *Postgres) StationTestGrantsForStation(ctx context.Context, stationID string) ([]domain.StationTestGrant, error) {
	m, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	return m.StationTestGrantsForStation(ctx, stationID)
}

func (p *Postgres) RevokeStationTestGrant(ctx context.Context, appID, version, stationID string) (result domain.StationTestGrant, err error) {
	err = p.mutate(ctx, func(m *Memory) error {
		var e error
		result, e = m.RevokeStationTestGrant(ctx, appID, version, stationID)
		return e
	})
	return
}

func (p *Postgres) CreateManagedOrganization(ctx context.Context, organization domain.Organization) (result domain.ManagedOrganization, err error) {
	err = p.mutate(ctx, func(m *Memory) error {
		var e error
		result, e = m.CreateManagedOrganization(ctx, organization)
		return e
	})
	return
}

func (p *Postgres) ManagedOrganization(ctx context.Context, id string) (domain.ManagedOrganization, error) {
	m, err := p.read(ctx)
	if err != nil {
		return domain.ManagedOrganization{}, err
	}
	return m.ManagedOrganization(ctx, id)
}

func (p *Postgres) UpdateManagedOrganization(ctx context.Context, id string, organization domain.Organization, appIDs []string, expectedVersion int) (result domain.ManagedOrganization, err error) {
	err = p.mutate(ctx, func(m *Memory) error {
		var e error
		result, e = m.UpdateManagedOrganization(ctx, id, organization, appIDs, expectedVersion)
		return e
	})
	return
}

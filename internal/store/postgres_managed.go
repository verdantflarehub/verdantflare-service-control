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

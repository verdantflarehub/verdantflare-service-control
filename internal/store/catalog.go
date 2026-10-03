package store

import (
	"context"
	"sort"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func (m *Memory) PublicCatalog(_ context.Context) (domain.PublicCatalog, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := domain.PublicCatalog{Models: []domain.PublicModel{}, Apps: []domain.PublicApp{}}
	for _, model := range m.publicModels {
		if model.PublicVisible {
			result.Models = append(result.Models, model)
		}
	}
	for _, app := range m.apps {
		if app.PublicVisible && published(app) {
			result.Apps = append(result.Apps, domain.PublicApp{ID: app.ID, Name: app.Name, Category: app.Category, Summary: app.Summary, Version: app.Version, Developer: app.Developer, Description: app.Description, Memory: app.Memory, Disk: app.Disk, CPU: app.CPU, GPU: app.GPU, IconURL: app.PublicIconURL})
		}
	}
	sort.Slice(result.Models, func(i, j int) bool { return result.Models[i].ID < result.Models[j].ID })
	sort.Slice(result.Apps, func(i, j int) bool { return result.Apps[i].ID < result.Apps[j].ID })
	return result, nil
}

func (m *Memory) ListManagedModels(_ context.Context) ([]domain.PublicModel, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]domain.PublicModel, 0, len(m.publicModels))
	for _, model := range m.publicModels {
		result = append(result, model)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (m *Memory) CreateManagedModel(_ context.Context, model domain.PublicModel) (domain.PublicModel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.publicModels == nil {
		m.publicModels = map[string]domain.PublicModel{}
	}
	if _, exists := m.publicModels[model.ID]; exists {
		return domain.PublicModel{}, domain.NewError(409, "model_exists", "模型 ID 已存在")
	}
	model.PublicVisible = false
	m.publicModels[model.ID] = model
	return model, nil
}

func (m *Memory) UpdateManagedModel(_ context.Context, id string, model domain.PublicModel) (domain.PublicModel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.publicModels[id]; !exists {
		return domain.PublicModel{}, domain.NewError(404, "model_not_found", "模型不存在")
	}
	model.ID = id
	m.publicModels[id] = model
	return model, nil
}

func (p *Postgres) PublicCatalog(ctx context.Context) (domain.PublicCatalog, error) {
	m, err := p.read(ctx)
	if err != nil {
		return domain.PublicCatalog{}, err
	}
	return m.PublicCatalog(ctx)
}

func (p *Postgres) ListManagedModels(ctx context.Context) ([]domain.PublicModel, error) {
	m, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	return m.ListManagedModels(ctx)
}

func (p *Postgres) CreateManagedModel(ctx context.Context, model domain.PublicModel) (result domain.PublicModel, err error) {
	err = p.mutate(ctx, func(m *Memory) error { var e error; result, e = m.CreateManagedModel(ctx, model); return e })
	return
}

func (p *Postgres) UpdateManagedModel(ctx context.Context, id string, model domain.PublicModel) (result domain.PublicModel, err error) {
	err = p.mutate(ctx, func(m *Memory) error { var e error; result, e = m.UpdateManagedModel(ctx, id, model); return e })
	return
}

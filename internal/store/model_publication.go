package store

import (
	"context"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func (m *Memory) BeginModelPublication(_ context.Context, stage domain.ModelPublicationStage) (domain.ModelPublicationStage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.modelPublications == nil {
		m.modelPublications = map[string]domain.ModelPublicationStage{}
	}
	if prior, exists := m.modelPublications[stage.ModelID]; exists {
		return prior, domain.NewError(409, "MODEL_PUBLICATION_IN_PROGRESS", "模型上架已有未完成阶段，请先恢复或人工核对")
	}
	stage.Status = "staging"
	stage.UpdatedAt = time.Now().UTC()
	m.modelPublications[stage.ModelID] = stage
	return stage, nil
}

func (m *Memory) ModelPublication(_ context.Context, modelID string) (domain.ModelPublicationStage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stage, ok := m.modelPublications[modelID]
	if !ok {
		return domain.ModelPublicationStage{}, domain.NewError(404, "MODEL_PUBLICATION_NOT_FOUND", "模型没有上架阶段")
	}
	return stage, nil
}

func (m *Memory) AdvanceModelPublication(_ context.Context, modelID, expected string, stage domain.ModelPublicationStage) (domain.ModelPublicationStage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prior, ok := m.modelPublications[modelID]
	if !ok || prior.Status != expected || prior.RequestID != stage.RequestID || stage.ModelID != modelID {
		return domain.ModelPublicationStage{}, domain.NewError(409, "MODEL_PUBLICATION_CHANGED", "模型上架阶段已变化")
	}
	stage.UpdatedAt = time.Now().UTC()
	m.modelPublications[modelID] = stage
	return stage, nil
}

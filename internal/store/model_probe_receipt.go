package store

import (
	"context"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func (m *Memory) SaveModelProbeReceipt(_ context.Context, receipt domain.ModelProbeReceipt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.modelProbeReceipts == nil {
		m.modelProbeReceipts = map[string]domain.ModelProbeReceipt{}
	}
	m.modelProbeReceipts[receipt.ModelID] = receipt
	return nil
}

func (m *Memory) ModelProbeReceipt(_ context.Context, modelID string) (domain.ModelProbeReceipt, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	receipt, exists := m.modelProbeReceipts[modelID]
	if !exists {
		return domain.ModelProbeReceipt{}, domain.NewError(404, "MODEL_PROBE_NOT_FOUND", "模型尚无真实收费探针记录")
	}
	return receipt, nil
}

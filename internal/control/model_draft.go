package control

import (
	"context"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

// SaveModelDraft is deliberately unable to set the public bit. A complete
// gateway validation and a paid probe remain prerequisites for publication.
func (s *Service) SaveModelDraft(ctx context.Context, subject string, candidate domain.PublicModel, expectedRevision int) (domain.PublicModel, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return domain.PublicModel{}, err
	}
	candidate.ID = strings.TrimSpace(candidate.ID)
	if candidate.PublicVisible || candidate.ExperienceMode != "" {
		return domain.PublicModel{}, domain.NewError(400, "MODEL_DRAFT_ONLY", "草稿工具不能上架或开放在线体验")
	}
	if err := validatePublicModel(candidate); err != nil {
		return domain.PublicModel{}, err
	}
	if err := s.validateProviderIconReference(ctx, candidate.ProviderIconURL); err != nil {
		return domain.PublicModel{}, err
	}
	if expectedRevision == 0 {
		return s.repository.CreateManagedModel(ctx, candidate)
	}
	if expectedRevision < 0 {
		return domain.PublicModel{}, domain.NewError(400, "MODEL_REVISION_INVALID", "目录修订无效")
	}
	models, err := s.repository.ListManagedModels(ctx)
	if err != nil {
		return domain.PublicModel{}, err
	}
	for _, current := range models {
		if current.ID == candidate.ID {
			if current.PublicVisible {
				return domain.PublicModel{}, domain.NewError(409, "MODEL_ALREADY_PUBLISHED", "请先通过下架流程处理已公开模型")
			}
			return s.repository.UpdateManagedModelCAS(ctx, candidate.ID, expectedRevision, candidate)
		}
	}
	return domain.PublicModel{}, domain.NewError(404, "MODEL_NOT_FOUND", "模型目录记录不存在")
}

func (s *Service) UnpublishModel(ctx context.Context, subject, modelID string, expectedRevision int) (domain.PublicModel, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return domain.PublicModel{}, err
	}
	if expectedRevision < 0 || !modelOpsIDPattern.MatchString(modelID) || strings.Contains(modelID, "..") {
		return domain.PublicModel{}, domain.NewError(400, "MODEL_CONFIG_INVALID", "模型 ID 或目录修订无效")
	}
	models, err := s.repository.ListManagedModels(ctx)
	if err != nil {
		return domain.PublicModel{}, err
	}
	for _, current := range models {
		if current.ID == modelID {
			current.PublicVisible = false
			current.ExperienceMode = ""
			return s.repository.UpdateManagedModelCAS(ctx, modelID, expectedRevision, current)
		}
	}
	return domain.PublicModel{}, domain.NewError(404, "MODEL_NOT_FOUND", "模型目录记录不存在")
}

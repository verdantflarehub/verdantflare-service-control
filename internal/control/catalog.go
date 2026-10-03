package control

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func (s *Service) PublicCatalog(ctx context.Context) (domain.PublicCatalog, error) {
	return s.repository.PublicCatalog(ctx)
}

func (s *Service) ListManagedModels(ctx context.Context, subject string) ([]domain.PublicModel, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return nil, err
	}
	return s.repository.ListManagedModels(ctx)
}

func validatePublicModel(model domain.PublicModel) error {
	if !appIDPattern.MatchString(model.ID) || utf8.RuneCountInString(strings.TrimSpace(model.Name)) < 2 || strings.TrimSpace(model.Provider) == "" || strings.TrimSpace(model.Summary) == "" || len(model.Categories) == 0 {
		return domain.NewError(400, "public_model_invalid", "模型 ID、名称、提供方、简介与类别不能为空")
	}
	for _, field := range []string{model.Name, model.Provider, model.Summary, model.Context, model.MaxInput, model.MaxOutput, model.InputPrice, model.OutputPrice, model.CachePrice, model.PriceUnit} {
		if utf8.RuneCountInString(field) > 500 {
			return domain.NewError(400, "public_model_too_long", "模型字段不能超过 500 字符")
		}
	}
	for _, category := range model.Categories {
		if strings.TrimSpace(category) == "" {
			return domain.NewError(400, "public_model_invalid", "模型类别不能为空")
		}
	}
	if (model.InputPrice != "" || model.OutputPrice != "" || model.CachePrice != "") && strings.TrimSpace(model.PriceUnit) == "" {
		return domain.NewError(400, "price_unit_required", "填写公开报价时必须填写单位")
	}
	return nil
}

func (s *Service) CreateManagedModel(ctx context.Context, subject string, model domain.PublicModel) (domain.PublicModel, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return domain.PublicModel{}, err
	}
	model.ID = strings.TrimSpace(model.ID)
	if err := validatePublicModel(model); err != nil {
		return domain.PublicModel{}, err
	}
	return s.repository.CreateManagedModel(ctx, model)
}

func (s *Service) UpdateManagedModel(ctx context.Context, subject, id string, model domain.PublicModel) (domain.PublicModel, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return domain.PublicModel{}, err
	}
	model.ID = id
	if err := validatePublicModel(model); err != nil {
		return domain.PublicModel{}, err
	}
	return s.repository.UpdateManagedModel(ctx, id, model)
}

package control

import (
	"context"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func (s *Service) PublicCatalog(ctx context.Context) (domain.PublicCatalog, error) {
	catalog, err := s.repository.PublicCatalog(ctx)
	if err != nil || len(catalog.Models) == 0 {
		return catalog, err
	}
	available, err := s.gatewayModels(ctx)
	if err != nil {
		return domain.PublicCatalog{}, err
	}
	filtered := make([]domain.PublicModel, 0, len(catalog.Models))
	for _, item := range catalog.Models {
		if _, ok := available[item.ID]; ok {
			filtered = append(filtered, item)
		}
	}
	catalog.Models = filtered
	return catalog, nil
}

func (s *Service) gatewayModels(ctx context.Context) (map[string]struct{}, error) {
	if s.modelGateway == nil {
		return nil, domain.NewError(503, "model_gateway_unconfigured", "模型网关目录尚未配置")
	}
	models, err := s.modelGateway.ListModels(ctx)
	if err != nil {
		return nil, domain.NewError(503, "model_gateway_unavailable", "暂时无法核验模型网关目录")
	}
	return models, nil
}

func (s *Service) publishedGatewayModels(ctx context.Context) ([]domain.PublicModel, error) {
	models, err := s.repository.ListManagedModels(ctx)
	if err != nil {
		return nil, err
	}
	published := make([]domain.PublicModel, 0, len(models))
	for _, item := range models {
		if item.PublicVisible {
			published = append(published, item)
		}
	}
	if len(published) == 0 {
		return published, nil
	}
	available, err := s.gatewayModels(ctx)
	if err != nil {
		return nil, err
	}
	filtered := published[:0]
	for _, item := range published {
		if _, ok := available[item.ID]; ok {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (s *Service) ListGatewayModels(ctx context.Context, subject string) ([]string, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return nil, err
	}
	available, err := s.gatewayModels(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(available))
	for id := range available {
		models = append(models, id)
	}
	sort.Strings(models)
	return models, nil
}

func (s *Service) ListManagedModels(ctx context.Context, subject string) ([]domain.PublicModel, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return nil, err
	}
	return s.repository.ListManagedModels(ctx)
}

func validatePublicModel(model domain.PublicModel) error {
	if model.ExperienceMode != "" && model.ExperienceMode != "chat" {
		return domain.NewError(400, "experience_mode_invalid", "不支持该模型体验方式")
	}
	if model.ExperienceMode != "" && !model.PublicVisible {
		return domain.NewError(400, "experience_requires_publication", "开启在线体验前必须先上架模型")
	}
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
	if model.ExperienceMode != "" {
		return domain.PublicModel{}, domain.NewError(400, "experience_requires_publication", "请先创建并上架模型，再单独开放在线体验")
	}
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
	if model.PublicVisible || model.ExperienceMode != "" {
		available, err := s.gatewayModels(ctx)
		if err != nil {
			return domain.PublicModel{}, err
		}
		if _, ok := available[id]; !ok {
			return domain.PublicModel{}, domain.NewError(409, "model_gateway_missing", "模型未出现在网关可用列表，不能上架")
		}
		if model.ExperienceMode == "chat" {
			chatModels, err := s.modelGateway.ListChatModels(ctx)
			if err != nil {
				return domain.PublicModel{}, domain.NewError(503, "model_gateway_unavailable", "暂时无法核验模型对话端点")
			}
			if _, ok := chatModels[id]; !ok {
				return domain.PublicModel{}, domain.NewError(409, "model_chat_unsupported", "该模型未提供 OpenAI 对话端点，不能开放文本体验")
			}
		}
	}
	return s.repository.UpdateManagedModel(ctx, id, model)
}

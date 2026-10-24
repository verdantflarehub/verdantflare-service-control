package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
)

type PublishModelInput struct {
	ModelID                 string `json:"modelId"`
	SourceChannelID         int    `json:"sourceChannelId"`
	ExpectedCatalogRevision int    `json:"expectedCatalogRevision"`
	ExpectedGatewayRevision string `json:"expectedGatewayRevision"`
	RequestID               string `json:"requestId"`
}

type ModelPublicationResult struct {
	ModelID         string `json:"modelId"`
	CatalogRevision int    `json:"catalogRevision"`
	GatewayRevision string `json:"gatewayRevision"`
	PublicChannelID int    `json:"publicChannelId"`
	Status          string `json:"status"`
}

type modelPublicRouter interface {
	StagePublicRoute(context.Context, gateway.ModelOpsPublicRouteInput) (gateway.ModelOpsPublicRouteResult, error)
	ActivatePublicRoute(context.Context, gateway.ModelOpsPublicRouteInput) (gateway.ModelOpsPublicRouteResult, error)
}

// PublishModel stages a disabled default route before making any public
// directory change. An ambiguous write stops the workflow for reconciliation.
func (s *Service) PublishModel(ctx context.Context, subject string, input PublishModelInput) (ModelPublicationResult, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return ModelPublicationResult{}, err
	}
	if err := validateModelOpsWrite(input.ModelID, input.RequestID, input.ExpectedGatewayRevision); err != nil {
		return ModelPublicationResult{}, err
	}
	if input.SourceChannelID <= 0 || input.ExpectedCatalogRevision <= 0 {
		return ModelPublicationResult{}, domain.NewError(400, "MODEL_CONFIG_INVALID", "渠道或目录修订无效")
	}
	router, ok := s.modelOpsGateway.(modelPublicRouter)
	if !ok {
		return ModelPublicationResult{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关公开路由接口尚未接入")
	}
	models, err := s.repository.ListManagedModels(ctx)
	if err != nil {
		return ModelPublicationResult{}, err
	}
	var model domain.PublicModel
	for _, item := range models {
		if item.ID == input.ModelID {
			model = item
			break
		}
	}
	if model.ID == "" {
		return ModelPublicationResult{}, domain.NewError(404, "MODEL_NOT_FOUND", "模型草稿不存在")
	}
	if model.PublicVisible || model.ExperienceMode != "" || model.Revision != input.ExpectedCatalogRevision {
		return ModelPublicationResult{}, domain.NewError(409, "MODEL_CATALOG_CHANGED", "模型目录状态或修订已变化")
	}
	if err := validatePublicModel(model); err != nil {
		return ModelPublicationResult{}, err
	}
	if err := s.validateProviderIconReference(ctx, model.ProviderIconURL); err != nil {
		return ModelPublicationResult{}, err
	}
	snapshot, err := s.modelOpsGateway.Snapshot(ctx, input.ModelID)
	if err != nil {
		return ModelPublicationResult{}, modelOpsGatewayError(err)
	}
	if snapshot.Revision != input.ExpectedGatewayRevision {
		return ModelPublicationResult{}, domain.NewError(409, "MODEL_CONFIG_CHANGED", "网关修订已变化")
	}
	var source *gateway.ModelOpsChannel
	for i := range snapshot.Channels {
		channel := &snapshot.Channels[i]
		if channel.ID == input.SourceChannelID {
			source = channel
			break
		}
	}
	if source == nil || source.Status != 1 || !source.CredentialConfigured || source.UpstreamModel == "" || source.Type != 43 || len(source.Groups) != 1 || source.Groups[0] != "center-internal" {
		return ModelPublicationResult{}, domain.NewError(409, "MODEL_CHANNEL_NOT_DEDICATED", "来源渠道不是已启用的内部专用渠道")
	}
	if !snapshot.Metadata.Exists || snapshot.Metadata.Status != 1 || snapshot.Metadata.Endpoints != "/v1/chat/completions" {
		return ModelPublicationResult{}, domain.NewError(409, "MODEL_METADATA_NOT_READY", "网关模型元数据未就绪")
	}
	priced := (snapshot.Pricing.BillingMode == "tiered_expr" && strings.TrimSpace(snapshot.Pricing.BillingExpr) != "") ||
		(snapshot.Pricing.BillingMode == "ratio" && snapshot.Pricing.ModelPrice != nil && *snapshot.Pricing.ModelPrice > 0)
	if !priced {
		return ModelPublicationResult{}, domain.NewError(409, "MODEL_BILLING_NOT_READY", "网关计费未就绪")
	}
	receipt, err := s.repository.ModelProbeReceipt(ctx, input.ModelID)
	if err != nil || receipt.GatewayRevision != snapshot.Revision || receipt.BilledQuota <= 0 || receipt.BilledUSD <= 0 || receipt.BilledUSD > 0.01 {
		return ModelPublicationResult{}, domain.NewError(409, "MODEL_PROBE_REQUIRED", "当前网关修订缺少真实结算探针")
	}
	stage := domain.ModelPublicationStage{ModelID: input.ModelID, RequestID: input.RequestID, SourceChannelID: input.SourceChannelID,
		ProbeGatewayRevision: snapshot.Revision, CatalogRevision: model.Revision}
	stage, err = s.repository.BeginModelPublication(ctx, stage)
	if err != nil {
		return ModelPublicationResult{}, err
	}
	return s.resumeModelPublication(ctx, router, stage)
}

func (s *Service) ResumeModelPublication(ctx context.Context, subject, modelID, requestID string) (ModelPublicationResult, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return ModelPublicationResult{}, err
	}
	if !modelOpsIDPattern.MatchString(modelID) || strings.Contains(modelID, "..") || !modelOpsRequestPattern.MatchString(requestID) {
		return ModelPublicationResult{}, domain.NewError(400, "MODEL_CONFIG_INVALID", "恢复参数无效")
	}
	stage, err := s.repository.ModelPublication(ctx, modelID)
	if err != nil {
		return ModelPublicationResult{}, err
	}
	if stage.RequestID != requestID {
		return ModelPublicationResult{}, domain.NewError(409, "MODEL_PUBLICATION_CHANGED", "请求 ID 与原阶段不符")
	}
	router, ok := s.modelOpsGateway.(modelPublicRouter)
	if !ok {
		return ModelPublicationResult{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关公开路由接口尚未接入")
	}
	return s.resumeModelPublication(ctx, router, stage)
}

func (s *Service) resumeModelPublication(ctx context.Context, router modelPublicRouter, stage domain.ModelPublicationStage) (ModelPublicationResult, error) {
	modelID := stage.ModelID
	if stage.Status == "staging" {
		// Reuse the original idempotency key. If the first gateway write succeeded
		// but Control lost the response, this returns the same disabled route.
		staged, err := router.StagePublicRoute(ctx, gateway.ModelOpsPublicRouteInput{ModelID: modelID,
			SourceChannelID: stage.SourceChannelID, RequestID: stage.RequestID, ExpectedGatewayRevision: stage.ProbeGatewayRevision})
		if err != nil {
			return ModelPublicationResult{}, domain.NewError(503, "MODEL_PUBLICATION_OUTCOME_UNKNOWN", "路由预备结果待核对；保留原请求 ID 后恢复")
		}
		if !publicRouteMatches(staged.Channels, stage.SourceChannelID, staged.PublicChannelID, false) {
			return ModelPublicationResult{}, domain.NewError(409, "MODEL_PUBLICATION_READBACK_FAILED", "预备路由不匹配来源渠道")
		}
		stage.PublicChannelID, stage.StagedGatewayRevision, stage.Status = staged.PublicChannelID, staged.Revision, "staged"
		if _, err := s.repository.AdvanceModelPublication(ctx, modelID, "staging", stage); err != nil {
			return ModelPublicationResult{}, err
		}
	}
	if stage.Status == "staged" {
		snapshot, err := s.modelOpsGateway.Snapshot(ctx, modelID)
		if err != nil {
			return ModelPublicationResult{}, modelOpsGatewayError(err)
		}
		if snapshot.Revision != stage.StagedGatewayRevision || !publicRouteMatches(snapshot.Channels, stage.SourceChannelID, stage.PublicChannelID, false) {
			return ModelPublicationResult{}, domain.NewError(409, "MODEL_PUBLICATION_READBACK_FAILED", "禁用的公开路由已变化")
		}
		models, err := s.repository.ListManagedModels(ctx)
		if err != nil {
			return ModelPublicationResult{}, err
		}
		var current domain.PublicModel
		for _, item := range models {
			if item.ID == modelID {
				current = item
				break
			}
		}
		if current.ID == "" {
			return ModelPublicationResult{}, domain.NewError(404, "MODEL_NOT_FOUND", "模型草稿不存在")
		}
		if current.PublicVisible && current.Revision == stage.CatalogRevision+1 {
			stage.CatalogRevision = current.Revision
		} else if !current.PublicVisible && current.Revision == stage.CatalogRevision {
			current.PublicVisible = true
			published, err := s.repository.UpdateManagedModelCAS(ctx, modelID, current.Revision, current)
			if err != nil {
				return ModelPublicationResult{}, err
			}
			stage.CatalogRevision = published.Revision
		} else {
			return ModelPublicationResult{}, domain.NewError(409, "MODEL_CATALOG_CHANGED", "目录状态已变化，拒绝继续公开")
		}
		stage.Status = "catalog"
		if _, err := s.repository.AdvanceModelPublication(ctx, modelID, "staged", stage); err != nil {
			return ModelPublicationResult{}, err
		}
	}
	if stage.Status == "catalog" {
		models, err := s.repository.ListManagedModels(ctx)
		if err != nil {
			return ModelPublicationResult{}, err
		}
		public := false
		for _, item := range models {
			if item.ID == modelID && item.PublicVisible && item.Revision == stage.CatalogRevision {
				public = true
			}
		}
		if !public {
			return ModelPublicationResult{}, domain.NewError(409, "MODEL_CATALOG_CHANGED", "公开目录已变化")
		}
		snapshot, err := s.modelOpsGateway.Snapshot(ctx, modelID)
		if err != nil {
			return ModelPublicationResult{}, modelOpsGatewayError(err)
		}
		if snapshot.Revision == stage.StagedGatewayRevision && publicRouteMatches(snapshot.Channels, stage.SourceChannelID, stage.PublicChannelID, false) {
			digest := sha256.Sum256([]byte(stage.RequestID + "\x00activate"))
			activated, err := router.ActivatePublicRoute(ctx, gateway.ModelOpsPublicRouteInput{ModelID: modelID,
				SourceChannelID: stage.SourceChannelID, PublicChannelID: stage.PublicChannelID,
				RequestID: hex.EncodeToString(digest[:]), ExpectedGatewayRevision: stage.StagedGatewayRevision})
			if err != nil {
				return ModelPublicationResult{}, domain.NewError(503, "MODEL_PUBLICATION_OUTCOME_UNKNOWN", "路由启用结果待核对；保留原请求 ID 后恢复")
			}
			snapshot, err = s.modelOpsGateway.Snapshot(ctx, modelID)
			if err != nil {
				return ModelPublicationResult{}, modelOpsGatewayError(err)
			}
			if activated.Revision != snapshot.Revision {
				return ModelPublicationResult{}, domain.NewError(409, "MODEL_CONFIG_CHANGED", "启用后网关修订已变化")
			}
		}
		if snapshot.Revision == stage.StagedGatewayRevision || !publicRouteMatches(snapshot.Channels, stage.SourceChannelID, stage.PublicChannelID, true) {
			return ModelPublicationResult{}, domain.NewError(409, "MODEL_PUBLICATION_READBACK_FAILED", "公开路由状态回读不一致")
		}
		stage.ActiveGatewayRevision, stage.Status = snapshot.Revision, "active"
		if _, err := s.repository.AdvanceModelPublication(ctx, modelID, "catalog", stage); err != nil {
			return ModelPublicationResult{}, err
		}
	}
	if stage.Status == "active" {
		snapshot, err := s.modelOpsGateway.Snapshot(ctx, modelID)
		if err != nil {
			return ModelPublicationResult{}, modelOpsGatewayError(err)
		}
		if snapshot.Revision != stage.ActiveGatewayRevision || !publicRouteMatches(snapshot.Channels, stage.SourceChannelID, stage.PublicChannelID, true) {
			return ModelPublicationResult{}, domain.NewError(409, "MODEL_PUBLICATION_READBACK_FAILED", "公开路由已变化")
		}
		available, err := s.gatewayModels(ctx)
		if err != nil {
			return ModelPublicationResult{}, err
		}
		if _, ok := available[modelID]; !ok {
			return ModelPublicationResult{}, domain.NewError(409, "MODEL_PUBLICATION_READBACK_FAILED", "目录令牌未看到模型")
		}
		catalog, err := s.PublicCatalog(ctx)
		if err != nil {
			return ModelPublicationResult{}, err
		}
		found := false
		for _, item := range catalog.Models {
			if item.ID == modelID && item.Revision == stage.CatalogRevision {
				found = true
			}
		}
		if !found {
			return ModelPublicationResult{}, domain.NewError(409, "MODEL_PUBLICATION_READBACK_FAILED", "公开目录未回读到模型")
		}
		stage.Status = "verified"
		if _, err := s.repository.AdvanceModelPublication(ctx, modelID, "active", stage); err != nil {
			return ModelPublicationResult{}, err
		}
	}
	if stage.Status != "verified" {
		return ModelPublicationResult{}, domain.NewError(409, "MODEL_PUBLICATION_CHANGED", "未知上架阶段")
	}
	return ModelPublicationResult{ModelID: modelID, CatalogRevision: stage.CatalogRevision,
		GatewayRevision: stage.ActiveGatewayRevision, PublicChannelID: stage.PublicChannelID, Status: stage.Status}, nil
}

func publicRouteMatches(channels []gateway.ModelOpsChannel, sourceID, publicID int, active bool) bool {
	var source, public *gateway.ModelOpsChannel
	for i := range channels {
		if channels[i].ID == sourceID {
			source = &channels[i]
		}
		if channels[i].ID == publicID {
			public = &channels[i]
		}
	}
	if source == nil || public == nil || !source.CredentialConfigured || !public.CredentialConfigured ||
		len(public.Groups) != 1 || public.Groups[0] != "default" || source.UpstreamModel != public.UpstreamModel ||
		source.UpstreamHost != public.UpstreamHost || source.Type != public.Type {
		return false
	}
	if active {
		return public.Status == 1
	}
	return public.Status != 1
}

func (s *Service) ModelPublicationStatus(ctx context.Context, subject, modelID string) (domain.ModelPublicationStage, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return domain.ModelPublicationStage{}, err
	}
	if !modelOpsIDPattern.MatchString(modelID) || strings.Contains(modelID, "..") {
		return domain.ModelPublicationStage{}, domain.NewError(400, "MODEL_CONFIG_INVALID", "模型 ID 无效")
	}
	return s.repository.ModelPublication(ctx, modelID)
}

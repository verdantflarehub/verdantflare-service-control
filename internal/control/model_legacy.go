package control

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
)

const legacyV4ProBillingExpr = `(weekday("UTC") >= 1 && weekday("UTC") <= 5 && ((hour("UTC") >= 1 && hour("UTC") < 4) || (hour("UTC") >= 6 && hour("UTC") < 10))) ? tier("peak", p * 1.32 + cr * 0.044 + c * 3.96) : tier("off_peak", p * 0.66 + cr * 0.022 + c * 1.98)`

func legacyModelID(modelID string) bool {
	return modelID == "deepseek-flash" || modelID == "deepseek-v4-pro"
}

// Existing DeepSeek records predate the endpoint-path metadata convention.
// Accept only their single OpenAI-compatible text endpoint during an in-place
// migration; new models must still declare the explicit chat-completions path.
func legacyTextEndpoint(raw string) bool {
	if strings.TrimSpace(raw) == "/v1/chat/completions" {
		return true
	}
	var endpoints []string
	return json.Unmarshal([]byte(raw), &endpoints) == nil && len(endpoints) == 1 && endpoints[0] == "openai"
}

type LegacyModelPreview struct {
	ModelID         string `json:"modelId"`
	CatalogRevision int    `json:"catalogRevision"`
	GatewayRevision string `json:"gatewayRevision"`
	LegacyChannelID int    `json:"legacyChannelId"`
	BillingMode     string `json:"billingMode"`
	Eligible        bool   `json:"eligible"`
	BlockReason     string `json:"blockReason,omitempty"`
}

func (s *Service) PreviewLegacyModel(ctx context.Context, subject, modelID string) (LegacyModelPreview, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return LegacyModelPreview{}, err
	}
	if !legacyModelID(modelID) {
		return LegacyModelPreview{}, domain.NewError(400, "MODEL_CONFIG_INVALID", "仅支持两个指定的 DeepSeek 存量模型")
	}
	if s.modelOpsGateway == nil {
		return LegacyModelPreview{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关模型配置接口尚未接入")
	}
	models, err := s.repository.ListManagedModels(ctx)
	if err != nil {
		return LegacyModelPreview{}, err
	}
	result := LegacyModelPreview{ModelID: modelID}
	found := false
	for _, item := range models {
		if item.ID == modelID {
			found = true
			result.CatalogRevision = item.Revision
			if !item.PublicVisible {
				result.BlockReason = "目录未公开"
			}
			break
		}
	}
	if !found {
		return LegacyModelPreview{}, domain.NewError(404, "MODEL_NOT_FOUND", "存量模型不存在")
	}
	snapshot, err := s.modelOpsGateway.Snapshot(ctx, modelID)
	if err != nil {
		return LegacyModelPreview{}, modelOpsGatewayError(err)
	}
	result.GatewayRevision, result.BillingMode = snapshot.Revision, snapshot.Pricing.BillingMode
	for _, channel := range snapshot.Channels {
		if len(channel.Groups) == 1 && channel.Groups[0] == "default" && channel.Status == 1 {
			if result.LegacyChannelID != 0 {
				result.BlockReason = "存在多个默认渠道"
				return result, nil
			}
			result.LegacyChannelID = channel.ID
		}
	}
	if result.LegacyChannelID == 0 {
		result.BlockReason = "缺少启用的默认渠道"
	} else if (modelID == "deepseek-flash" && (result.BillingMode != "ratio" || snapshot.Pricing.ModelPrice != nil ||
		snapshot.Pricing.ModelRatio == nil || *snapshot.Pricing.ModelRatio != 0.75 ||
		(snapshot.Pricing.CompletionRatio != nil && *snapshot.Pricing.CompletionRatio != 1))) ||
		(modelID == "deepseek-v4-pro" && (result.BillingMode != "tiered_expr" ||
			snapshot.Pricing.ModelPrice != nil || snapshot.Pricing.BillingExpr != legacyV4ProBillingExpr)) {
		result.BlockReason = "原计费规则不在迁移范围"
	} else if result.BlockReason == "" {
		result.Eligible = true
	}
	return result, nil
}

func (s *Service) PrepareLegacyModel(ctx context.Context, subject string, input gateway.ModelOpsLegacyPrepareInput) (gateway.ModelOpsLegacyPrepareResult, error) {
	if err := validateModelOpsWrite(input.ModelID, input.RequestID, input.ExpectedGatewayRevision); err != nil {
		return gateway.ModelOpsLegacyPrepareResult{}, err
	}
	preview, err := s.PreviewLegacyModel(ctx, subject, input.ModelID)
	if err != nil {
		return gateway.ModelOpsLegacyPrepareResult{}, err
	}
	if !preview.Eligible || preview.LegacyChannelID != input.LegacyChannelID {
		return gateway.ModelOpsLegacyPrepareResult{}, domain.NewError(409, "MODEL_CONFIG_CHANGED", "存量渠道或计费已变化")
	}
	client, ok := s.modelOpsGateway.(interface {
		PrepareLegacy(context.Context, gateway.ModelOpsLegacyPrepareInput) (gateway.ModelOpsLegacyPrepareResult, error)
	})
	if !ok {
		return gateway.ModelOpsLegacyPrepareResult{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关存量渠道预备接口尚未接入")
	}
	result, err := client.PrepareLegacy(ctx, input)
	if err != nil {
		return gateway.ModelOpsLegacyPrepareResult{}, modelOpsGatewayError(err)
	}
	return result, nil
}

type LegacyRecoveryResult struct {
	ModelID         string `json:"modelId"`
	Status          string `json:"status"`
	GatewayRevision string `json:"gatewayRevision"`
	Replayed        bool   `json:"replayed"`
}

func (s *Service) RecoverLegacyModel(ctx context.Context, subject string, input gateway.ModelOpsLegacyRecoveryInput) (LegacyRecoveryResult, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return LegacyRecoveryResult{}, err
	}
	if err := validateModelOpsWrite(input.ModelID, input.RequestID, input.ExpectedGatewayRevision); err != nil {
		return LegacyRecoveryResult{}, err
	}
	if !legacyModelID(input.ModelID) || (input.Action != "abort" && input.Action != "rollback") {
		return LegacyRecoveryResult{}, domain.NewError(400, "MODEL_CONFIG_INVALID", "存量恢复目标或动作无效")
	}
	stage, err := s.repository.ModelPublication(ctx, input.ModelID)
	if err != nil {
		return LegacyRecoveryResult{}, err
	}
	if stage.LegacyChannelID != input.LegacyChannelID || stage.SourceChannelID != input.SourceChannelID ||
		stage.PublicChannelID != input.PublicChannelID || stage.PublicChannelID <= 0 {
		return LegacyRecoveryResult{}, domain.NewError(409, "MODEL_PUBLICATION_CHANGED", "存量上架阶段不匹配")
	}
	terminal := "aborted"
	progress := "aborting"
	if input.Action == "rollback" {
		terminal, progress = "rolled_back", "rolling_back"
	}
	if stage.Status == terminal && stage.RecoveryRequestID == input.RequestID {
		return LegacyRecoveryResult{ModelID: input.ModelID, Status: terminal,
			GatewayRevision: stage.ActiveGatewayRevision, Replayed: true}, nil
	}
	client, ok := s.modelOpsGateway.(interface {
		RecoverLegacy(context.Context, gateway.ModelOpsLegacyRecoveryInput) (gateway.ModelOpsLegacyRecoveryResult, error)
	})
	if !ok {
		return LegacyRecoveryResult{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关存量恢复接口尚未接入")
	}
	if stage.Status != progress {
		valid := (input.Action == "abort" && (stage.Status == "staged" || stage.Status == "catalog")) ||
			(input.Action == "rollback" && (stage.Status == "active" || stage.Status == "verified"))
		if !valid {
			return LegacyRecoveryResult{}, domain.NewError(409, "MODEL_PUBLICATION_CHANGED", "当前阶段不可执行此恢复动作")
		}
		snapshot, err := s.modelOpsGateway.Snapshot(ctx, input.ModelID)
		if err != nil {
			return LegacyRecoveryResult{}, modelOpsGatewayError(err)
		}
		if snapshot.Revision != input.ExpectedGatewayRevision ||
			!publicRouteMatches(snapshot.Channels, stage.SourceChannelID, stage.PublicChannelID, input.Action == "rollback") {
			return LegacyRecoveryResult{}, domain.NewError(409, "MODEL_CONFIG_CHANGED", "存量路由在恢复前已变化")
		}
		previousStatus := stage.Status
		stage.RecoveryRequestID, stage.RecoveryGatewayRevision = input.RequestID, input.ExpectedGatewayRevision
		stage.RecoveryAction, stage.Status = input.Action, progress
		stage, err = s.repository.AdvanceModelPublication(ctx, input.ModelID, previousStatus, stage)
		if err != nil {
			return LegacyRecoveryResult{}, err
		}
	} else if stage.RecoveryRequestID != input.RequestID || stage.RecoveryAction != input.Action ||
		stage.RecoveryGatewayRevision != input.ExpectedGatewayRevision {
		return LegacyRecoveryResult{}, domain.NewError(409, "MODEL_PUBLICATION_CHANGED", "恢复请求与已持久化意图不匹配")
	}
	result, err := client.RecoverLegacy(ctx, input)
	if err != nil {
		return LegacyRecoveryResult{}, domain.NewError(503, "MODEL_PUBLICATION_OUTCOME_UNKNOWN", "存量恢复结果待核对；使用原请求 ID 恢复")
	}
	snapshot, err := s.modelOpsGateway.Snapshot(ctx, input.ModelID)
	if err != nil || snapshot.Revision != result.Revision {
		return LegacyRecoveryResult{}, domain.NewError(503, "MODEL_PUBLICATION_OUTCOME_UNKNOWN", "恢复已提交但回读不可用；使用原请求 ID 核对")
	}
	oldRestored, publicRemoved := false, true
	for _, channel := range snapshot.Channels {
		if channel.ID == input.LegacyChannelID && channel.Status == 1 && len(channel.Groups) == 1 && channel.Groups[0] == "default" {
			oldRestored = true
		}
		if channel.ID == input.PublicChannelID {
			publicRemoved = false
		}
	}
	if !oldRestored || !publicRemoved {
		return LegacyRecoveryResult{}, domain.NewError(409, "MODEL_PUBLICATION_READBACK_FAILED", "旧渠道或新渠道恢复回读不一致")
	}
	stage.ActiveGatewayRevision, stage.Status = result.Revision, terminal
	if _, err := s.repository.AdvanceModelPublication(ctx, input.ModelID, progress, stage); err != nil {
		return LegacyRecoveryResult{}, err
	}
	return LegacyRecoveryResult{ModelID: input.ModelID, Status: terminal,
		GatewayRevision: result.Revision, Replayed: result.Replayed}, nil
}

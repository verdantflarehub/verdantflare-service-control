package control

import (
	"context"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

type ModelPublicationCheck struct {
	Code   string `json:"code"`
	Passed bool   `json:"passed"`
}

type ModelPublicationValidation struct {
	ModelID              string                  `json:"modelId"`
	GatewayRevision      string                  `json:"gatewayRevision"`
	CatalogRevision      int                     `json:"catalogRevision"`
	Checks               []ModelPublicationCheck `json:"checks"`
	Ready                bool                    `json:"ready"`
	ActualChargeVerified bool                    `json:"actualChargeVerified"`
}

// ValidateModelPublication is a read-only, fail-closed preview. A future
// publish tool must additionally verify a bounded real probe and re-read both
// sources immediately before and after changing the public bit.
func (s *Service) ValidateModelPublication(ctx context.Context, subject, modelID string) (ModelPublicationValidation, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return ModelPublicationValidation{}, err
	}
	if !modelOpsIDPattern.MatchString(modelID) || strings.Contains(modelID, "..") {
		return ModelPublicationValidation{}, domain.NewError(400, "MODEL_CONFIG_INVALID", "模型 ID 无效")
	}
	if s.modelOpsGateway == nil {
		return ModelPublicationValidation{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关模型配置接口尚未接入")
	}
	snapshot, err := s.modelOpsGateway.Snapshot(ctx, modelID)
	if err != nil {
		return ModelPublicationValidation{}, modelOpsGatewayError(err)
	}
	result := ModelPublicationValidation{ModelID: modelID, GatewayRevision: snapshot.Revision,
		Checks: make([]ModelPublicationCheck, 0, 6)}
	add := func(code string, passed bool) {
		result.Checks = append(result.Checks, ModelPublicationCheck{Code: code, Passed: passed})
	}
	models, err := s.repository.ListManagedModels(ctx)
	if err != nil {
		return ModelPublicationValidation{}, err
	}
	catalogOK := false
	for _, item := range models {
		if item.ID == modelID {
			result.CatalogRevision = item.Revision
			catalogOK = validatePublicModel(item) == nil
			break
		}
	}
	add("catalog_complete", catalogOK)
	add("gateway_metadata_enabled", snapshot.Metadata.Exists && snapshot.Metadata.Status == 1)
	channelOK := false
	for _, channel := range snapshot.Channels {
		if channel.Status == 1 && channel.CredentialConfigured && channel.UpstreamModel != "" {
			for _, group := range channel.Groups {
				if group == "default" {
					channelOK = true
				}
			}
		}
	}
	add("default_group_channel_ready", channelOK)
	billingOK := false
	switch snapshot.Pricing.BillingMode {
	case "tiered_expr":
		billingOK = strings.TrimSpace(snapshot.Pricing.BillingExpr) != ""
	case "ratio":
		if snapshot.Pricing.ModelPrice != nil {
			billingOK = *snapshot.Pricing.ModelPrice > 0
		} else {
			billingOK = snapshot.Pricing.ModelRatio != nil && *snapshot.Pricing.ModelRatio > 0
		}
	}
	add("gateway_billing_configured", billingOK)
	available, err := s.gatewayModels(ctx)
	if err != nil {
		return ModelPublicationValidation{}, err
	}
	_, visible := available[modelID]
	add("directory_token_exact_model_visible", visible)
	// No paid probe receipt is implemented yet. Returning ready=true here would
	// let a catalog-only change masquerade as a tested billable model.
	add("bounded_actual_charge_probe", false)
	return result, nil
}

package control

import (
	"context"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

type validationOpsGateway struct{ snapshot gateway.ModelOpsSnapshot }

func (g validationOpsGateway) Snapshot(context.Context, string) (gateway.ModelOpsSnapshot, error) {
	return g.snapshot, nil
}
func (validationOpsGateway) BillingPreview(context.Context, gateway.ModelOpsBillingInput) (gateway.ModelOpsBillingPreview, error) {
	return gateway.ModelOpsBillingPreview{}, nil
}
func (validationOpsGateway) BillingApply(context.Context, gateway.ModelOpsBillingInput) (gateway.ModelOpsWriteResult, error) {
	return gateway.ModelOpsWriteResult{}, nil
}
func (validationOpsGateway) AttachAlias(context.Context, gateway.ModelOpsAliasInput) (gateway.ModelOpsWriteResult, error) {
	return gateway.ModelOpsWriteResult{}, nil
}

type validationDirectoryGateway struct{ modelID string }

func (g validationDirectoryGateway) ListModels(context.Context) (map[string]struct{}, error) {
	return map[string]struct{}{g.modelID: {}}, nil
}
func (g validationDirectoryGateway) ListChatModels(context.Context) (map[string]struct{}, error) {
	return map[string]struct{}{g.modelID: {}}, nil
}

func TestModelPublicationValidationCannotPassWithoutActualChargeProbe(t *testing.T) {
	ctx := context.Background()
	repo := store.NewMemorySeeded(time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC))
	const modelID = "internal-deepseek-test"
	_, err := repo.CreateManagedModel(ctx, domain.PublicModel{ID: modelID, Name: "DeepSeek Test", Provider: "DeepSeek",
		Summary: "Internal probe", Categories: []string{"文本生成"}})
	if err != nil {
		t.Fatal(err)
	}
	price := 0.02
	snapshot := gateway.ModelOpsSnapshot{ModelID: modelID, Revision: "revision", Metadata: gateway.ModelOpsMetadata{Exists: true, Status: 1},
		Channels: []gateway.ModelOpsChannel{{ID: 1, Status: 1, Groups: []string{"default"}, UpstreamModel: "deepseek-chat", CredentialConfigured: true}},
		Pricing:  gateway.ModelOpsPricing{BillingMode: "ratio", ModelPrice: &price}}
	service := NewService(repo, validationDirectoryGateway{modelID: modelID})
	service.SetModelOpsGateway(validationOpsGateway{snapshot: snapshot})
	result, err := service.ValidateModelPublication(ctx, "00000000-0000-4000-8000-000000000001", modelID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Ready || result.ActualChargeVerified || len(result.Checks) != 6 || result.Checks[5].Code != "bounded_actual_charge_probe" || result.Checks[5].Passed {
		t.Fatalf("unprobed model passed publication gate: %+v", result)
	}
	for _, check := range result.Checks[:5] {
		if !check.Passed {
			t.Fatalf("expected preflight check %q to pass: %+v", check.Code, result)
		}
	}
	free := 0.0
	ratio := 2.0
	snapshot.Pricing.ModelPrice = &free
	snapshot.Pricing.ModelRatio = &ratio
	service.SetModelOpsGateway(validationOpsGateway{snapshot: snapshot})
	result, err = service.ValidateModelPublication(ctx, "00000000-0000-4000-8000-000000000001", modelID)
	if err != nil || result.Checks[3].Passed {
		t.Fatalf("explicit zero price must not be hidden by a ratio: %+v, %v", result, err)
	}
}

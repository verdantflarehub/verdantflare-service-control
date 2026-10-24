package control

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

type publicationGateway struct {
	snapshot      gateway.ModelOpsSnapshot
	stageFails    int
	activateFails int
}

func (g *publicationGateway) Snapshot(context.Context, string) (gateway.ModelOpsSnapshot, error) {
	return g.snapshot, nil
}
func (*publicationGateway) BillingPreview(context.Context, gateway.ModelOpsBillingInput) (gateway.ModelOpsBillingPreview, error) {
	return gateway.ModelOpsBillingPreview{}, nil
}
func (*publicationGateway) BillingApply(context.Context, gateway.ModelOpsBillingInput) (gateway.ModelOpsWriteResult, error) {
	return gateway.ModelOpsWriteResult{}, nil
}
func (*publicationGateway) AttachAlias(context.Context, gateway.ModelOpsAliasInput) (gateway.ModelOpsWriteResult, error) {
	return gateway.ModelOpsWriteResult{}, nil
}
func (g *publicationGateway) StagePublicRoute(_ context.Context, input gateway.ModelOpsPublicRouteInput) (gateway.ModelOpsPublicRouteResult, error) {
	if g.stageFails > 0 {
		g.stageFails--
		return gateway.ModelOpsPublicRouteResult{}, errors.New("temporary failure")
	}
	if g.snapshot.Revision != input.ExpectedGatewayRevision && g.snapshot.Revision != strings.Repeat("b", 64) {
		return gateway.ModelOpsPublicRouteResult{}, errors.New("stale revision")
	}
	if len(g.snapshot.Channels) == 1 {
		public := g.snapshot.Channels[0]
		public.ID, public.Name, public.Groups, public.Status = 2, "public", []string{"default"}, 2
		g.snapshot.Channels = append(g.snapshot.Channels, public)
		g.snapshot.Revision = strings.Repeat("b", 64)
	}
	return gateway.ModelOpsPublicRouteResult{ModelID: input.ModelID, SourceChannelID: input.SourceChannelID,
		PublicChannelID: 2, Revision: g.snapshot.Revision, Channels: g.snapshot.Channels}, nil
}
func (g *publicationGateway) ActivatePublicRoute(_ context.Context, input gateway.ModelOpsPublicRouteInput) (gateway.ModelOpsPublicRouteResult, error) {
	if g.activateFails > 0 {
		g.activateFails--
		return gateway.ModelOpsPublicRouteResult{}, errors.New("temporary failure")
	}
	if g.snapshot.Revision != input.ExpectedGatewayRevision {
		return gateway.ModelOpsPublicRouteResult{}, errors.New("stale revision")
	}
	g.snapshot.Channels[1].Status = 1
	g.snapshot.Revision = strings.Repeat("c", 64)
	return gateway.ModelOpsPublicRouteResult{ModelID: input.ModelID, SourceChannelID: input.SourceChannelID,
		PublicChannelID: 2, Revision: g.snapshot.Revision, Channels: g.snapshot.Channels}, nil
}

type publicationDirectory struct {
	gateway *publicationGateway
	modelID string
}

func (d publicationDirectory) ListModels(context.Context) (map[string]struct{}, error) {
	if len(d.gateway.snapshot.Channels) > 1 && d.gateway.snapshot.Channels[1].Status == 1 {
		return map[string]struct{}{d.modelID: {}}, nil
	}
	return map[string]struct{}{}, nil
}
func (d publicationDirectory) ListChatModels(ctx context.Context) (map[string]struct{}, error) {
	return d.ListModels(ctx)
}

func TestModelPublicationRecoversActivationWithoutPrematureExposure(t *testing.T) {
	ctx := context.Background()
	const modelID = "internal-deepseek-publication-test"
	repo := store.NewMemorySeeded(time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC))
	draft, err := repo.CreateManagedModel(ctx, domain.PublicModel{ID: modelID, Name: "DeepSeek Internal Test", Provider: "DeepSeek",
		Summary: "Internal model publication verification", Categories: []string{"文本生成"}})
	if err != nil {
		t.Fatal(err)
	}
	price := 0.005
	revision := strings.Repeat("a", 64)
	g := &publicationGateway{stageFails: 1, activateFails: 1, snapshot: gateway.ModelOpsSnapshot{ModelID: modelID, Revision: revision,
		Metadata: gateway.ModelOpsMetadata{Exists: true, Status: 1, Endpoints: "/v1/chat/completions"},
		Pricing:  gateway.ModelOpsPricing{BillingMode: "ratio", ModelPrice: &price},
		Channels: []gateway.ModelOpsChannel{{ID: 1, Name: "internal", Type: 43, Status: 1, CredentialConfigured: true,
			Groups: []string{"center-internal"}, UpstreamModel: "deepseek-chat", UpstreamHost: "https://api.deepseek.com"}}}}
	if err := repo.SaveModelProbeReceipt(ctx, domain.ModelProbeReceipt{ModelID: modelID, RequestID: "probe_publication_001",
		GatewayRevision: revision, BilledQuota: 10, BilledUSD: 0.001, VerifiedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(repo, publicationDirectory{gateway: g, modelID: modelID})
	svc.SetModelOpsGateway(g)
	subject := "00000000-0000-4000-8000-000000000001"
	input := PublishModelInput{ModelID: modelID, SourceChannelID: 1, ExpectedCatalogRevision: draft.Revision,
		ExpectedGatewayRevision: revision, RequestID: "publish_publication_001"}
	if _, err := svc.PublishModel(ctx, subject, input); err == nil {
		t.Fatal("failed activation reported success")
	} else {
		t.Logf("publication stopped: %v", err)
	}
	stage, err := repo.ModelPublication(ctx, modelID)
	if err != nil || stage.Status != "staging" || stage.PublicChannelID != 0 {
		t.Fatalf("stage after failure: %+v, %v", stage, err)
	}
	public, err := svc.PublicCatalog(ctx)
	if err != nil || len(public.Models) != 0 {
		t.Fatalf("disabled route exposed model: %+v, %v", public.Models, err)
	}
	if _, err := svc.ResumeModelPublication(ctx, subject, modelID, input.RequestID); err == nil {
		t.Fatal("failed activation reported success")
	}
	stage, err = repo.ModelPublication(ctx, modelID)
	if err != nil || stage.Status != "catalog" || stage.PublicChannelID != 2 {
		t.Fatalf("stage after activation failure: %+v, %v", stage, err)
	}
	public, err = svc.PublicCatalog(ctx)
	if err != nil || len(public.Models) != 0 {
		t.Fatalf("disabled route exposed model after catalog CAS: %+v, %v", public.Models, err)
	}
	result, err := svc.ResumeModelPublication(ctx, subject, modelID, input.RequestID)
	if err != nil || result.Status != "verified" || result.PublicChannelID != 2 {
		t.Fatalf("resume: %+v, %v", result, err)
	}
	public, err = svc.PublicCatalog(ctx)
	if err != nil || len(public.Models) != 1 {
		t.Fatalf("verified model missing: %+v, %v", public.Models, err)
	}
	validation, err := svc.ValidateModelPublication(ctx, subject, modelID)
	if err != nil || !validation.Ready || !validation.ActualChargeVerified {
		t.Fatalf("validation: %+v, %v", validation, err)
	}
	if _, err := svc.ResumeModelPublication(ctx, subject, modelID, "different_request_id_001"); err == nil {
		t.Fatal("different request ID resumed publication")
	}
}

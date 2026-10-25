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
func (g *publicationGateway) PrepareLegacy(_ context.Context, input gateway.ModelOpsLegacyPrepareInput) (gateway.ModelOpsLegacyPrepareResult, error) {
	if input.ExpectedGatewayRevision != g.snapshot.Revision {
		return gateway.ModelOpsLegacyPrepareResult{}, errors.New("stale revision")
	}
	return gateway.ModelOpsLegacyPrepareResult{ModelID: input.ModelID, LegacyChannelID: input.LegacyChannelID,
		ChannelID: 4, Revision: strings.Repeat("d", 64)}, nil
}
func (g *publicationGateway) RecoverLegacy(_ context.Context, input gateway.ModelOpsLegacyRecoveryInput) (gateway.ModelOpsLegacyRecoveryResult, error) {
	if g.snapshot.Revision != input.ExpectedGatewayRevision {
		return gateway.ModelOpsLegacyRecoveryResult{}, errors.New("stale revision")
	}
	kept := g.snapshot.Channels[:0]
	for _, channel := range g.snapshot.Channels {
		if channel.ID != input.PublicChannelID {
			kept = append(kept, channel)
		}
	}
	g.snapshot.Channels = kept
	if input.Action == "rollback" {
		g.snapshot.Channels = append(g.snapshot.Channels, gateway.ModelOpsChannel{ID: input.LegacyChannelID,
			Status: 1, Type: 43, Groups: []string{"default"}, CredentialConfigured: true})
	}
	g.snapshot.Revision = strings.Repeat("e", 64)
	return gateway.ModelOpsLegacyRecoveryResult{ModelID: input.ModelID, LegacyChannelID: input.LegacyChannelID,
		SourceChannelID: input.SourceChannelID, PublicChannelID: input.PublicChannelID,
		Action: input.Action, Revision: g.snapshot.Revision, Channels: g.snapshot.Channels}, nil
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
	newRouteFound := false
	for _, channel := range g.snapshot.Channels {
		if channel.ID == 2 {
			newRouteFound = true
		}
	}
	if !newRouteFound {
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
	for i := range g.snapshot.Channels {
		if g.snapshot.Channels[i].ID == 2 {
			g.snapshot.Channels[i].Status = 1
		}
	}
	if input.LegacyChannelID > 0 {
		kept := g.snapshot.Channels[:0]
		for _, channel := range g.snapshot.Channels {
			if channel.ID != input.LegacyChannelID {
				kept = append(kept, channel)
			}
		}
		g.snapshot.Channels = kept
	}
	g.snapshot.Revision = strings.Repeat("c", 64)
	return gateway.ModelOpsPublicRouteResult{ModelID: input.ModelID, SourceChannelID: input.SourceChannelID,
		PublicChannelID: 2, Revision: g.snapshot.Revision, Channels: g.snapshot.Channels}, nil
}

type publicationDirectory struct {
	gateway *publicationGateway
	modelID string
}

func (d publicationDirectory) ListModels(context.Context) (map[string]struct{}, error) {
	for _, channel := range d.gateway.snapshot.Channels {
		if channel.Status == 1 && len(channel.Groups) == 1 && channel.Groups[0] == "default" {
			return map[string]struct{}{d.modelID: {}}, nil
		}
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

func TestLegacyModelPublicationKeepsPublicCatalogWhileReplacingRoute(t *testing.T) {
	ctx := context.Background()
	const modelID = "deepseek-flash"
	repo := store.NewMemorySeeded(time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC))
	draft, err := repo.CreateManagedModel(ctx, domain.PublicModel{ID: modelID, Name: "DeepSeek Flash", Provider: "DeepSeek",
		Summary: "Public DeepSeek model", Categories: []string{"文本生成"}})
	if err != nil {
		t.Fatal(err)
	}
	draft.PublicVisible = true
	public, err := repo.UpdateManagedModelCAS(ctx, modelID, draft.Revision, draft)
	if err != nil {
		t.Fatal(err)
	}
	ratio := 0.75
	revision := strings.Repeat("a", 64)
	g := &publicationGateway{snapshot: gateway.ModelOpsSnapshot{ModelID: modelID, Revision: revision,
		Metadata: gateway.ModelOpsMetadata{Exists: true, Status: 1, Endpoints: "/v1/chat/completions"},
		Pricing:  gateway.ModelOpsPricing{BillingMode: "ratio", ModelRatio: &ratio},
		Channels: []gateway.ModelOpsChannel{
			{ID: 1, Name: "internal", Type: 43, Status: 1, CredentialConfigured: true,
				Groups: []string{"center-internal"}, UpstreamModel: "deepseek-chat", UpstreamHost: "https://api.deepseek.com"},
			{ID: 3, Name: "shared-old", Type: 43, Status: 1, CredentialConfigured: true,
				Groups: []string{"default"}, UpstreamModel: "deepseek-chat", UpstreamHost: "https://api.deepseek.com"},
		}}}
	if err := repo.SaveModelProbeReceipt(ctx, domain.ModelProbeReceipt{ModelID: modelID, RequestID: "legacy_probe_0001",
		GatewayRevision: revision, BilledQuota: 10, BilledUSD: 0.001, VerifiedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(repo, publicationDirectory{gateway: g, modelID: modelID})
	svc.SetModelOpsGateway(g)
	subject := "00000000-0000-4000-8000-000000000001"
	before, err := svc.PublicCatalog(ctx)
	if err != nil || len(before.Models) != 1 {
		t.Fatalf("legacy model unavailable before migration: %+v, %v", before.Models, err)
	}
	result, err := svc.PublishModel(ctx, subject, PublishModelInput{ModelID: modelID,
		SourceChannelID: 1, LegacyChannelID: 3, ExpectedCatalogRevision: public.Revision,
		ExpectedGatewayRevision: revision, RequestID: "legacy_publish_request_001"})
	if err != nil || result.Status != "verified" || result.CatalogRevision != public.Revision {
		t.Fatalf("legacy publish: %+v, %v", result, err)
	}
	if !legacyChannelRemoved(g.snapshot.Channels, 3) {
		t.Fatal("old route still serves migrated model")
	}
	after, err := svc.PublicCatalog(ctx)
	if err != nil || len(after.Models) != 1 || after.Models[0].Revision != public.Revision {
		t.Fatalf("public catalog changed: %+v, %v", after.Models, err)
	}
	recovered, err := svc.RecoverLegacyModel(ctx, subject, gateway.ModelOpsLegacyRecoveryInput{ModelID: modelID,
		LegacyChannelID: 3, SourceChannelID: 1, PublicChannelID: 2, Action: "rollback",
		RequestID: "legacy_recovery_request_001", ExpectedGatewayRevision: result.GatewayRevision})
	if err != nil || recovered.Status != "rolled_back" {
		t.Fatalf("legacy rollback: %+v, %v", recovered, err)
	}
	after, err = svc.PublicCatalog(ctx)
	if err != nil || len(after.Models) != 1 || after.Models[0].Revision != public.Revision {
		t.Fatalf("public catalog changed by rollback: %+v, %v", after.Models, err)
	}
}

func TestLegacyPreviewAndPrepareRequireExactExistingRoute(t *testing.T) {
	ctx := context.Background()
	repo := store.NewMemorySeeded(time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC))
	draft, err := repo.CreateManagedModel(ctx, domain.PublicModel{ID: "deepseek-flash", Name: "DeepSeek Flash",
		Provider: "DeepSeek", Summary: "Public DeepSeek model", Categories: []string{"文本生成"}})
	if err != nil {
		t.Fatal(err)
	}
	draft.PublicVisible = true
	if _, err := repo.UpdateManagedModelCAS(ctx, draft.ID, draft.Revision, draft); err != nil {
		t.Fatal(err)
	}
	ratio := 0.75
	revision := strings.Repeat("a", 64)
	g := &publicationGateway{snapshot: gateway.ModelOpsSnapshot{ModelID: draft.ID, Revision: revision,
		Pricing: gateway.ModelOpsPricing{BillingMode: "ratio", ModelRatio: &ratio},
		Channels: []gateway.ModelOpsChannel{{ID: 3, Status: 1, Type: 43, CredentialConfigured: true,
			Groups: []string{"default"}}}}}
	svc := NewService(repo, publicationDirectory{gateway: g, modelID: draft.ID})
	svc.SetModelOpsGateway(g)
	subject := "00000000-0000-4000-8000-000000000001"
	preview, err := svc.PreviewLegacyModel(ctx, subject, draft.ID)
	if err != nil || !preview.Eligible || preview.LegacyChannelID != 3 {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	if _, err := svc.PrepareLegacyModel(ctx, subject, gateway.ModelOpsLegacyPrepareInput{ModelID: draft.ID,
		LegacyChannelID: 2, RequestID: "legacy_prepare_request_001", ExpectedGatewayRevision: revision}); err == nil {
		t.Fatal("wrong old route accepted")
	}
	result, err := svc.PrepareLegacyModel(ctx, subject, gateway.ModelOpsLegacyPrepareInput{ModelID: draft.ID,
		LegacyChannelID: 3, RequestID: "legacy_prepare_request_002", ExpectedGatewayRevision: revision})
	if err != nil || result.ChannelID != 4 {
		t.Fatalf("prepare: %+v, %v", result, err)
	}
	if _, err := svc.PreviewLegacyModel(ctx, subject, "unrelated-model"); err == nil {
		t.Fatal("unreviewed model accepted")
	}
}

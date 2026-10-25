package store

import (
	"context"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func TestModelPublicationStageSurvivesAggregatePersistenceAndCAS(t *testing.T) {
	ctx := context.Background()
	memory := NewMemoryBootstrap()
	stage, err := memory.BeginModelPublication(ctx, domain.ModelPublicationStage{ModelID: "internal-test-model",
		RequestID: "publish_request_0001", SourceChannelID: 7, ProbeGatewayRevision: "source-revision", CatalogRevision: 1})
	if err != nil || stage.Status != "staging" {
		t.Fatalf("begin: %+v, %v", stage, err)
	}
	encoded, err := encodeMemory(memory)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeMemory(encoded)
	if err != nil {
		t.Fatal(err)
	}
	stage, err = restored.ModelPublication(ctx, "internal-test-model")
	if err != nil || stage.RequestID != "publish_request_0001" || stage.Status != "staging" {
		t.Fatalf("restored: %+v, %v", stage, err)
	}
	stage.Status, stage.PublicChannelID = "staged", 9
	if _, err := restored.AdvanceModelPublication(ctx, stage.ModelID, "catalog", stage); err == nil {
		t.Fatal("stale stage accepted")
	}
	advanced, err := restored.AdvanceModelPublication(ctx, stage.ModelID, "staging", stage)
	if err != nil || advanced.Status != "staged" || advanced.PublicChannelID != 9 {
		t.Fatalf("advanced: %+v, %v", advanced, err)
	}
	if _, err := restored.BeginModelPublication(ctx, domain.ModelPublicationStage{ModelID: stage.ModelID, RequestID: "different_request_001"}); err == nil {
		t.Fatal("second publication replaced in-progress stage")
	}
}

func TestModelPublicationCanRestartOnlyAfterTerminalRecovery(t *testing.T) {
	ctx := context.Background()
	for _, terminal := range []string{"aborted", "rolled_back"} {
		memory := NewMemoryBootstrap()
		first, err := memory.BeginModelPublication(ctx, domain.ModelPublicationStage{
			ModelID: "deepseek-flash", RequestID: "legacy_publish_request_001", SourceChannelID: 7,
		})
		if err != nil {
			t.Fatal(err)
		}
		first.Status = terminal
		if _, err := memory.AdvanceModelPublication(ctx, first.ModelID, "staging", first); err != nil {
			t.Fatal(err)
		}
		if _, err := memory.BeginModelPublication(ctx, domain.ModelPublicationStage{
			ModelID: first.ModelID, RequestID: first.RequestID,
		}); err == nil {
			t.Fatal("old request ID restarted a recovered publication")
		}
		next, err := memory.BeginModelPublication(ctx, domain.ModelPublicationStage{
			ModelID: first.ModelID, RequestID: "legacy_publish_request_002", SourceChannelID: 8,
		})
		if err != nil || next.Status != "staging" || next.SourceChannelID != 8 {
			t.Fatalf("restart: %+v, %v", next, err)
		}
		if _, err := memory.AdvanceModelPublication(ctx, first.ModelID, "staging", first); err == nil {
			t.Fatal("old request changed the new publication")
		}
	}
}

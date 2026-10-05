package store

import (
	"context"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func TestModelExperienceRunIdempotencyIsolationAndCleanup(t *testing.T) {
	repository := NewMemoryBootstrap()
	ctx := context.Background()
	now := time.Now().UTC()
	run := domain.ModelExperienceRun{
		ID: "mrun_one", RequestID: "request_id_123456789", OrganizationID: "org_verdantflare",
		CenterUserID: "cu_owner", ModelID: "deepseek-flash", Prompt: "你好", Status: "submitting",
		CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
	created, fresh, err := repository.CreateModelExperienceRun(ctx, run)
	if err != nil || !fresh || created.ID != run.ID {
		t.Fatalf("create: %+v %t %v", created, fresh, err)
	}
	snapshot, err := encodeMemory(repository)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := decodeMemory(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := reopened.GetModelExperienceRun(ctx, run.OrganizationID, run.CenterUserID, run.ID)
	if err != nil || persisted.Prompt != run.Prompt {
		t.Fatalf("run did not survive repository serialization: %+v %v", persisted, err)
	}
	duplicate, fresh, err := repository.CreateModelExperienceRun(ctx, run)
	if err != nil || fresh || duplicate.ID != run.ID {
		t.Fatalf("idempotent retry: %+v %t %v", duplicate, fresh, err)
	}
	changed := run
	changed.Prompt = "其他请求"
	if _, _, err := repository.CreateModelExperienceRun(ctx, changed); err == nil {
		t.Fatal("same request ID accepted a changed prompt")
	}
	if _, err := repository.GetModelExperienceRun(ctx, run.OrganizationID, "cu_other", run.ID); err == nil {
		t.Fatal("another user read the prompt")
	}
	if _, err := repository.GetModelExperienceRun(ctx, "org_other", run.CenterUserID, run.ID); err == nil {
		t.Fatal("another organization read the run")
	}
	quota := 362
	finished, err := repository.FinishModelExperienceRun(ctx, run.OrganizationID, run.CenterUserID, run.ID, "completed", "真实回答", "", 3, 4, 7, &quota)
	if err != nil || finished.Response != "真实回答" || finished.TotalTokens != 7 || finished.BilledQuota == nil || *finished.BilledQuota != quota {
		t.Fatalf("finish: %+v %v", finished, err)
	}
	if err := repository.SweepModelExperienceRuns(ctx, now.Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetModelExperienceRun(ctx, run.OrganizationID, run.CenterUserID, run.ID); err == nil {
		t.Fatal("expired prompt and response remain readable")
	}
}

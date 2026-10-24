package store

import (
	"context"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func TestManagedModelCASRejectsStaleEditorialWrite(t *testing.T) {
	ctx := context.Background()
	memory := NewMemoryBootstrap()
	base := domain.PublicModel{ID: "internal-test-model", Name: "Internal test", Provider: "Provider", Summary: "Draft", Categories: []string{"文本生成"}}
	created, err := memory.CreateManagedModel(ctx, base)
	if err != nil || created.Revision != 1 || created.PublicVisible {
		t.Fatalf("create = %+v, %v", created, err)
	}
	updated, err := memory.UpdateManagedModelCAS(ctx, base.ID, 1, base)
	if err != nil || updated.Revision != 2 {
		t.Fatalf("CAS update = %+v, %v", updated, err)
	}
	_, err = memory.UpdateManagedModelCAS(ctx, base.ID, 1, base)
	if err == nil {
		t.Fatal("stale model revision was accepted")
	}
	current, err := memory.ListManagedModels(ctx)
	if err != nil || len(current) != 1 || current[0].Revision != 2 {
		t.Fatalf("stale write changed catalog: %+v, %v", current, err)
	}
}

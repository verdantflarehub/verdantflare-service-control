package store

import (
	"context"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func TestTransferLoginUserMovesOnlyMembershipAndClearsPendingRecord(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryBootstrap()
	for _, id := range []string{"org_source", "org_target"} {
		if _, err := m.CreateManagedOrganization(ctx, domain.Organization{OrganizationID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.AddMember(ctx, "org_target", domain.Member{Email: "person@example.test", Role: "成员", Status: "待邀请"}); err != nil {
		t.Fatal(err)
	}
	original, err := m.BindLoginUser(ctx, "org_source", "person-id", "person@example.test", "成员")
	if err != nil {
		t.Fatal(err)
	}
	moved, err := m.TransferLoginUser(ctx, "org_source", "org_target", original.ID, "开发者", "operator")
	if err != nil || moved.ID != original.ID || moved.CenterUserID != original.CenterUserID || moved.Role != "开发者" {
		t.Fatalf("moved = %+v, %v", moved, err)
	}
	source, _ := m.ListMembers(ctx, "org_source")
	target, _ := m.ListMembers(ctx, "org_target")
	if len(source) != 0 || len(target) != 1 || target[0].ID != original.ID {
		t.Fatalf("source=%+v target=%+v", source, target)
	}
	center, err := m.CenterContext(ctx, original.ID)
	if err != nil || center.ActiveOrganizationID != "org_target" || len(center.Organizations) != 1 {
		t.Fatalf("context = %+v, %v", center, err)
	}
	if _, err := m.TransferLoginUser(ctx, "org_source", "org_target", original.ID, "开发者", "operator"); err == nil {
		t.Fatal("repeat transfer must conflict instead of changing membership")
	}
	encoded, err := encodeMemory(m)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := decodeMemory(encoded)
	if err != nil {
		t.Fatal(err)
	}
	persisted, _ := reloaded.ListMembers(ctx, "org_target")
	if len(persisted) != 1 || persisted[0].ID != original.ID {
		t.Fatalf("persisted target = %+v", persisted)
	}
}

func TestTransferLoginUserRejectsLastAdminSelfAndFrozenTarget(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryBootstrap()
	for _, id := range []string{"org_source", "org_target"} {
		if _, err := m.CreateManagedOrganization(ctx, domain.Organization{OrganizationID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	member, err := m.BindLoginUser(ctx, "org_source", "admin-id", "admin@example.test", "组织管理员")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.TransferLoginUser(ctx, "org_source", "org_target", member.ID, "成员", member.ID); err == nil {
		t.Fatal("self transfer succeeded")
	}
	if _, err := m.TransferLoginUser(ctx, "org_source", "org_target", member.ID, "成员", "operator"); err == nil {
		t.Fatal("last administrator transferred")
	}
	if _, err := m.BindLoginUser(ctx, "org_source", "second-admin", "second@example.test", "组织管理员"); err != nil {
		t.Fatal(err)
	}
	target, err := m.ManagedOrganization(ctx, "org_target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateManagedOrganization(ctx, "org_target", domain.Organization{Status: "冻结", Plan: target.Organization.Plan}, nil, target.Organization.EntitlementVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := m.TransferLoginUser(ctx, "org_source", "org_target", member.ID, "成员", "operator"); err == nil {
		t.Fatal("frozen target accepted transfer")
	}
	source, _ := m.ListMembers(ctx, "org_source")
	if len(source) != 2 {
		t.Fatalf("failed transfers changed source: %+v", source)
	}
}

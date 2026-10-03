package store

import (
	"context"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func TestMemberManagementRoundTripAndPermissions(t *testing.T) {
	ctx := context.Background()
	m := NewMemorySeeded(time.Now().UTC())
	orgID := "org_verdantflare"
	created, err := m.AddMember(ctx, orgID, domain.Member{Email: "new@example.test", Name: "New", Role: "成员", Status: "待邀请"})
	if err != nil || created.ID == "" {
		t.Fatalf("create pending member = %+v, %v", created, err)
	}
	updated, err := m.UpdateMember(ctx, orgID, created.ID, "开发者", "已取消", "support")
	if err != nil || updated.Role != "开发者" || updated.Status != "已取消" {
		t.Fatalf("update pending member = %+v, %v", updated, err)
	}
	if _, err := m.UpdateMember(ctx, orgID, created.ID, "开发者", "正常", "support"); err == nil {
		t.Fatal("unbound record became active without Login binding")
	}

	boundSubject := "00000000-0000-4000-8000-000000000001"
	if _, err := m.UpdateMember(ctx, orgID, boundSubject, "开发者", "正常", "support"); err != nil {
		t.Fatal(err)
	}
	if _, roles, _, err := m.ActiveOrganization(ctx, boundSubject); err != nil || len(roles) == 0 || roles[0] != "developer" {
		t.Fatalf("updated business role not effective: roles=%v err=%v", roles, err)
	}
	if _, err := m.UpdateMember(ctx, orgID, "logto_01vf9k2", "成员", "停用", "support"); err == nil {
		t.Fatal("last active organization admin was disabled")
	}
	if _, err := m.UpdateMember(ctx, orgID, boundSubject, "开发者", "停用", "support"); err != nil {
		t.Fatal(err)
	}
	if organization, _, _, err := m.ActiveOrganization(ctx, boundSubject); err != nil || organization.OrganizationID == orgID {
		t.Fatalf("disabled user retained active organization: %+v, %v", organization, err)
	}
	if _, err := m.SetActiveOrganization(ctx, boundSubject, orgID); err == nil {
		t.Fatal("disabled user switched back to the organization")
	}
	if _, err := m.UpdateMember(ctx, orgID, "logto_01vf9k2", "成员", "正常", "logto_01vf9k2"); err == nil {
		t.Fatal("operator changed their own role")
	}

	raw, err := encodeMemory(m)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := decodeMemory(raw)
	if err != nil {
		t.Fatal(err)
	}
	members, err := reloaded.ListMembers(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	foundCancelled, foundDisabled := false, false
	for _, member := range members {
		foundCancelled = foundCancelled || member.ID == created.ID && member.Status == "已取消"
		foundDisabled = foundDisabled || member.ID == boundSubject && member.Status == "停用"
	}
	if !foundCancelled || !foundDisabled {
		t.Fatalf("member updates did not survive repository serialization: %+v", members)
	}
}

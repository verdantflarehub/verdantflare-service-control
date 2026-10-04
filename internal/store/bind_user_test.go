package store

import (
	"context"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"testing"
)

func TestBindLoginUserReplacesPendingRecordAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryBootstrap()
	organization, err := m.CreateManagedOrganization(ctx, domain.Organization{OrganizationID: "org_DV97FRRNE4Q", Name: "演示用户"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.AddMember(ctx, organization.Organization.OrganizationID, domain.Member{Email: "zhaotao0919@hotmail.com", Role: "组织管理员", Status: "待邀请"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.BindLoginUser(ctx, organization.Organization.OrganizationID, "043755da-ad63-453e-89f3-a71cd3a67ba2", "zhaotao0919@hotmail.com", "组织管理员")
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.BindLoginUser(ctx, organization.Organization.OrganizationID, "043755da-ad63-453e-89f3-a71cd3a67ba2", "zhaotao0919@hotmail.com", "组织管理员")
	if err != nil || first.CenterUserID != second.CenterUserID {
		t.Fatalf("idempotency: %+v %+v %v", first, second, err)
	}
	context, err := m.CenterContext(ctx, first.ID)
	if err != nil || context.ActiveOrganizationID != organization.Organization.OrganizationID || len(context.Organizations) != 1 || len(context.Organizations[0].Roles) != 1 || context.Organizations[0].Roles[0] != "organization_admin" {
		t.Fatalf("context: %+v %v", context, err)
	}
	members, err := m.ListMembers(ctx, organization.Organization.OrganizationID)
	if err != nil || len(members) != 1 || members[0].ID != first.ID {
		t.Fatalf("members: %+v %v", members, err)
	}
	if _, err := m.BindLoginUser(ctx, organization.Organization.OrganizationID, first.ID, first.Email, "成员"); err == nil {
		t.Fatal("role change must conflict")
	}
}

package store

import (
	"context"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func TestHandoffIdentityRequiresExactActiveMembership(t *testing.T) {
	m := NewMemoryBootstrap()
	m.identityRevision = 7
	const subject = "00000000-0000-4000-8000-000000000001"
	user := m.users[subject]
	user.Memberships[0].Status = "正常"
	m.users[subject] = user

	got, err := m.ResolveHandoffIdentity(context.Background(), subject, "org_verdantflare")
	if err != nil || got.SubjectRef != subject || got.CenterUserRef != user.CenterUserID || got.IdentityRevision != "7" {
		t.Fatalf("exact identity: %+v %v", got, err)
	}
	for _, trial := range []struct{ subject, centerID, orgID string }{
		{"other", user.CenterUserID, "org_verdantflare"},
		{subject, "wrong-center-user", "org_verdantflare"},
		{subject, user.CenterUserID, "org_other"},
	} {
		if _, err := m.CheckHandoffIdentity(context.Background(), trial.subject, trial.centerID, trial.orgID); err == nil {
			t.Fatalf("invalid scope accepted: %+v", trial)
		}
	}
	user.Memberships[0].Status = "停用"
	m.users[subject] = user
	if _, err := m.ResolveHandoffIdentity(context.Background(), subject, "org_verdantflare"); err == nil {
		t.Fatal("disabled membership accepted")
	}
	user.Memberships[0].Status = "正常"
	m.users[subject] = user
	org := m.organizations["org_verdantflare"]
	org.Status = "停用"
	m.organizations["org_verdantflare"] = org
	if _, err := m.ResolveHandoffIdentity(context.Background(), subject, "org_verdantflare"); err == nil {
		t.Fatal("disabled organization accepted")
	}
	org.Status = "正常"
	m.organizations["org_verdantflare"] = org
	alias := user
	alias.LoginSubject = "other-login-subject"
	alias.Memberships = []domain.Membership{{OrganizationID: "org_verdantflare", Status: "正常", Roles: []string{"member"}}}
	m.users[alias.LoginSubject] = alias
	if _, err := m.ResolveHandoffIdentity(context.Background(), subject, "org_verdantflare"); err == nil {
		t.Fatal("ambiguous Center user mapping accepted")
	}
	delete(m.users, alias.LoginSubject)
	m.identityRevision = 0
	if _, err := m.ResolveHandoffIdentity(context.Background(), subject, "org_verdantflare"); err == nil {
		t.Fatal("uninitialized revision accepted")
	}
}

package store

import (
	"context"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func TestDirectoryMemberRequiresExplicitMemberAndCurrentRevision(t *testing.T) {
	m := NewMemoryBootstrap()
	m.identityRevision = 7
	const subject = "00000000-0000-4000-8000-000000000001"
	const org = "org_verdantflare"
	ctx := context.Background()
	revision, _, err := m.DirectoryRevision(ctx)
	if err != nil || revision != "7" {
		t.Fatalf("revision: %q %v", revision, err)
	}
	user := m.users[subject]
	user.Memberships[0].Status = "正常"
	m.users[subject] = user
	if _, err := m.CheckDirectoryMember(ctx, subject, org, revision); err == nil {
		t.Fatal("administrator without explicit member role accepted")
	}
	user.Memberships[0].Roles = append(user.Memberships[0].Roles, "member")
	m.users[subject] = user
	fact, err := m.CheckDirectoryMember(ctx, subject, org, revision)
	if err != nil || fact.SubjectRef != subject || fact.CenterUserRef != user.CenterUserID || fact.IdentityRevision != revision {
		t.Fatalf("member fact: %+v %v", fact, err)
	}
	if _, err := m.CheckDirectoryMember(ctx, subject, org, "6"); err == nil {
		t.Fatal("stale revision accepted")
	} else if problem, ok := err.(*domain.Error); !ok || problem.Status != 409 {
		t.Fatalf("stale revision error: %v", err)
	}
	user.Memberships[0].Status = "停用"
	m.users[subject] = user
	if _, err := m.CheckDirectoryMember(ctx, subject, org, revision); err == nil {
		t.Fatal("disabled member accepted")
	}
	user.Memberships[0].Status = "正常"
	m.users[subject] = user
	organization := m.organizations[org]
	organization.Status = "冻结"
	m.organizations[org] = organization
	if _, err := m.CheckDirectoryMember(ctx, subject, org, revision); err == nil {
		t.Fatal("frozen organization accepted")
	}
	m.identityRevision = 0
	if _, _, err := m.DirectoryRevision(ctx); err == nil {
		t.Fatal("uninitialized revision accepted")
	}
}

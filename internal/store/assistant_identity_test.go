package store

import (
	"context"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func assistantFixture() *Memory {
	m := NewMemoryBootstrap()
	m.identityRevision = 9007199254740993
	m.users = map[string]domain.CenterUser{
		"login-a": {CenterUserID: "owner-a", LoginSubject: "login-a", ActiveOrganizationID: "org-a",
			Memberships: []domain.Membership{{OrganizationID: "org-a", Roles: []string{"member"}, Status: "正常"}}},
	}
	m.organizations = map[string]domain.Organization{"org-a": {OrganizationID: "org-a", Status: "正常"}, "org-b": {OrganizationID: "org-b", Status: "正常"}}
	return m
}

func TestAssistantIdentityExactBindingAndNoFallback(t *testing.T) {
	m := assistantFixture()
	identity, err := m.assistantIdentity("login-a", "", "")
	if err != nil || identity.UserID != "owner-a" || identity.OrgID != "org-a" || identity.Revision != "9007199254740993" {
		t.Fatalf("unexpected identity: %+v, %v", identity, err)
	}
	if _, err := m.assistantIdentity("", "owner-a", "org-b"); err == nil {
		t.Fatal("cross-organization check accepted")
	}
	user := m.users["login-a"]
	user.ActiveOrganizationID = "missing"
	m.users["login-a"] = user
	if _, err := m.assistantIdentity("login-a", "", ""); err == nil {
		t.Fatal("active organization silently fell back")
	}
	if _, err := m.assistantIdentity("unknown", "", ""); err == nil {
		t.Fatal("unknown subject accepted")
	}
}

func TestAssistantIdentityRejectsAliasConflictAndUnknownStates(t *testing.T) {
	m := assistantFixture()
	alias := m.users["login-a"]
	alias.LoginSubject = "alias-a"
	m.users["alias-a"] = alias
	if _, err := m.assistantIdentity("alias-a", "", ""); err != nil {
		t.Fatal("equivalent alias rejected:", err)
	}
	alias.Memberships = []domain.Membership{{OrganizationID: "org-a", Roles: []string{"organization_admin"}, Status: "正常"}}
	m.users["alias-a"] = alias
	if _, err := m.assistantIdentity("", "owner-a", "org-a"); err == nil {
		t.Fatal("conflicting alias permissions were merged")
	}
	for _, status := range []string{"", "停用", "unknown"} {
		m = assistantFixture()
		user := m.users["login-a"]
		user.Memberships[0].Status = status
		m.users["login-a"] = user
		if _, err := m.assistantIdentity("login-a", "", ""); err == nil {
			t.Fatalf("member status %q accepted", status)
		}
	}
	for _, status := range []string{"", "冻结", "unknown"} {
		m = assistantFixture()
		org := m.organizations["org-a"]
		org.Status = status
		m.organizations["org-a"] = org
		if _, err := m.assistantIdentity("login-a", "", ""); err == nil {
			t.Fatalf("organization status %q accepted", status)
		}
	}
}

func TestAssistantIdentityPersistenceAndUninitializedDenial(t *testing.T) {
	m := assistantFixture()
	raw, err := encodeMemory(m)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeMemory(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restored.assistantIdentity("login-a", "", "")
	if err != nil || got.Revision != "9007199254740993" {
		t.Fatal("revision did not survive serialization")
	}
	restored.identityRevision = 0
	if _, err = restored.assistantIdentity("login-a", "", ""); err == nil {
		t.Fatal("uninitialized revision accepted")
	}
}

func TestAssistantIdentityFingerprintIncludesOrganizationAndMembership(t *testing.T) {
	m := assistantFixture()
	before, err := authorizationFingerprint(m)
	if err != nil {
		t.Fatal(err)
	}
	org := m.organizations["org-a"]
	org.Status = "冻结"
	m.organizations["org-a"] = org
	after, err := authorizationFingerprint(m)
	if err != nil || before == after {
		t.Fatal("organization mutation missed")
	}
	m = assistantFixture()
	user := m.users["login-a"]
	user.Memberships[0].Roles = []string{"organization_admin"}
	m.users["login-a"] = user
	after, err = authorizationFingerprint(m)
	if err != nil || before == after {
		t.Fatal("role mutation missed")
	}
	// Existing public context remains compatible; strict checks are assistant-only.
	if _, err = m.CenterContext(context.Background(), "login-a"); err != nil {
		t.Fatal(err)
	}
}

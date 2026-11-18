package store

import (
	"context"
	"slices"
	"testing"
)

func TestProductionBootstrapHasNoSampleBusinessData(t *testing.T) {
	m := NewMemoryBootstrap()
	ctx := context.Background()
	if len(m.organizations) != 1 || len(m.users) != 1 {
		t.Fatalf("unexpected bootstrap identities: %d organizations, %d users", len(m.organizations), len(m.users))
	}
	for _, user := range m.users {
		for _, membership := range user.Memberships {
			if slices.Contains(membership.Roles, "skill_ops_admin") {
				t.Fatal("production bootstrap must not auto-grant skill operations")
			}
		}
	}
	if len(m.apps) != 0 || len(m.models) != 0 || len(m.tasks) != 0 || len(m.sessions) != 0 || len(m.apiKeys) != 0 || len(m.billing) != 0 || len(m.members) != 0 {
		t.Fatal("bootstrap must not contain sample catalog, tasks, sessions, keys, billing or members")
	}
	if _, err := m.Billing(ctx, "org_verdantflare"); err == nil {
		t.Fatal("missing billing must not look like a zero-value real invoice")
	}
	for _, entry := range m.skillEntries {
		if entry.Visibility != "draft" || !entry.Sample || entry.Distribution != "unavailable" || entry.PublishedReleaseID != nil {
			t.Fatalf("normal bootstrap exposed sample: %+v", entry)
		}
	}
}

func TestSkillCatalogDemoIsIsolatedAndNonDownloadable(t *testing.T) {
	m := NewMemorySkillCatalogDemo()
	if len(m.skillEntries) != 16 {
		t.Fatalf("demo entries=%d, want 16", len(m.skillEntries))
	}
	for _, entry := range m.skillEntries {
		if entry.Visibility != "listed" || !entry.Sample || entry.Distribution != "unavailable" || entry.PublishedReleaseID != nil {
			t.Fatalf("demo entry claims release: %+v", entry)
		}
	}
	for _, user := range m.users {
		for _, membership := range user.Memberships {
			if slices.Contains(membership.Roles, "skill_ops_admin") {
				t.Fatal("demo must not grant skill operations")
			}
		}
	}
}

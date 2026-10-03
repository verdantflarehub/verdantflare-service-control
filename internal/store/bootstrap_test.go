package store

import (
	"context"
	"testing"
)

func TestProductionBootstrapHasNoSampleBusinessData(t *testing.T) {
	m := NewMemoryBootstrap()
	ctx := context.Background()
	if len(m.organizations) != 1 || len(m.users) != 1 {
		t.Fatalf("unexpected bootstrap identities: %d organizations, %d users", len(m.organizations), len(m.users))
	}
	if len(m.apps) != 0 || len(m.models) != 0 || len(m.tasks) != 0 || len(m.sessions) != 0 || len(m.apiKeys) != 0 || len(m.billing) != 0 || len(m.members) != 0 {
		t.Fatal("bootstrap must not contain sample catalog, tasks, sessions, keys, billing or members")
	}
	if _, err := m.Billing(ctx, "org_verdantflare"); err == nil {
		t.Fatal("missing billing must not look like a zero-value real invoice")
	}
}

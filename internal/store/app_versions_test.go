package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func TestAppVersionStoreKeepsSealedSnapshot(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryBootstrap()
	_, err := m.CreateManagedApp(ctx, domain.App{ID: "sealed-demo", Name: "Sealed Demo", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	input := domain.AppVersion{
		AppID: "sealed-demo", Version: "1.0.0", Manifest: json.RawMessage(`{"app_id":"sealed-demo"}`),
		Artifacts: []domain.AppArtifact{{Component: "web", SHA256: "original"}},
	}
	created, err := m.CreateAppVersion(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Manifest[0] = 'x'
	input.Artifacts[0].SHA256 = "changed"
	created.Manifest[0] = 'x'
	created.Artifacts[0].SHA256 = "changed"
	stored, err := m.AppVersion(ctx, "sealed-demo", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if string(stored.Manifest) != `{"app_id":"sealed-demo"}` || stored.Artifacts[0].SHA256 != "original" {
		t.Fatalf("stored version was mutated: %+v", stored)
	}
	if _, err := m.CreateAppVersion(ctx, domain.AppVersion{AppID: "sealed-demo", Version: "1.0.0"}); err == nil {
		t.Fatal("duplicate version replaced sealed snapshot")
	}
}

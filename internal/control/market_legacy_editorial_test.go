package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

func TestLegacyListedAppCanBeEditedWithoutReplacingMissingEditorialFields(t *testing.T) {
	ctx := context.Background()
	repo := store.NewMemoryBootstrap()
	created, err := repo.CreateManagedApp(ctx, domain.App{
		ID: "legacy-editorial", Name: "Legacy App", GroupID: "desktop", Category: "创作",
		Summary: "Existing summary", Description: "Existing description", Visibility: "listed",
		PublicIconURL: "http://legacy.invalid/icon.png", Channel: "Listed",
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(repo)
	name := "Legacy App Updated"
	updated, err := service.PatchManagedAppV2(ctx, "00000000-0000-4000-8000-000000000001", created.App.ID,
		fmt.Sprintf("app:%s:%d", created.App.ID, created.App.Revision), PatchAppV2Input{Name: &name})
	if err != nil || updated.Name != name {
		t.Fatalf("unrelated PATCH was blocked by historical fields: %+v, %v", updated, err)
	}
	badIcon := "http://new.invalid/icon.png"
	if _, err := service.PatchManagedAppV2(ctx, "00000000-0000-4000-8000-000000000001", updated.ID,
		fmt.Sprintf("app:%s:%d", updated.ID, updated.Revision), PatchAppV2Input{PublicIconURL: &badIcon}); err == nil {
		t.Fatal("new invalid icon URL was accepted")
	}
	empty := ""
	if _, err := service.PatchManagedAppV2(ctx, "00000000-0000-4000-8000-000000000001", updated.ID,
		fmt.Sprintf("app:%s:%d", updated.ID, updated.Revision), PatchAppV2Input{Summary: &empty}); err == nil {
		t.Fatal("PATCH removed a previously present listed field")
	}
}

func TestLegacyPublishedAppCanSelectVerifiedVersionWithoutNewEditorialFields(t *testing.T) {
	ctx := context.Background()
	repo := store.NewMemoryBootstrap()
	manifest := []byte(`{"schemaVersion":2}`)
	digest := sha256.Sum256(manifest)
	manifestDigest := "sha256:" + hex.EncodeToString(digest[:])
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = w.Write(manifest)
	}))
	defer registry.Close()
	service := NewService(repo)
	service.SetChartRegistry("registry.verdantflare.ai", registry.URL, registry.Client())
	for _, appID := range []string{"legacy-switch", "first-publish"} {
		app := domain.App{ID: appID, Name: "Legacy App", GroupID: "desktop", Category: "创作", Channel: "Candidate", Visibility: "hidden"}
		if appID == "legacy-switch" {
			app.PublishedVersion, app.Channel, app.Visibility = "1.0.0", "Stable", "listed"
		}
		created, err := repo.CreateManagedApp(ctx, app)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CreateAppVersion(ctx, domain.AppVersion{AppID: appID, Version: "2.0.0", Chart: &domain.AppVersionChart{Version: "2.0.0", ManifestDigest: manifestDigest, PackageDigest: "sha256:" + fmt.Sprintf("%064d", 1)}}); err != nil {
			t.Fatal(err)
		}
		version := "2.0.0"
		selected, err := service.SelectAppReleaseV2(ctx, "00000000-0000-4000-8000-000000000001", appID,
			fmt.Sprintf("app:%s:%d", appID, created.App.Revision), &version)
		if appID == "legacy-switch" {
			if err != nil || selected.PublishedVersion != version {
				t.Fatalf("legacy version switch = %+v, %v", selected, err)
			}
		} else if err == nil {
			t.Fatal("first publication without required editorial fields was accepted")
		}
	}
}

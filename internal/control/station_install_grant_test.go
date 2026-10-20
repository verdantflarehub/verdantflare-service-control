package control

import (
	"context"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

func TestStationInstallGrantRequiresCurrentOrganizationRight(t *testing.T) {
	ctx := context.Background()
	repo := store.NewMemorySeeded(time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC))
	created, err := repo.CreateManagedApp(ctx, domain.App{ID: "test-publish-app", Name: "Test Publish App", GroupID: "desktop", Category: "创作", Summary: "摘要", Description: "介绍", Channel: "Stable", Visibility: "listed", Enabled: true, PublishedVersion: "1.0.0", InstallGrantRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	if !created.App.InstallGrantRequired {
		t.Fatal("installation grant policy lost")
	}
	service := NewService(repo)
	before, err := service.StationInstallGrant(ctx, "test-publish-app", "1.0.0", "org_verdantflare")
	if err != nil || before.Allowed {
		t.Fatalf("ungranted app allowed: %+v, %v", before, err)
	}
	org, err := repo.ManagedOrganization(ctx, "org_verdantflare")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GrantAppToOrganization(ctx, "org_verdantflare", "test-publish-app", org.Organization.EntitlementVersion); err != nil {
		t.Fatal(err)
	}
	after, err := service.StationInstallGrant(ctx, "test-publish-app", "1.0.0", "org_verdantflare")
	if err != nil || !after.Allowed {
		t.Fatalf("granted app denied: %+v, %v", after, err)
	}
	other, err := service.StationInstallGrant(ctx, "test-publish-app", "1.0.0", "org_northshore")
	if err != nil || other.Allowed {
		t.Fatalf("other organization allowed: %+v, %v", other, err)
	}
	if _, err := service.StationInstallGrant(ctx, "test-publish-app", "0.9.0", "org_verdantflare"); err == nil {
		t.Fatal("old release allowed")
	}
}

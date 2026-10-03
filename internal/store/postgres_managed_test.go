package store

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

// CONTROL_TEST_DATABASE_URL must point to a disposable database with the migration applied.
func TestManagedAppSurvivesPostgresReopen(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("CONTROL_TEST_DATABASE_URL is not set")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || !strings.HasSuffix(parsed.Path, "_test") {
		t.Fatal("CONTROL_TEST_DATABASE_URL must name a disposable *_test database")
	}
	ctx := context.Background()
	first, err := OpenPostgres(databaseURL, 2, 1, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("persistence-%d", time.Now().UnixNano())
	_, err = first.CreateManagedApp(ctx, domain.App{ID: id, Name: "Persistence Demo", Version: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = first.UpdateManagedApp(ctx, id, domain.App{Name: "Persistence Demo", Version: "0.1.0", Channel: "Preview"})
	if err != nil {
		t.Fatal(err)
	}
	organization, err := first.ManagedOrganization(ctx, "org_northshore")
	if err != nil {
		t.Fatal(err)
	}
	appIDs := append(organization.AppIDs, id)
	_, err = first.UpdateManagedOrganization(ctx, "org_northshore", domain.Organization{Plan: organization.Organization.Plan, Status: "正常"}, appIDs, organization.Organization.EntitlementVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := OpenPostgres(databaseURL, 2, 1, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	app, err := second.ManagedApp(ctx, id)
	if err != nil || app.App.Channel != "Preview" {
		t.Fatalf("reloaded app = %+v, error = %v", app, err)
	}
	market, err := second.ListApps(ctx, "org_northshore")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range market {
		if item.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatal("published and granted app missing after PostgreSQL reopen")
	}
}

package store

import (
	"context"
	"crypto/sha256"
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
	memberEmail := fmt.Sprintf("member-%d@example.test", time.Now().UnixNano())
	member, err := first.AddMember(ctx, "org_northshore", domain.Member{Email: memberEmail, Name: "Persistence Member", Role: "成员", Status: "待邀请"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.UpdateMember(ctx, "org_northshore", member.ID, "开发者", "已取消", "support"); err != nil {
		t.Fatal(err)
	}
	boundSubject := "00000000-0000-4000-8000-000000000001"
	if _, err := first.UpdateMember(ctx, "org_verdantflare", boundSubject, "开发者", "停用", "support"); err != nil {
		t.Fatal(err)
	}
	keyHash := sha256.Sum256([]byte("postgres-test-only"))
	keyID := fmt.Sprintf("key-persistence-%d", time.Now().UnixNano())
	if _, err := first.CreateAPIKey(ctx, "org_northshore", domain.APIKey{ID: keyID, Name: "Test key", Prefix: "vf_test_••••", SecretHash: keyHash}); err != nil {
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
	members, err := second.ListMembers(ctx, "org_northshore")
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, item := range members {
		found = found || item.ID == member.ID && item.Role == "开发者" && item.Status == "已取消"
	}
	if !found {
		t.Fatal("managed member update missing after PostgreSQL reopen")
	}
	if _, err := second.SetActiveOrganization(ctx, boundSubject, "org_verdantflare"); err == nil {
		t.Fatal("disabled organization membership regained access after PostgreSQL reopen")
	}
	keys, err := second.ListAPIKeys(ctx, "org_northshore")
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, item := range keys {
		found = found || item.ID == keyID && item.SecretHash == keyHash
	}
	if !found {
		t.Fatal("API key verifier missing after PostgreSQL reopen")
	}
}

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

func TestSkillEntrySurvivesLegacyAggregateWrite(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("CONTROL_TEST_DATABASE_URL is not set")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || !strings.HasSuffix(parsed.Path, "_test") {
		t.Fatal("database must be disposable *_test")
	}
	ctx := context.Background()
	repo, err := OpenPostgres(databaseURL, 2, 1, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("skill-test-%d", time.Now().UnixNano())
	created, err := repo.CreateSkillEntry(ctx, domain.SkillCatalogEntry{EntryID: id, Kind: "skill", Name: "Test", Summary: "Test", Description: "Test", Category: "Test", Visibility: "draft", Sample: true}, "test-actor", "org_verdantflare")
	if err != nil {
		t.Fatal(err)
	}
	created.Name, created.Visibility = "Listed test", "listed"
	updated, err := repo.UpdateSkillEntryCAS(ctx, id, 1, created, "test-actor", "org_verdantflare")
	if err != nil || updated.Revision != 2 {
		t.Fatalf("update=%+v err=%v", updated, err)
	}
	if err := repo.mutate(ctx, func(m *Memory) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenPostgres(databaseURL, 2, 1, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	entry, err := reopened.SkillEntry(ctx, id)
	if err != nil || entry.Revision != 2 || entry.Visibility != "listed" || !entry.Sample {
		t.Fatalf("persisted=%+v err=%v", entry, err)
	}
}

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func TestSkillReleaseSurvivesPostgresReopen(t *testing.T) {
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
	id := fmt.Sprintf("0.0.%d-test", time.Now().UnixNano())
	data := []byte("zip bytes")
	digest := sha256.Sum256(data)
	file := domain.SkillReleaseFile{Name: "verdantflare-image.zip", Kind: "package", SkillID: "verdantflare-image", Requires: []string{"verdantflare-project"}, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data)), Data: data}
	release := domain.SkillRelease{ReleaseID: id, Stage: "integration-candidate", Authentication: "unsigned", ManifestSHA256: "m1", ArchiveSHA256: "a1", Files: []domain.SkillReleaseFile{file}}
	created, isNew, err := repo.CreateSkillRelease(ctx, release, "actor", "org_verdantflare")
	if err != nil || !isNew || created.Status != "candidate" || len(created.Files) != 1 {
		t.Fatalf("create=%+v new=%v err=%v", created, isNew, err)
	}
	if _, isNew, err := repo.CreateSkillRelease(ctx, release, "actor", "org_verdantflare"); err != nil || isNew {
		t.Fatalf("idempotent create new=%v err=%v", isNew, err)
	}
	changed := release
	changed.ManifestSHA256 = "m2"
	if _, _, err := repo.CreateSkillRelease(ctx, changed, "actor", "org_verdantflare"); err == nil {
		t.Fatal("different content accepted")
	}
	if _, err := repo.SetSkillReleaseStatus(ctx, id, "published", "actor", "org_verdantflare"); err != nil {
		t.Fatal(err)
	}
	if err := repo.mutate(ctx, func(m *Memory) error { return nil }); err != nil {
		t.Fatal(err)
	}
	repo.Close()
	reopened, err := OpenPostgres(databaseURL, 2, 1, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.SkillReleaseFile(ctx, id, "verdantflare-image.zip")
	if err != nil || string(got.Data) != "zip bytes" || got.Requires[0] != "verdantflare-project" {
		t.Fatalf("file=%+v err=%v", got, err)
	}
	if _, err := reopened.SetSkillReleaseStatus(ctx, id, "published", "actor", "org_verdantflare"); err == nil {
		t.Fatal("double publish accepted")
	}
	withdrawn, err := reopened.SetSkillReleaseStatus(ctx, id, "withdrawn", "actor", "org_verdantflare")
	if err != nil || withdrawn.Status != "withdrawn" || withdrawn.PublishedAt == nil || withdrawn.WithdrawnAt == nil {
		t.Fatalf("withdraw=%+v err=%v", withdrawn, err)
	}
	if err := reopened.RecordSkillReleaseEvent(ctx, domain.SkillReleaseAuditEvent{ReleaseID: id, Action: "download", FileName: "Install.md", ActorSubject: "actor", OrganizationID: "org_verdantflare"}); err != nil {
		t.Fatal(err)
	}
}

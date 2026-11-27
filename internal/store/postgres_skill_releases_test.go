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
	source := &domain.SkillReleaseSource{Repository: "verdantflarehub/verdantflare-skills", Tag: "vf-hub-skill-input-" + id, Commit: strings.Repeat("c", 40),
		GitHubReleaseID: 7, AssetID: 8, Uploader: "github-actions[bot]", Size: 9, SHA256: "a1", FetchedAt: time.Now().UTC().Truncate(time.Second)}
	release := domain.SkillRelease{ReleaseID: id, Stage: "integration-candidate", Authentication: "unsigned", ManifestSHA256: "m1", ArchiveSHA256: "a1", Source: source, Files: []domain.SkillReleaseFile{file}}
	actor := domain.SkillReleaseActor{Subject: "actor", OrganizationID: "org_verdantflare", TokenID: "mcp_1", Tool: "skill_publish.register"}
	created, isNew, err := repo.CreateSkillRelease(ctx, release, actor)
	if err != nil || !isNew || created.Status != "candidate" || len(created.Files) != 1 {
		t.Fatalf("create=%+v new=%v err=%v", created, isNew, err)
	}
	if _, isNew, err := repo.CreateSkillRelease(ctx, release, actor); err != nil || isNew {
		t.Fatalf("idempotent create new=%v err=%v", isNew, err)
	}
	changed := release
	changed.ManifestSHA256 = "m2"
	if _, _, err := repo.CreateSkillRelease(ctx, changed, actor); err == nil {
		t.Fatal("different content accepted")
	}
	if _, err := repo.SetSkillReleaseStatus(ctx, id, "published", "", actor); err != nil {
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
	if _, err := reopened.SetSkillReleaseStatus(ctx, id, "published", "", actor); err == nil {
		t.Fatal("double publish accepted")
	}
	withdrawn, err := reopened.SetSkillReleaseStatus(ctx, id, "withdrawn", "指南有误", actor)
	if err != nil || withdrawn.Status != "withdrawn" || withdrawn.PublishedAt == nil || withdrawn.WithdrawnAt == nil {
		t.Fatalf("withdraw=%+v err=%v", withdrawn, err)
	}
	if withdrawn.Source == nil || *withdrawn.Source != *source {
		t.Fatalf("source receipt did not survive reopen: %+v", withdrawn.Source)
	}
	audit, err := reopened.SkillReleaseAudit(ctx, id, 10)
	if err != nil || len(audit) != 3 || audit[0].Action != "withdraw" || audit[0].Reason != "指南有误" || audit[2].Action != "register" || audit[2].TokenID != "mcp_1" || audit[2].Tool != "skill_publish.register" {
		t.Fatalf("audit=%+v err=%v", audit, err)
	}
	if err := reopened.RecordSkillReleaseEvent(ctx, domain.SkillReleaseAuditEvent{ReleaseID: id, Action: "download", FileName: "Install.md", ActorSubject: "actor", OrganizationID: "org_verdantflare"}); err != nil {
		t.Fatal(err)
	}
}

func TestGrantSkillOperatorOnlyTouchesExistingMembership(t *testing.T) {
	m := NewMemorySeeded(time.Now())
	ctx := context.Background()
	roles, changed, err := m.GrantSkillOperator(ctx, "logto_01vf9k2", "org_northshore")
	if err != nil || !changed || roles[len(roles)-1] != "skill_ops_admin" {
		t.Fatalf("roles=%v changed=%v err=%v", roles, changed, err)
	}
	if _, changed, err := m.GrantSkillOperator(ctx, "logto_01vf9k2", "org_northshore"); err != nil || changed {
		t.Fatalf("repeat changed=%v err=%v", changed, err)
	}
	if _, _, err := m.GrantSkillOperator(ctx, "unknown", "org_northshore"); err == nil {
		t.Fatal("unknown subject granted")
	}
	if _, _, err := m.GrantSkillOperator(ctx, "logto_01vf9k2", "org_missing"); err == nil {
		t.Fatal("missing membership granted")
	}
}

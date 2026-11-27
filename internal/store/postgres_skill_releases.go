package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

const skillReleaseSchemaSQL = `
CREATE TABLE IF NOT EXISTS control_skill_release (
    release_id TEXT PRIMARY KEY,
    stage TEXT NOT NULL,
    authentication TEXT NOT NULL,
    manifest_sha256 TEXT NOT NULL,
    archive_sha256 TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('candidate', 'published', 'withdrawn')),
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ,
    withdrawn_at TIMESTAMPTZ
);
CREATE TABLE IF NOT EXISTS control_skill_release_file (
    release_id TEXT NOT NULL REFERENCES control_skill_release(release_id),
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('guide', 'manifest', 'package')),
    skill_id TEXT NOT NULL DEFAULT '',
    requires JSONB NOT NULL DEFAULT '[]',
    sha256 TEXT NOT NULL,
    size BIGINT NOT NULL CHECK (size > 0),
    data BYTEA NOT NULL,
    position INTEGER NOT NULL,
    PRIMARY KEY (release_id, name)
);
CREATE TABLE IF NOT EXISTS control_skill_release_audit (
    event_id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    release_id TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('upload', 'publish', 'withdraw', 'download')),
    file_name TEXT NOT NULL DEFAULT '',
    actor_subject TEXT NOT NULL,
    organization_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS control_skill_release_audit_release
    ON control_skill_release_audit (release_id, event_id DESC);
` + skillReleaseSourceSchemaSQL

// skillReleaseSourceSchemaSQL upgrades the 014 tables in place for the skill
// publishing MCP (migration 015 holds the same statements): a source receipt
// per release, the token and tool behind each change, and the register action.
const skillReleaseSourceSchemaSQL = `
ALTER TABLE control_skill_release ADD COLUMN IF NOT EXISTS source JSONB;
ALTER TABLE control_skill_release_audit ADD COLUMN IF NOT EXISTS token_id TEXT NOT NULL DEFAULT '';
ALTER TABLE control_skill_release_audit ADD COLUMN IF NOT EXISTS tool TEXT NOT NULL DEFAULT '';
ALTER TABLE control_skill_release_audit ADD COLUMN IF NOT EXISTS reason TEXT NOT NULL DEFAULT '';
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'control_skill_release_audit_action_v2') THEN
        ALTER TABLE control_skill_release_audit DROP CONSTRAINT IF EXISTS control_skill_release_audit_action_check;
        ALTER TABLE control_skill_release_audit ADD CONSTRAINT control_skill_release_audit_action_v2
            CHECK (action IN ('upload', 'register', 'publish', 'withdraw', 'download'));
    END IF;
EXCEPTION WHEN duplicate_object THEN
    NULL; -- another replica upgraded the constraint first
END $$;
`

func ensureSkillReleaseSchema(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, skillReleaseSchemaSQL)
	return err
}

const skillReleaseColumns = `release_id,stage,authentication,manifest_sha256,archive_sha256,status,source,created_by,created_at,published_at,withdrawn_at`

func scanSkillRelease(row skillScanner) (domain.SkillRelease, error) {
	var r domain.SkillRelease
	var source []byte
	err := row.Scan(&r.ReleaseID, &r.Stage, &r.Authentication, &r.ManifestSHA256, &r.ArchiveSHA256, &r.Status, &source, &r.CreatedBy, &r.CreatedAt, &r.PublishedAt, &r.WithdrawnAt)
	if err == nil && len(source) > 0 {
		r.Source = &domain.SkillReleaseSource{}
		if err := json.Unmarshal(source, r.Source); err != nil {
			return r, fmt.Errorf("read skill release source: %w", err)
		}
	}
	return r, err
}

type skillQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadSkillReleaseFiles(ctx context.Context, q skillQuerier, id string) ([]domain.SkillReleaseFile, error) {
	rows, err := q.QueryContext(ctx, `SELECT name,kind,skill_id,requires,sha256,size FROM control_skill_release_file WHERE release_id=$1 ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := make([]domain.SkillReleaseFile, 0)
	for rows.Next() {
		var f domain.SkillReleaseFile
		var requires []byte
		if err := rows.Scan(&f.Name, &f.Kind, &f.SkillID, &requires, &f.SHA256, &f.Size); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(requires, &f.Requires); err != nil {
			return nil, err
		}
		if len(f.Requires) == 0 {
			f.Requires = nil
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

func (p *Postgres) skillRelease(ctx context.Context, q interface {
	skillQuerier
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string, lock bool) (domain.SkillRelease, error) {
	query := `SELECT ` + skillReleaseColumns + ` FROM control_skill_release WHERE release_id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	release, err := scanSkillRelease(q.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SkillRelease{}, releaseNotFound()
	}
	if err != nil {
		return domain.SkillRelease{}, fmt.Errorf("read skill release: %w", err)
	}
	release.Files, err = loadSkillReleaseFiles(ctx, q, id)
	return release, err
}

func (p *Postgres) CreateSkillRelease(ctx context.Context, release domain.SkillRelease, actor domain.SkillReleaseActor) (domain.SkillRelease, bool, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.SkillRelease{}, false, err
	}
	defer tx.Rollback()
	var source any
	if release.Source != nil {
		encoded, err := json.Marshal(release.Source)
		if err != nil {
			return domain.SkillRelease{}, false, err
		}
		source = encoded
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO control_skill_release
		(release_id,stage,authentication,manifest_sha256,archive_sha256,status,source,created_by)
		VALUES ($1,$2,$3,$4,$5,'candidate',$6,$7) ON CONFLICT (release_id) DO NOTHING`,
		release.ReleaseID, release.Stage, release.Authentication, release.ManifestSHA256, release.ArchiveSHA256, source, actor.Subject)
	if err != nil {
		return domain.SkillRelease{}, false, fmt.Errorf("create skill release: %w", err)
	}
	if inserted, _ := result.RowsAffected(); inserted == 0 {
		current, err := p.skillRelease(ctx, tx, release.ReleaseID, false)
		if err != nil {
			return domain.SkillRelease{}, false, err
		}
		if !sameReleaseContent(current, release) {
			return domain.SkillRelease{}, false, releaseConflict()
		}
		return current, false, nil
	}
	for i, f := range release.Files {
		requires, _ := json.Marshal(append([]string{}, f.Requires...))
		if _, err := tx.ExecContext(ctx, `INSERT INTO control_skill_release_file
			(release_id,name,kind,skill_id,requires,sha256,size,data,position) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			release.ReleaseID, f.Name, f.Kind, f.SkillID, requires, f.SHA256, f.Size, f.Data, i); err != nil {
			return domain.SkillRelease{}, false, fmt.Errorf("store skill release file: %w", err)
		}
	}
	if err := insertSkillReleaseAudit(ctx, tx, release.ReleaseID, "register", "", actor); err != nil {
		return domain.SkillRelease{}, false, err
	}
	created, err := p.skillRelease(ctx, tx, release.ReleaseID, false)
	if err != nil {
		return domain.SkillRelease{}, false, err
	}
	return created, true, tx.Commit()
}

func (p *Postgres) ListSkillReleases(ctx context.Context) ([]domain.SkillRelease, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT `+skillReleaseColumns+` FROM control_skill_release ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		return nil, fmt.Errorf("list skill releases: %w", err)
	}
	items := make([]domain.SkillRelease, 0)
	for rows.Next() {
		release, err := scanSkillRelease(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, release)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Files, err = loadSkillReleaseFiles(ctx, p.db, items[i].ReleaseID); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (p *Postgres) GetSkillRelease(ctx context.Context, id string) (domain.SkillRelease, error) {
	return p.skillRelease(ctx, p.db, id, false)
}

type skillExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertSkillReleaseAudit(ctx context.Context, q skillExecer, id, action, reason string, actor domain.SkillReleaseActor) error {
	_, err := q.ExecContext(ctx, `INSERT INTO control_skill_release_audit (release_id,action,actor_subject,organization_id,token_id,tool,reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, id, action, actor.Subject, actor.OrganizationID, actor.TokenID, actor.Tool, reason)
	return err
}

func (p *Postgres) SkillReleaseAudit(ctx context.Context, id string, limit int) ([]domain.SkillReleaseAuditEvent, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT release_id,action,file_name,actor_subject,organization_id,token_id,tool,reason,created_at
		FROM control_skill_release_audit WHERE release_id=$1 AND action <> 'download' ORDER BY event_id DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, fmt.Errorf("list skill release audit: %w", err)
	}
	defer rows.Close()
	events := make([]domain.SkillReleaseAuditEvent, 0)
	for rows.Next() {
		var e domain.SkillReleaseAuditEvent
		if err := rows.Scan(&e.ReleaseID, &e.Action, &e.FileName, &e.ActorSubject, &e.OrganizationID, &e.TokenID, &e.Tool, &e.Reason, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (p *Postgres) SetSkillReleaseStatus(ctx context.Context, id, status, reason string, actor domain.SkillReleaseActor) (domain.SkillRelease, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.SkillRelease{}, err
	}
	defer tx.Rollback()
	current, err := p.skillRelease(ctx, tx, id, true)
	if err != nil {
		return domain.SkillRelease{}, err
	}
	if err := checkReleaseTransition(current.Status, status); err != nil {
		return domain.SkillRelease{}, err
	}
	column := "published_at"
	if status == "withdrawn" {
		column = "withdrawn_at"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE control_skill_release SET status=$2,`+column+`=NOW() WHERE release_id=$1`, id, status); err != nil {
		return domain.SkillRelease{}, err
	}
	if err := insertSkillReleaseAudit(ctx, tx, id, actionFor(status), reason, actor); err != nil {
		return domain.SkillRelease{}, err
	}
	updated, err := p.skillRelease(ctx, tx, id, false)
	if err != nil {
		return domain.SkillRelease{}, err
	}
	return updated, tx.Commit()
}

func (p *Postgres) SkillReleaseFile(ctx context.Context, id, name string) (domain.SkillReleaseFile, error) {
	var f domain.SkillReleaseFile
	var requires []byte
	err := p.db.QueryRowContext(ctx, `SELECT name,kind,skill_id,requires,sha256,size,data FROM control_skill_release_file
		WHERE release_id=$1 AND name=$2`, id, name).Scan(&f.Name, &f.Kind, &f.SkillID, &requires, &f.SHA256, &f.Size, &f.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return f, domain.NewError(404, "skill_release_file_not_found", "发行中没有该文件")
	}
	if err != nil {
		return f, fmt.Errorf("read skill release file: %w", err)
	}
	_ = json.Unmarshal(requires, &f.Requires)
	return f, nil
}

func (p *Postgres) RecordSkillReleaseEvent(ctx context.Context, e domain.SkillReleaseAuditEvent) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO control_skill_release_audit (release_id,action,file_name,actor_subject,organization_id)
		VALUES ($1,$2,$3,$4,$5)`, e.ReleaseID, e.Action, e.FileName, e.ActorSubject, e.OrganizationID)
	return err
}

func (p *Postgres) GrantSkillOperator(ctx context.Context, subject, organizationID string) (roles []string, changed bool, err error) {
	err = p.mutate(ctx, func(m *Memory) error {
		var e error
		roles, changed, e = m.GrantSkillOperator(ctx, subject, organizationID)
		return e
	})
	return
}

func (p *Postgres) SkillOperatorRoles(ctx context.Context, subject, organizationID string) ([]string, error) {
	m, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	return m.SkillOperatorRoles(ctx, subject, organizationID)
}

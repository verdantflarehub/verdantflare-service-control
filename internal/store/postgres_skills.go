package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

const skillEntryColumns = `entry_id,kind,name,summary,description,category,sort_order,visibility,sample,revision,updated_at`

type skillScanner interface {
	Scan(...any) error
}

func scanSkillEntry(row skillScanner) (domain.SkillCatalogEntry, error) {
	var entry domain.SkillCatalogEntry
	err := row.Scan(&entry.EntryID, &entry.Kind, &entry.Name, &entry.Summary, &entry.Description, &entry.Category, &entry.SortOrder, &entry.Visibility, &entry.Sample, &entry.Revision, &entry.UpdatedAt)
	entry.Distribution = "unavailable"
	return withSkillPreview(entry), err
}

func seedPostgresSkillCatalog(ctx context.Context, db *sql.DB) error {
	for _, entry := range defaultSkillEntries() {
		_, err := db.ExecContext(ctx, `INSERT INTO control_skill_catalog_entry
			(entry_id,kind,name,summary,description,category,sort_order,visibility,sample,revision)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,1) ON CONFLICT (entry_id) DO NOTHING`,
			entry.EntryID, entry.Kind, entry.Name, entry.Summary, entry.Description, entry.Category, entry.SortOrder, entry.Visibility, entry.Sample)
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) ListSkillEntries(ctx context.Context) ([]domain.SkillCatalogEntry, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT `+skillEntryColumns+` FROM control_skill_catalog_entry ORDER BY sort_order,entry_id`)
	if err != nil {
		return nil, fmt.Errorf("list skill entries: %w", err)
	}
	defer rows.Close()
	items := make([]domain.SkillCatalogEntry, 0)
	for rows.Next() {
		entry, err := scanSkillEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scan skill entry: %w", err)
		}
		items = append(items, entry)
	}
	return items, rows.Err()
}

func (p *Postgres) SkillEntry(ctx context.Context, id string) (domain.SkillCatalogEntry, error) {
	entry, err := scanSkillEntry(p.db.QueryRowContext(ctx, `SELECT `+skillEntryColumns+` FROM control_skill_catalog_entry WHERE entry_id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SkillCatalogEntry{}, domain.NewError(404, "skill_not_found", "技能目录条目不存在")
	}
	if err != nil {
		return domain.SkillCatalogEntry{}, fmt.Errorf("read skill entry: %w", err)
	}
	return entry, nil
}

func (p *Postgres) CreateSkillEntry(ctx context.Context, entry domain.SkillCatalogEntry, actor, organizationID string) (domain.SkillCatalogEntry, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.SkillCatalogEntry{}, err
	}
	defer tx.Rollback()
	created, err := scanSkillEntry(tx.QueryRowContext(ctx, `INSERT INTO control_skill_catalog_entry
		(entry_id,kind,name,summary,description,category,sort_order,visibility,sample,revision)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,1) ON CONFLICT (entry_id) DO NOTHING RETURNING `+skillEntryColumns,
		entry.EntryID, entry.Kind, entry.Name, entry.Summary, entry.Description, entry.Category, entry.SortOrder, entry.Visibility, entry.Sample))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SkillCatalogEntry{}, domain.NewError(409, "skill_exists", "技能目录条目已存在")
	}
	if err != nil {
		return domain.SkillCatalogEntry{}, fmt.Errorf("create skill entry: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO control_skill_catalog_audit
		(entry_id,action,actor_subject,organization_id,revision) VALUES ($1,'create',$2,$3,$4)`, created.EntryID, actor, organizationID, created.Revision); err != nil {
		return domain.SkillCatalogEntry{}, fmt.Errorf("audit skill entry: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.SkillCatalogEntry{}, err
	}
	return created, nil
}

func (p *Postgres) UpdateSkillEntryCAS(ctx context.Context, id string, revision int, entry domain.SkillCatalogEntry, actor, organizationID string) (domain.SkillCatalogEntry, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.SkillCatalogEntry{}, err
	}
	defer tx.Rollback()
	updated, err := scanSkillEntry(tx.QueryRowContext(ctx, `UPDATE control_skill_catalog_entry SET
		name=$3,summary=$4,description=$5,category=$6,sort_order=$7,visibility=$8,revision=revision+1,updated_at=NOW()
		WHERE entry_id=$1 AND revision=$2 RETURNING `+skillEntryColumns,
		id, revision, entry.Name, entry.Summary, entry.Description, entry.Category, entry.SortOrder, entry.Visibility))
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if queryErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM control_skill_catalog_entry WHERE entry_id=$1)`, id).Scan(&exists); queryErr != nil {
			return domain.SkillCatalogEntry{}, queryErr
		}
		if !exists {
			return domain.SkillCatalogEntry{}, domain.NewError(404, "skill_not_found", "技能目录条目不存在")
		}
		return domain.SkillCatalogEntry{}, domain.NewError(409, "skill_revision_changed", "目录资料已变化，请重新回读")
	}
	if err != nil {
		return domain.SkillCatalogEntry{}, fmt.Errorf("update skill entry: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO control_skill_catalog_audit
		(entry_id,action,actor_subject,organization_id,revision) VALUES ($1,'update',$2,$3,$4)`, id, actor, organizationID, updated.Revision); err != nil {
		return domain.SkillCatalogEntry{}, fmt.Errorf("audit skill entry: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.SkillCatalogEntry{}, err
	}
	return updated, nil
}

func (p *Postgres) ListSkillAudit(ctx context.Context, id string) ([]domain.SkillCatalogAuditEvent, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT entry_id,action,actor_subject,organization_id,revision,created_at
		FROM control_skill_catalog_audit WHERE entry_id=$1 ORDER BY event_id DESC LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.SkillCatalogAuditEvent, 0)
	for rows.Next() {
		var event domain.SkillCatalogAuditEvent
		if err := rows.Scan(&event.EntryID, &event.Action, &event.ActorSubject, &event.OrganizationID, &event.Revision, &event.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, event)
	}
	return items, rows.Err()
}

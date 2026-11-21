package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

const creativeResourceSchemaSQL = `
CREATE TABLE IF NOT EXISTS control_creative_resource (
    resource_id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL,
    owner_subject TEXT NOT NULL,
    title TEXT NOT NULL,
    category TEXT NOT NULL CHECK (category IN ('image', 'video', 'outfit', 'pose', 'motion_transfer')),
    subject_category TEXT NOT NULL DEFAULT 'beauty' CHECK (subject_category IN ('beauty', 'handsome', 'animal')),
    post_kind TEXT NOT NULL DEFAULT 'single' CHECK (post_kind IN ('single', 'compare', 'edit')),
    media_type TEXT NOT NULL DEFAULT 'image' CHECK (media_type IN ('image', 'video')),
    prompt TEXT NOT NULL,
    prompt_origin TEXT NOT NULL DEFAULT 'extracted',
    source_url TEXT NOT NULL DEFAULT '',
    source_author TEXT NOT NULL DEFAULT '',
    engine TEXT NOT NULL DEFAULT '',
    model_name TEXT NOT NULL DEFAULT '',
    aspect_ratio TEXT NOT NULL DEFAULT '',
    resolution TEXT NOT NULL DEFAULT '',
    drive_path TEXT NOT NULL DEFAULT '',
    image_mime TEXT NOT NULL DEFAULT 'image/png',
    image_data BYTEA NOT NULL,
    image_sha256 TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'withdrawn')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS control_creative_resource_item (
    item_id TEXT PRIMARY KEY,
    resource_id TEXT NOT NULL REFERENCES control_creative_resource(resource_id) ON DELETE CASCADE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    step_label TEXT NOT NULL DEFAULT '',
    label TEXT NOT NULL DEFAULT '',
    engine TEXT NOT NULL DEFAULT '',
    model_name TEXT NOT NULL DEFAULT '',
    aspect_ratio TEXT NOT NULL DEFAULT '',
    resolution TEXT NOT NULL DEFAULT '',
    edit_prompt TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    image_mime TEXT NOT NULL DEFAULT 'image/png',
    image_data BYTEA NOT NULL,
    image_sha256 TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS control_creative_resource_org_idx
    ON control_creative_resource (organization_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS control_creative_resource_item_res_idx
    ON control_creative_resource_item (resource_id, sort_order, created_at);
`

func ensureCreativeResourceSchema(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, creativeResourceSchemaSQL)
	return err
}

func creativeResourceImageURL(id string) string {
	return "/api/control/resources/" + id + "/image"
}

func creativeResourceItemImageURL(resourceID, itemID string) string {
	return "/api/control/resources/" + resourceID + "/items/" + itemID + "/image"
}

type CreativeResourceStore interface {
	CreateCreativeResource(context.Context, domain.CreativeResource, []byte) (domain.CreativeResource, error)
	AppendCreativeResourceItem(context.Context, string, string, domain.CreativeResourceItem, []byte) (domain.CreativeResourceItem, error)
	ListCreativeResources(context.Context, string, string, string) ([]domain.CreativeResource, error)
	GetCreativeResource(context.Context, string, string) (domain.CreativeResource, error)
	GetCreativeResourceImage(context.Context, string, string) (domain.CreativeResourceImage, error)
	GetCreativeResourceItemImage(context.Context, string, string, string) (domain.CreativeResourceImage, error)
	WithdrawCreativeResource(context.Context, string, string) (domain.CreativeResource, error)
}

const creativeResourceCols = `resource_id, organization_id, owner_subject, title, category, subject_category, media_type, post_kind, prompt, prompt_origin, source_url, source_author, engine, model_name, aspect_ratio, resolution, drive_path, image_mime, image_sha256, revision, status, created_at, updated_at`

func scanCreativeResource(row interface{ Scan(...any) error }) (domain.CreativeResource, error) {
	var r domain.CreativeResource
	err := row.Scan(
		&r.ResourceID, &r.OrganizationID, &r.OwnerSubject, &r.Title, &r.Category, &r.SubjectCategory,
		&r.MediaType, &r.PostKind, &r.Prompt, &r.PromptOrigin, &r.SourceURL, &r.SourceAuthor,
		&r.Engine, &r.ModelName, &r.AspectRatio, &r.Resolution, &r.DrivePath,
		&r.ImageMime, &r.ImageSHA256, &r.Revision, &r.Status, &r.CreatedAt, &r.UpdatedAt,
	)
	if err == nil {
		r.ImageURL = creativeResourceImageURL(r.ResourceID)
		r.ItemCount = 1
	}
	return r, err
}

func synthesizeBaseItem(r domain.CreativeResource) domain.CreativeResourceItem {
	label := r.ModelName
	if label == "" {
		label = "原图"
	}
	step := ""
	if r.PostKind == "edit" {
		step = "① 母版 v1"
	}
	return domain.CreativeResourceItem{
		ItemID:      "base",
		ResourceID:  r.ResourceID,
		SortOrder:   0,
		StepLabel:   step,
		Label:       label,
		Engine:      r.Engine,
		ModelName:   r.ModelName,
		AspectRatio: r.AspectRatio,
		Resolution:  r.Resolution,
		ImageMime:   r.ImageMime,
		ImageSHA256: r.ImageSHA256,
		ImageURL:    creativeResourceImageURL(r.ResourceID),
		CreatedAt:   r.CreatedAt,
	}
}

func (p *Postgres) loadItems(ctx context.Context, r domain.CreativeResource) ([]domain.CreativeResourceItem, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT item_id, resource_id, sort_order, step_label, label, engine, model_name, aspect_ratio, resolution, edit_prompt, note, image_mime, image_sha256, created_at
		 FROM control_creative_resource_item
		 WHERE resource_id=$1 ORDER BY sort_order ASC, created_at ASC`, r.ResourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.CreativeResourceItem, 0)
	for rows.Next() {
		var it domain.CreativeResourceItem
		if err := rows.Scan(
			&it.ItemID, &it.ResourceID, &it.SortOrder, &it.StepLabel, &it.Label,
			&it.Engine, &it.ModelName, &it.AspectRatio, &it.Resolution,
			&it.EditPrompt, &it.Note, &it.ImageMime, &it.ImageSHA256, &it.CreatedAt,
		); err != nil {
			return nil, err
		}
		it.ImageURL = creativeResourceItemImageURL(it.ResourceID, it.ItemID)
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return []domain.CreativeResourceItem{synthesizeBaseItem(r)}, nil
	}
	return items, nil
}

func (p *Postgres) CreateCreativeResource(ctx context.Context, r domain.CreativeResource, imageData []byte) (domain.CreativeResource, error) {
	if r.ResourceID == "" || r.OrganizationID == "" || r.Title == "" || r.Prompt == "" || len(imageData) == 0 {
		return domain.CreativeResource{}, domain.NewError(400, "invalid_resource", "缺少必要的素材字段或图片内容")
	}
	if r.Category == "" {
		r.Category = "image"
	}
	switch r.SubjectCategory {
	case "beauty", "handsome", "animal":
	case "":
		r.SubjectCategory = "beauty"
	default:
		return domain.CreativeResource{}, domain.NewError(400, "invalid_subject_category", "分类仅支持 beauty(美女) / handsome(帅哥) / animal(动物)")
	}
	if r.MediaType == "" {
		if r.Category == "video" {
			r.MediaType = "video"
		} else {
			r.MediaType = "image"
		}
	}
	if r.PostKind == "" {
		r.PostKind = "single"
	}
	if r.PromptOrigin == "" {
		r.PromptOrigin = "extracted"
	}
	if r.ImageMime == "" {
		r.ImageMime = "image/png"
	}
	sum := sha256.Sum256(imageData)
	r.ImageSHA256 = hex.EncodeToString(sum[:])

	created, err := scanCreativeResource(p.db.QueryRowContext(ctx,
		`INSERT INTO control_creative_resource (
			resource_id, organization_id, owner_subject, title, category, subject_category, media_type, post_kind,
			prompt, prompt_origin, source_url, source_author, engine, model_name, aspect_ratio,
			resolution, drive_path, image_mime, image_data, image_sha256, revision, status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,1,'active')
		RETURNING `+creativeResourceCols,
		r.ResourceID, r.OrganizationID, r.OwnerSubject, r.Title, r.Category, r.SubjectCategory, r.MediaType, r.PostKind,
		r.Prompt, r.PromptOrigin, r.SourceURL, r.SourceAuthor, r.Engine, r.ModelName, r.AspectRatio,
		r.Resolution, r.DrivePath, r.ImageMime, imageData, r.ImageSHA256,
	))
	if err != nil {
		return domain.CreativeResource{}, fmt.Errorf("create creative resource: %w", err)
	}
	created.Items = []domain.CreativeResourceItem{synthesizeBaseItem(created)}
	created.ItemCount = 1
	return created, nil
}

func (p *Postgres) AppendCreativeResourceItem(ctx context.Context, resourceID, upgradePostKind string, item domain.CreativeResourceItem, imageData []byte) (domain.CreativeResourceItem, error) {
	if resourceID == "" || item.ItemID == "" || len(imageData) == 0 {
		return domain.CreativeResourceItem{}, domain.NewError(400, "invalid_item", "缺少媒体项字段或图片内容")
	}
	if item.ImageMime == "" {
		item.ImageMime = "image/png"
	}
	sum := sha256.Sum256(imageData)
	item.ImageSHA256 = hex.EncodeToString(sum[:])

	// Ensure base item exists in child table if appending first child
	var cnt int
	if err := p.db.QueryRowContext(ctx, `SELECT count(*) FROM control_creative_resource_item WHERE resource_id=$1`, resourceID).Scan(&cnt); err != nil {
		return domain.CreativeResourceItem{}, err
	}
	if cnt == 0 {
		_, err := p.db.ExecContext(ctx, `
			INSERT INTO control_creative_resource_item (
				item_id, resource_id, sort_order, step_label, label, engine, model_name,
				aspect_ratio, resolution, edit_prompt, note, image_mime, image_data, image_sha256
			)
			SELECT resource_id || '_base', resource_id, 0,
				CASE WHEN $2 = 'edit' THEN '① 母版 v1' ELSE '' END,
				COALESCE(NULLIF(model_name, ''), '母版 v1'),
				engine, model_name, aspect_ratio, resolution, '', '', image_mime, image_data, image_sha256
			FROM control_creative_resource WHERE resource_id=$1
			ON CONFLICT (item_id) DO NOTHING`, resourceID, upgradePostKind)
		if err != nil {
			return domain.CreativeResourceItem{}, err
		}
		cnt = 1
	}
	if item.SortOrder == 0 {
		item.SortOrder = cnt
	}
	var out domain.CreativeResourceItem
	err := p.db.QueryRowContext(ctx, `
		INSERT INTO control_creative_resource_item (
			item_id, resource_id, sort_order, step_label, label, engine, model_name,
			aspect_ratio, resolution, edit_prompt, note, image_mime, image_data, image_sha256
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING item_id, resource_id, sort_order, step_label, label, engine, model_name, aspect_ratio, resolution, edit_prompt, note, image_mime, image_sha256, created_at`,
		item.ItemID, resourceID, item.SortOrder, item.StepLabel, item.Label, item.Engine, item.ModelName,
		item.AspectRatio, item.Resolution, item.EditPrompt, item.Note, item.ImageMime, imageData, item.ImageSHA256,
	).Scan(
		&out.ItemID, &out.ResourceID, &out.SortOrder, &out.StepLabel, &out.Label,
		&out.Engine, &out.ModelName, &out.AspectRatio, &out.Resolution,
		&out.EditPrompt, &out.Note, &out.ImageMime, &out.ImageSHA256, &out.CreatedAt,
	)
	if err != nil {
		return domain.CreativeResourceItem{}, fmt.Errorf("append creative resource item: %w", err)
	}
	out.ImageURL = creativeResourceItemImageURL(out.ResourceID, out.ItemID)
	if upgradePostKind == "compare" || upgradePostKind == "edit" {
		_, _ = p.db.ExecContext(ctx, `UPDATE control_creative_resource SET post_kind=$2, revision=revision+1, updated_at=NOW() WHERE resource_id=$1`, resourceID, upgradePostKind)
	}
	return out, nil
}

func (p *Postgres) ListCreativeResources(ctx context.Context, orgID, mediaType, subjectCategory string) ([]domain.CreativeResource, error) {
	mt := strings.TrimSpace(mediaType)
	sc := strings.TrimSpace(subjectCategory)
	rows, err := p.db.QueryContext(ctx,
		`SELECT r.resource_id, r.organization_id, r.owner_subject, r.title, r.category, r.subject_category,
		        r.media_type, r.post_kind, r.prompt, r.prompt_origin, r.source_url, r.source_author,
		        r.engine, r.model_name, r.aspect_ratio, r.resolution, r.drive_path,
		        r.image_mime, r.image_sha256, r.revision, r.status, r.created_at, r.updated_at,
		        GREATEST(1, (SELECT count(*) FROM control_creative_resource_item i WHERE i.resource_id = r.resource_id)) AS item_count
		 FROM control_creative_resource r
		 WHERE (r.organization_id=$1 OR r.organization_id='verdantflare-team' OR $1='org_verdantflare')
		   AND r.status='active'
		   AND ($2='' OR $2='all' OR r.media_type=$2)
		   AND ($3='' OR $3='all' OR r.subject_category=$3)
		 ORDER BY r.created_at DESC LIMIT 200`, orgID, mt, sc)
	if err != nil {
		return nil, fmt.Errorf("list creative resources: %w", err)
	}
	defer rows.Close()
	items := make([]domain.CreativeResource, 0)
	for rows.Next() {
		var r domain.CreativeResource
		if err := rows.Scan(
			&r.ResourceID, &r.OrganizationID, &r.OwnerSubject, &r.Title, &r.Category, &r.SubjectCategory,
			&r.MediaType, &r.PostKind, &r.Prompt, &r.PromptOrigin, &r.SourceURL, &r.SourceAuthor,
			&r.Engine, &r.ModelName, &r.AspectRatio, &r.Resolution, &r.DrivePath,
			&r.ImageMime, &r.ImageSHA256, &r.Revision, &r.Status, &r.CreatedAt, &r.UpdatedAt,
			&r.ItemCount,
		); err != nil {
			return nil, fmt.Errorf("scan creative resource: %w", err)
		}
		r.ImageURL = creativeResourceImageURL(r.ResourceID)
		items = append(items, r)
	}
	return items, rows.Err()
}

func (p *Postgres) GetCreativeResource(ctx context.Context, orgID, resourceID string) (domain.CreativeResource, error) {
	item, err := scanCreativeResource(p.db.QueryRowContext(ctx,
		`SELECT `+creativeResourceCols+` FROM control_creative_resource
		 WHERE resource_id=$1 AND (organization_id=$2 OR organization_id='verdantflare-team' OR $2='org_verdantflare')`,
		resourceID, orgID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CreativeResource{}, domain.NewError(404, "resource_not_found", "创意素材不存在")
	}
	if err != nil {
		return domain.CreativeResource{}, fmt.Errorf("get creative resource: %w", err)
	}
	children, err := p.loadItems(ctx, item)
	if err != nil {
		return domain.CreativeResource{}, fmt.Errorf("load creative resource items: %w", err)
	}
	item.Items = children
	item.ItemCount = len(children)
	return item, nil
}

func (p *Postgres) GetCreativeResourceImage(ctx context.Context, orgID, resourceID string) (domain.CreativeResourceImage, error) {
	var img domain.CreativeResourceImage
	err := p.db.QueryRowContext(ctx,
		`SELECT image_mime, image_data, image_sha256 FROM control_creative_resource
		 WHERE resource_id=$1 AND (organization_id=$2 OR organization_id='verdantflare-team' OR $2='org_verdantflare')`,
		resourceID, orgID).Scan(&img.Mime, &img.Data, &img.SHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CreativeResourceImage{}, domain.NewError(404, "resource_not_found", "创意素材图片不存在")
	}
	if err != nil {
		return domain.CreativeResourceImage{}, fmt.Errorf("get creative resource image: %w", err)
	}
	return img, nil
}

func (p *Postgres) GetCreativeResourceItemImage(ctx context.Context, orgID, resourceID, itemID string) (domain.CreativeResourceImage, error) {
	if itemID == "base" {
		return p.GetCreativeResourceImage(ctx, orgID, resourceID)
	}
	var img domain.CreativeResourceImage
	err := p.db.QueryRowContext(ctx,
		`SELECT i.image_mime, i.image_data, i.image_sha256
		 FROM control_creative_resource_item i
		 JOIN control_creative_resource r ON r.resource_id = i.resource_id
		 WHERE i.resource_id=$1 AND i.item_id=$2
		   AND (r.organization_id=$3 OR r.organization_id='verdantflare-team' OR $3='org_verdantflare')`,
		resourceID, itemID, orgID).Scan(&img.Mime, &img.Data, &img.SHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CreativeResourceImage{}, domain.NewError(404, "resource_item_not_found", "素材图片项不存在")
	}
	if err != nil {
		return domain.CreativeResourceImage{}, fmt.Errorf("get creative resource item image: %w", err)
	}
	return img, nil
}

func (p *Postgres) WithdrawCreativeResource(ctx context.Context, orgID, resourceID string) (domain.CreativeResource, error) {
	item, err := scanCreativeResource(p.db.QueryRowContext(ctx,
		`UPDATE control_creative_resource SET status='withdrawn', revision=revision+1, updated_at=NOW()
		 WHERE resource_id=$1 AND (organization_id=$2 OR organization_id='verdantflare-team' OR $2='org_verdantflare')
		 RETURNING `+creativeResourceCols,
		resourceID, orgID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CreativeResource{}, domain.NewError(404, "resource_not_found", "创意素材不存在")
	}
	if err != nil {
		return domain.CreativeResource{}, fmt.Errorf("withdraw creative resource: %w", err)
	}
	return item, nil
}

// Memory fallback for unit tests
func (m *Memory) CreateCreativeResource(_ context.Context, r domain.CreativeResource, _ []byte) (domain.CreativeResource, error) {
	now := time.Now().UTC()
	r.CreatedAt = now
	r.UpdatedAt = now
	r.Revision = 1
	r.Status = "active"
	r.ItemCount = 1
	r.ImageURL = creativeResourceImageURL(r.ResourceID)
	return r, nil
}
func (m *Memory) AppendCreativeResourceItem(_ context.Context, resourceID, _ string, item domain.CreativeResourceItem, _ []byte) (domain.CreativeResourceItem, error) {
	item.ResourceID = resourceID
	item.CreatedAt = time.Now().UTC()
	item.ImageURL = creativeResourceItemImageURL(resourceID, item.ItemID)
	return item, nil
}
func (m *Memory) ListCreativeResources(_ context.Context, _, _, _ string) ([]domain.CreativeResource, error) {
	return []domain.CreativeResource{}, nil
}
func (m *Memory) GetCreativeResource(_ context.Context, _, _ string) (domain.CreativeResource, error) {
	return domain.CreativeResource{}, domain.NewError(404, "resource_not_found", "创意素材不存在")
}
func (m *Memory) GetCreativeResourceImage(_ context.Context, _, _ string) (domain.CreativeResourceImage, error) {
	return domain.CreativeResourceImage{}, domain.NewError(404, "resource_not_found", "创意素材图片不存在")
}
func (m *Memory) GetCreativeResourceItemImage(_ context.Context, _, _, _ string) (domain.CreativeResourceImage, error) {
	return domain.CreativeResourceImage{}, domain.NewError(404, "resource_item_not_found", "素材图片项不存在")
}
func (m *Memory) WithdrawCreativeResource(_ context.Context, _, _ string) (domain.CreativeResource, error) {
	return domain.CreativeResource{}, domain.NewError(404, "resource_not_found", "创意素材不存在")
}

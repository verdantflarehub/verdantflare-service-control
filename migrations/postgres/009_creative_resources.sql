BEGIN;

CREATE TABLE IF NOT EXISTS control_creative_resource (
    resource_id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL,
    owner_subject TEXT NOT NULL,
    title TEXT NOT NULL,
    category TEXT NOT NULL CHECK (category IN ('image', 'video', 'outfit', 'pose', 'motion_transfer')),
    prompt TEXT NOT NULL,
    prompt_origin TEXT NOT NULL DEFAULT 'extracted',
    source_url TEXT NOT NULL DEFAULT '',
    source_author TEXT NOT NULL DEFAULT '',
    model_name TEXT NOT NULL DEFAULT '',
    aspect_ratio TEXT NOT NULL DEFAULT '',
    drive_path TEXT NOT NULL DEFAULT '',
    image_mime TEXT NOT NULL DEFAULT 'image/png',
    image_data BYTEA NOT NULL,
    image_sha256 TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'withdrawn')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS control_creative_resource_org_idx
    ON control_creative_resource (organization_id, status, created_at DESC);

COMMIT;

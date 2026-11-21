BEGIN;

ALTER TABLE control_creative_resource
    ADD COLUMN IF NOT EXISTS post_kind TEXT NOT NULL DEFAULT 'single' CHECK (post_kind IN ('single', 'compare', 'edit')),
    ADD COLUMN IF NOT EXISTS media_type TEXT NOT NULL DEFAULT 'image' CHECK (media_type IN ('image', 'video')),
    ADD COLUMN IF NOT EXISTS resolution TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS engine TEXT NOT NULL DEFAULT '';

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

CREATE INDEX IF NOT EXISTS control_creative_resource_item_res_idx
    ON control_creative_resource_item (resource_id, sort_order, created_at);

DO $$
DECLARE repository_owner TEXT;
BEGIN
    SELECT pg_get_userbyid(relowner) INTO repository_owner
    FROM pg_class WHERE oid = 'public.control_repository_state'::regclass;
    EXECUTE format('ALTER TABLE public.control_creative_resource_item OWNER TO %I', repository_owner);
END $$;

COMMIT;

BEGIN;

ALTER TABLE control_creative_resource
    ADD COLUMN IF NOT EXISTS subject_category TEXT NOT NULL DEFAULT 'beauty'
        CHECK (subject_category IN ('beauty', 'handsome', 'animal'));

CREATE INDEX IF NOT EXISTS control_creative_resource_subject_idx
    ON control_creative_resource (organization_id, media_type, subject_category, status, created_at DESC);

COMMIT;

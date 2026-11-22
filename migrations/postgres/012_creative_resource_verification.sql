BEGIN;

ALTER TABLE control_creative_resource
    ADD COLUMN IF NOT EXISTS verification_status TEXT NOT NULL DEFAULT 'verified'
        CHECK (verification_status IN ('verified', 'unverified')),
    ADD COLUMN IF NOT EXISTS media_origin TEXT NOT NULL DEFAULT 'station_generated'
        CHECK (media_origin IN ('station_generated', 'author_original', 'none')),
    ADD COLUMN IF NOT EXISTS analysis_summary TEXT NOT NULL DEFAULT '';

COMMIT;

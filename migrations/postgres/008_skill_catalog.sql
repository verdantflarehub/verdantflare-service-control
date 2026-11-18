BEGIN;

-- The skill directory is separate from the legacy whole-state JSONB row so
-- older Control replicas cannot erase new entries during a rolling upgrade.
CREATE TABLE IF NOT EXISTS control_skill_catalog_entry (
    entry_id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('skill', 'bundle')),
    name TEXT NOT NULL,
    summary TEXT NOT NULL,
    description TEXT NOT NULL,
    category TEXT NOT NULL,
    sort_order INTEGER NOT NULL CHECK (sort_order BETWEEN 0 AND 1000000),
    visibility TEXT NOT NULL CHECK (visibility IN ('draft', 'listed', 'hidden', 'paused')),
    sample BOOLEAN NOT NULL DEFAULT FALSE,
    revision INTEGER NOT NULL CHECK (revision > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS control_skill_catalog_entry_listing
    ON control_skill_catalog_entry (visibility, sort_order, entry_id);

CREATE TABLE IF NOT EXISTS control_skill_catalog_audit (
    event_id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entry_id TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('create', 'update')),
    actor_subject TEXT NOT NULL,
    organization_id TEXT NOT NULL,
    revision INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS control_skill_catalog_audit_entry
    ON control_skill_catalog_audit (entry_id, event_id DESC);

DO $$
DECLARE repository_owner TEXT;
BEGIN
    SELECT pg_get_userbyid(relowner) INTO repository_owner
    FROM pg_class WHERE oid = 'public.control_repository_state'::regclass;
    EXECUTE format('ALTER TABLE public.control_skill_catalog_entry OWNER TO %I', repository_owner);
    EXECUTE format('ALTER TABLE public.control_skill_catalog_audit OWNER TO %I', repository_owner);
END $$;

COMMIT;

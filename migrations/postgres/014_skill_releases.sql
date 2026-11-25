BEGIN;

-- Fixed skill release bytes live in Control's own database (same approach as
-- creative resource media). Control also creates these at startup.
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

DO $$
DECLARE repository_owner TEXT;
BEGIN
    SELECT pg_get_userbyid(relowner) INTO repository_owner
    FROM pg_class WHERE oid = 'public.control_repository_state'::regclass;
    EXECUTE format('ALTER TABLE public.control_skill_release OWNER TO %I', repository_owner);
    EXECUTE format('ALTER TABLE public.control_skill_release_file OWNER TO %I', repository_owner);
    EXECUTE format('ALTER TABLE public.control_skill_release_audit OWNER TO %I', repository_owner);
END $$;

COMMIT;

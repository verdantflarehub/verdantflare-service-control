BEGIN;

CREATE TABLE IF NOT EXISTS control_model_provider_icons (
    sha256 CHAR(64) PRIMARY KEY,
    content_type TEXT NOT NULL CHECK (content_type IN ('image/png', 'image/jpeg')),
    image_bytes BYTEA NOT NULL CHECK (octet_length(image_bytes) BETWEEN 1 AND 1048576),
    width INTEGER NOT NULL CHECK (width BETWEEN 16 AND 4096),
    height INTEGER NOT NULL CHECK (height BETWEEN 16 AND 4096),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The migration may be executed by a privileged maintenance role. Keep the
-- icon table owned by the same application role as the existing repository.
DO $$
DECLARE
    repository_owner TEXT;
BEGIN
    SELECT pg_get_userbyid(relowner) INTO repository_owner
    FROM pg_class WHERE oid = 'public.control_repository_state'::regclass;
    EXECUTE format('ALTER TABLE public.control_model_provider_icons OWNER TO %I', repository_owner);
END $$;

COMMIT;

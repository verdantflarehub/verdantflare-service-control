BEGIN;

CREATE TABLE IF NOT EXISTS control_publication_nonces (
    digest CHAR(64) PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS control_publication_nonces_expires_at_idx
    ON control_publication_nonces (expires_at);

DO $$
DECLARE
    repository_owner TEXT;
BEGIN
    SELECT pg_get_userbyid(relowner) INTO repository_owner
    FROM pg_class WHERE oid = 'public.control_repository_state'::regclass;
    EXECUTE format('ALTER TABLE public.control_publication_nonces OWNER TO %I', repository_owner);
END $$;

COMMIT;

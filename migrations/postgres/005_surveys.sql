BEGIN;

-- Public event collection is separate from the core organization aggregate.
-- Row locking makes challenge consumption and response submission atomic.
CREATE TABLE IF NOT EXISTS control_survey_state (
    survey_id TEXT PRIMARY KEY,
    state JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO control_survey_state (survey_id) VALUES ('yunnan-202610')
ON CONFLICT (survey_id) DO NOTHING;

DO $$
DECLARE repository_owner TEXT;
BEGIN
    SELECT pg_get_userbyid(relowner) INTO repository_owner
    FROM pg_class WHERE oid = 'public.control_repository_state'::regclass;
    EXECUTE format('ALTER TABLE public.control_survey_state OWNER TO %I', repository_owner);
END $$;

COMMIT;

BEGIN;

-- Additive only. Existing rows remain on storage_version=1 until the new
-- service has been deployed to every replica and a separate cutover is run.
ALTER TABLE control_survey_state
    ADD COLUMN IF NOT EXISTS storage_version SMALLINT NOT NULL DEFAULT 1;

CREATE TABLE IF NOT EXISTS control_survey_entry (
    survey_id TEXT NOT NULL REFERENCES control_survey_state(survey_id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('challenge', 'guest', 'rate', 'response')),
    entry_key TEXT NOT NULL,
    payload JSONB NOT NULL,
    payload_bytes INTEGER NOT NULL CHECK (payload_bytes >= 0),
    PRIMARY KEY (survey_id, kind, entry_key)
);

CREATE TABLE IF NOT EXISTS control_survey_nickname_claim (
    survey_id TEXT NOT NULL REFERENCES control_survey_state(survey_id) ON DELETE CASCADE,
    nickname_key TEXT NOT NULL,
    guest_key TEXT,
    response_id TEXT,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (survey_id, nickname_key)
);

DO $$
DECLARE repository_owner TEXT;
BEGIN
    SELECT pg_get_userbyid(relowner) INTO repository_owner
    FROM pg_class WHERE oid = 'public.control_repository_state'::regclass;
    EXECUTE format('ALTER TABLE public.control_survey_entry OWNER TO %I', repository_owner);
    EXECUTE format('ALTER TABLE public.control_survey_nickname_claim OWNER TO %I', repository_owner);
END $$;

COMMIT;

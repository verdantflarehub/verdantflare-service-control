BEGIN;

-- Skill publishing MCP: releases are registered only from the skills
-- repository's workflow-built GitHub Release, so each release keeps a source
-- receipt; audit rows record the MCP token and tool behind each change and the
-- withdraw reason. Control applies the same statements at startup.
ALTER TABLE control_skill_release ADD COLUMN IF NOT EXISTS source JSONB;
ALTER TABLE control_skill_release_audit ADD COLUMN IF NOT EXISTS token_id TEXT NOT NULL DEFAULT '';
ALTER TABLE control_skill_release_audit ADD COLUMN IF NOT EXISTS tool TEXT NOT NULL DEFAULT '';
ALTER TABLE control_skill_release_audit ADD COLUMN IF NOT EXISTS reason TEXT NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'control_skill_release_audit_action_v2') THEN
        ALTER TABLE control_skill_release_audit DROP CONSTRAINT IF EXISTS control_skill_release_audit_action_check;
        ALTER TABLE control_skill_release_audit ADD CONSTRAINT control_skill_release_audit_action_v2
            CHECK (action IN ('upload', 'register', 'publish', 'withdraw', 'download'));
    END IF;
END $$;

COMMIT;

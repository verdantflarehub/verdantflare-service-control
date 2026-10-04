BEGIN;

-- Preserve the previously approved Flash experience when introducing the
-- per-model switch. Only legacy records without the field are changed;
-- an administrator's explicit later choice is never overwritten.
UPDATE control_repository_state
SET state = jsonb_set(state, '{publicModels,deepseek-flash,experienceMode}', '"chat"'::jsonb),
    updated_at = NOW()
WHERE singleton = TRUE
  AND state #>> '{publicModels,deepseek-flash,publicVisible}' = 'true'
  AND NOT ((state #> '{publicModels,deepseek-flash}') ? 'experienceMode');

COMMIT;

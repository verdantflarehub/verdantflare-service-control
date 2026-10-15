BEGIN;

UPDATE control_survey_state
SET state = jsonb_set(
    state,
    '{metadata}',
    jsonb_build_object(
        'id', 'yunnan-202610',
        'name', '云南交流',
        'description', '需求反馈',
        'createdAt', NOW()
    ),
    true
)
WHERE survey_id = 'yunnan-202610'
  AND NOT state ? 'metadata';

COMMIT;

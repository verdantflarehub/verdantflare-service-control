-- DeepSeek public catalog facts, verified against the official API pricing page
-- on 2026-10-05. This is editorial reference cost, not new-api billing data.
-- Fill only empty fields so an operator's later edits survive a rerun.
UPDATE control_repository_state
SET state = jsonb_set(
  jsonb_set(
    state,
    '{publicModels,deepseek-flash}',
    (state #> '{publicModels,deepseek-flash}') || jsonb_build_object(
      'maxInput', COALESCE(NULLIF(state #>> '{publicModels,deepseek-flash,maxInput}', ''), '输入与输出合计 ≤ 1,048,576 tokens'),
      'priceUnit', COALESCE(NULLIF(state #>> '{publicModels,deepseek-flash,priceUnit}', ''), '美元/百万 tokens · DeepSeek 官方参考成本'),
      'inputPrice', COALESCE(NULLIF(state #>> '{publicModels,deepseek-flash,inputPrice}', ''), '谷时 0.15 / 峰时 0.30'),
      'outputPrice', COALESCE(NULLIF(state #>> '{publicModels,deepseek-flash,outputPrice}', ''), '谷时 0.60 / 峰时 1.20'),
      'cachePrice', COALESCE(NULLIF(state #>> '{publicModels,deepseek-flash,cachePrice}', ''), '谷时 0.003 / 峰时 0.006')
    )
  ),
  '{publicModels,deepseek-v4-pro}',
  (state #> '{publicModels,deepseek-v4-pro}') || jsonb_build_object(
    'maxInput', COALESCE(NULLIF(state #>> '{publicModels,deepseek-v4-pro,maxInput}', ''), '输入与输出合计 ≤ 1,048,576 tokens'),
    'priceUnit', COALESCE(NULLIF(state #>> '{publicModels,deepseek-v4-pro,priceUnit}', ''), '美元/百万 tokens · DeepSeek 官方参考成本'),
    'inputPrice', COALESCE(NULLIF(state #>> '{publicModels,deepseek-v4-pro,inputPrice}', ''), '谷时 0.66 / 峰时 1.32'),
    'outputPrice', COALESCE(NULLIF(state #>> '{publicModels,deepseek-v4-pro,outputPrice}', ''), '谷时 1.98 / 峰时 3.96'),
    'cachePrice', COALESCE(NULLIF(state #>> '{publicModels,deepseek-v4-pro,cachePrice}', ''), '谷时 0.022 / 峰时 0.044')
  )
), updated_at = NOW()
WHERE singleton = TRUE
  AND state #>> '{publicModels,deepseek-flash,id}' = 'deepseek-flash'
  AND state #>> '{publicModels,deepseek-v4-pro,id}' = 'deepseek-v4-pro';

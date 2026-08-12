-- Restore gemini-3-pro-image as its own GA model.
-- Earlier migrations forced pro-image → gemini-3.1-flash-image; keep them separate.

UPDATE accounts
SET credentials = jsonb_set(
    credentials,
    '{model_mapping,gemini-3-pro-image}',
    '"gemini-3-pro-image"',
    true
)
WHERE platform = 'antigravity'
  AND deleted_at IS NULL
  AND credentials ? 'model_mapping'
  AND (
      NOT (credentials->'model_mapping' ? 'gemini-3-pro-image')
      OR credentials->'model_mapping'->>'gemini-3-pro-image' IN (
          'gemini-3.1-flash-image',
          'gemini-3.1-flash-image-preview',
          'gemini-3-pro-image-preview'
      )
  );

UPDATE accounts
SET credentials = jsonb_set(
    credentials,
    '{model_mapping,gemini-3-pro-image-preview}',
    '"gemini-3-pro-image"',
    true
)
WHERE platform = 'antigravity'
  AND deleted_at IS NULL
  AND credentials ? 'model_mapping'
  AND (
      NOT (credentials->'model_mapping' ? 'gemini-3-pro-image-preview')
      OR credentials->'model_mapping'->>'gemini-3-pro-image-preview' IN (
          'gemini-3.1-flash-image',
          'gemini-3.1-flash-image-preview',
          'gemini-3-pro-image-preview'
      )
  );

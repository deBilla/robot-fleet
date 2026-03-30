-- Add inference model assignment to robots for canary deployment support.

ALTER TABLE robots ADD COLUMN IF NOT EXISTS inference_model_id VARCHAR(64);
CREATE INDEX IF NOT EXISTS idx_robots_inference_model ON robots(inference_model_id);

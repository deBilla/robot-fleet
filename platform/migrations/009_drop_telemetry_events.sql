-- Remove unused telemetry_events table.
-- Raw telemetry is stored in S3 (NDJSON batches); hot state lives in Redis.
-- Postgres only needs robot metadata, not per-tick telemetry.

DROP TABLE IF EXISTS telemetry_events;

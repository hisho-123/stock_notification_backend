BEGIN;

CREATE FUNCTION valid_notification_thresholds(input SMALLINT[])
RETURNS BOOLEAN LANGUAGE SQL IMMUTABLE PARALLEL SAFE AS $$
  SELECT COALESCE(
    array_ndims(input) = 1 AND array_lower(input, 1) = 1
    AND cardinality(input) BETWEEN 1 AND 99
    AND NOT EXISTS (SELECT 1 FROM unnest(input) t WHERE t IS NULL OR t < 1 OR t > 99)
    AND input = ARRAY(SELECT DISTINCT t FROM unnest(input) t ORDER BY t),
    FALSE
  );
$$;

CREATE TABLE assets (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  code TEXT NOT NULL UNIQUE,
  name VARCHAR(200) NOT NULL,
  highest_value NUMERIC NOT NULL CHECK (highest_value > 0 AND highest_value < 'Infinity'::numeric),
  latest_value NUMERIC NOT NULL CHECK (latest_value > 0 AND latest_value < 'Infinity'::numeric),
  latest_fetched_at TIMESTAMPTZ NOT NULL,
  monitoring_enabled BOOLEAN NOT NULL DEFAULT TRUE,
  last_notified_threshold SMALLINT CHECK (last_notified_threshold BETWEEN 1 AND 99),
  last_checked_at TIMESTAMPTZ NOT NULL,
  last_error_code VARCHAR(32) CHECK (last_error_code IN ('PRICE_FETCH_FAILED', 'NOTIFICATION_FAILED', 'PRICE_DATA_STALE')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CHECK (char_length(code) BETWEEN 1 AND 64 AND code !~ '[[:space:][:cntrl:]]'
    AND code = translate(code, 'abcdefghijklmnopqrstuvwxyz', 'ABCDEFGHIJKLMNOPQRSTUVWXYZ')),
  CHECK (name !~ '^[[:space:]]*$'),
  CHECK (latest_value <= highest_value)
);

CREATE TABLE notification_settings (
  id SMALLINT PRIMARY KEY CHECK (id = 1),
  thresholds SMALLINT[] NOT NULL CHECK (valid_notification_thresholds(thresholds)),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO notification_settings (id, thresholds) VALUES (1, ARRAY[10,15,20,25,30]::smallint[]);

CREATE TABLE sessions (
  token_hash CHAR(64) PRIMARY KEY CHECK (token_hash ~ '^[0-9a-f]{64}$'),
  csrf_token_hash CHAR(64) NOT NULL CHECK (csrf_token_hash ~ '^[0-9a-f]{64}$'),
  created_at TIMESTAMPTZ NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > created_at)
);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
COMMIT;

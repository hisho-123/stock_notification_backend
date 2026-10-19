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

CREATE TABLE users (
 login_id VARCHAR(100) COLLATE "C" PRIMARY KEY CHECK (char_length(login_id) BETWEEN 1 AND 100),
 password_hash TEXT NOT NULL,
 line_user_id VARCHAR(33) NOT NULL UNIQUE CHECK (line_user_id ~ '^U[0-9a-f]{32}$'),
 line_following BOOLEAN NOT NULL DEFAULT TRUE,
 created_at TIMESTAMPTZ NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE assets (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  login_id VARCHAR(100) COLLATE "C" NOT NULL REFERENCES users(login_id) ON UPDATE RESTRICT ON DELETE RESTRICT,
  code TEXT NOT NULL,
  UNIQUE(login_id, code),
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
  login_id VARCHAR(100) COLLATE "C" PRIMARY KEY REFERENCES users(login_id) ON UPDATE RESTRICT ON DELETE RESTRICT,
  thresholds SMALLINT[] NOT NULL CHECK (valid_notification_thresholds(thresholds)),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);


CREATE TABLE sessions (
  token_hash CHAR(64) PRIMARY KEY CHECK (token_hash ~ '^[0-9a-f]{64}$'),
  login_id VARCHAR(100) COLLATE "C" NOT NULL REFERENCES users(login_id) ON UPDATE RESTRICT ON DELETE RESTRICT,
  csrf_token_hash CHAR(64) NOT NULL CHECK (csrf_token_hash ~ '^[0-9a-f]{64}$'),
  created_at TIMESTAMPTZ NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > created_at)
);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
CREATE INDEX sessions_login_id_idx ON sessions(login_id);
CREATE TABLE registration_tokens (
 token_hash CHAR(64) PRIMARY KEY CHECK (token_hash ~ '^[0-9a-f]{64}$'),
 line_user_id VARCHAR(33) NOT NULL CHECK (line_user_id ~ '^U[0-9a-f]{32}$'),
 created_at TIMESTAMPTZ NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > created_at),
 consumed_at TIMESTAMPTZ,
 revoked_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX registration_tokens_active_line_idx ON registration_tokens(line_user_id) WHERE consumed_at IS NULL AND revoked_at IS NULL;
CREATE INDEX registration_tokens_expires_at_idx ON registration_tokens(expires_at);
CREATE TABLE line_webhook_events (
 webhook_event_id VARCHAR(128) PRIMARY KEY,
 line_user_id VARCHAR(33) NOT NULL CHECK (line_user_id ~ '^U[0-9a-f]{32}$'),
 event_type VARCHAR(16) NOT NULL CHECK (event_type IN ('follow','unfollow','registration')),
 event_timestamp BIGINT NOT NULL CHECK (event_timestamp >= 0),
 reply_status VARCHAR(16) NOT NULL CHECK (reply_status IN ('not_required','pending','sent','failed')),
 processed_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX line_webhook_events_line_timestamp_idx ON line_webhook_events(line_user_id,event_timestamp);
COMMIT;

BEGIN;
DROP TABLE line_webhook_events;
DROP TABLE registration_tokens;
DROP TABLE sessions;
DROP TABLE notification_settings;
DROP TABLE assets;
DROP TABLE users;
DROP FUNCTION valid_notification_thresholds(SMALLINT[]);
COMMIT;

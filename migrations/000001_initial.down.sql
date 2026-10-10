BEGIN;
DROP TABLE sessions;
DROP TABLE notification_settings;
DROP TABLE assets;
DROP FUNCTION valid_notification_thresholds(SMALLINT[]);
COMMIT;

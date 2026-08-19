-- +goose Up
ALTER TABLE release_templates
  ADD COLUMN name VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL DEFAULT '' AFTER code,
  ADD COLUMN scheduling_allowed BOOLEAN NOT NULL DEFAULT FALSE AFTER final_effect,
  ADD COLUMN max_schedule_window_seconds BIGINT UNSIGNED NOT NULL DEFAULT 0 AFTER scheduling_allowed,
  ADD COLUMN allowed_roles JSON NULL AFTER max_schedule_window_seconds;

ALTER TABLE release_templates
  ADD CONSTRAINT chk_template_schedule_window CHECK (
    (scheduling_allowed = TRUE AND max_schedule_window_seconds > 0) OR
    (scheduling_allowed = FALSE AND max_schedule_window_seconds = 0)
  ),
  ADD CONSTRAINT chk_template_allowed_roles CHECK (JSON_TYPE(allowed_roles) = 'ARRAY');

-- +goose Down
ALTER TABLE release_templates
  DROP CHECK chk_template_allowed_roles,
  DROP CHECK chk_template_schedule_window,
  DROP COLUMN allowed_roles,
  DROP COLUMN max_schedule_window_seconds,
  DROP COLUMN scheduling_allowed,
  DROP COLUMN name;

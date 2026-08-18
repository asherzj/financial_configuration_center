-- +goose Up
CREATE TABLE configuration_revision_counters (
  counter_name VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  current_revision BIGINT UNSIGNED NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (counter_name),
  CONSTRAINT chk_revision_counter_positive CHECK (current_revision >= 0)
) ENGINE=InnoDB;

INSERT INTO configuration_revision_counters (counter_name, current_revision, updated_at)
VALUES ('global', 0, UTC_TIMESTAMP(6));

CREATE TABLE configuration_collections (
  name VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  description VARCHAR(1024) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL DEFAULT '',
  fields JSON NOT NULL,
  key_fields JSON NOT NULL,
  sdk_delivery_enabled BOOLEAN NOT NULL,
  schema_version BIGINT UNSIGNED NOT NULL,
  status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  config_revision BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(6) NOT NULL,
  created_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  PRIMARY KEY (name),
  CONSTRAINT chk_collection_fields_array CHECK (JSON_TYPE(fields) = 'ARRAY'),
  CONSTRAINT chk_collection_key_fields_array CHECK (JSON_TYPE(key_fields) = 'ARRAY'),
  CONSTRAINT chk_collection_schema_version CHECK (schema_version > 0),
  CONSTRAINT chk_collection_revision CHECK (config_revision > 0),
  CONSTRAINT chk_collection_status CHECK (status IN ('ENABLED', 'DISABLED'))
) ENGINE=InnoDB;

CREATE TABLE configuration_records (
  collection_name VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  environment VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  record_key VARCHAR(1024) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  data JSON NOT NULL,
  config_revision BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(6) NOT NULL,
  created_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  PRIMARY KEY (collection_name, environment, record_key),
  KEY idx_records_collection_env_revision (collection_name, environment, config_revision),
  CONSTRAINT fk_records_collection FOREIGN KEY (collection_name) REFERENCES configuration_collections(name) ON DELETE RESTRICT,
  CONSTRAINT chk_record_environment CHECK (environment <> ''),
  CONSTRAINT chk_record_data_object CHECK (JSON_TYPE(data) = 'OBJECT'),
  CONSTRAINT chk_record_revision CHECK (config_revision > 0)
) ENGINE=InnoDB;

CREATE TABLE configuration_subscriptions (
  id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  consumer_id VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  collection_name VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  index_name VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  index_fields JSON NOT NULL,
  cardinality VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  enabled BOOLEAN NOT NULL,
  config_revision BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(6) NOT NULL,
  created_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_subscription_identity (consumer_id, collection_name, index_name),
  KEY idx_subscriptions_consumer_enabled (consumer_id, enabled, collection_name),
  KEY idx_subscriptions_collection_enabled (collection_name, enabled, consumer_id),
  CONSTRAINT fk_subscriptions_collection FOREIGN KEY (collection_name) REFERENCES configuration_collections(name) ON DELETE RESTRICT,
  CONSTRAINT chk_subscription_index_fields CHECK (JSON_TYPE(index_fields) = 'ARRAY'),
  CONSTRAINT chk_subscription_cardinality CHECK (cardinality IN ('ONE_TO_ONE', 'ONE_TO_MANY')),
  CONSTRAINT chk_subscription_revision CHECK (config_revision > 0)
) ENGINE=InnoDB;

CREATE TABLE configuration_models (
  code VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  name VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  collection_name VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  definition JSON NOT NULL,
  enabled BOOLEAN NOT NULL,
  config_revision BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(6) NOT NULL,
  created_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  PRIMARY KEY (code),
  KEY idx_models_collection_enabled (collection_name, enabled, code),
  CONSTRAINT fk_models_collection FOREIGN KEY (collection_name) REFERENCES configuration_collections(name) ON DELETE RESTRICT,
  CONSTRAINT chk_model_definition_object CHECK (JSON_TYPE(definition) = 'OBJECT'),
  CONSTRAINT chk_model_revision CHECK (config_revision > 0)
) ENGINE=InnoDB;

CREATE TABLE release_templates (
  code VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  version BIGINT UNSIGNED NOT NULL,
  model_code VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  release_type_code VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  active_slot CHAR(1) CHARACTER SET ascii COLLATE ascii_bin NULL,
  final_effect VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  template JSON NOT NULL,
  created_at DATETIME(6) NOT NULL,
  created_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  PRIMARY KEY (code, version),
  UNIQUE KEY uq_release_template_active (model_code, release_type_code, active_slot),
  CONSTRAINT fk_templates_model FOREIGN KEY (model_code) REFERENCES configuration_models(code) ON DELETE RESTRICT,
  CONSTRAINT chk_template_version CHECK (version > 0),
  CONSTRAINT chk_template_active_slot CHECK (active_slot IS NULL OR active_slot = 'A'),
  CONSTRAINT chk_template_final_effect CHECK (final_effect IN ('BASE_FINAL', 'OVERLAY_FINAL')),
  CONSTRAINT chk_template_json CHECK (JSON_TYPE(template) = 'OBJECT')
) ENGINE=InnoDB;

CREATE TABLE release_orders (
  id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  release_number VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  idempotency_key VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  request_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  model_code VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  template_code VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  template_version BIGINT UNSIGNED NOT NULL,
  release_type_code VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  region VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  environment VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  stage VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  status VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  current_step_code VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  template_snapshot JSON NOT NULL,
  description VARCHAR(2048) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  authorized_roles JSON NOT NULL,
  batch_type VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  compensates_order_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NULL,
  entity_revision BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(6) NOT NULL,
  created_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  completed_at DATETIME(6) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_release_number (release_number),
  UNIQUE KEY uq_release_create_idempotency (created_by, idempotency_key),
  KEY idx_release_list (updated_at, id),
  KEY idx_release_scope (model_code, environment, status, updated_at),
  CONSTRAINT fk_orders_model FOREIGN KEY (model_code) REFERENCES configuration_models(code) ON DELETE RESTRICT,
  CONSTRAINT fk_orders_template FOREIGN KEY (template_code, template_version) REFERENCES release_templates(code, version) ON DELETE RESTRICT,
  CONSTRAINT fk_orders_compensation FOREIGN KEY (compensates_order_id) REFERENCES release_orders(id) ON DELETE RESTRICT,
  CONSTRAINT chk_order_status CHECK (status IN ('IN_PROGRESS', 'SUCCEEDED', 'ROLLED_BACK', 'REJECTED', 'FAILED')),
  CONSTRAINT chk_order_completion CHECK ((status = 'IN_PROGRESS' AND completed_at IS NULL) OR (status <> 'IN_PROGRESS' AND completed_at IS NOT NULL)),
  CONSTRAINT chk_order_template_snapshot CHECK (JSON_TYPE(template_snapshot) = 'OBJECT'),
  CONSTRAINT chk_order_authorized_roles CHECK (JSON_TYPE(authorized_roles) = 'ARRAY'),
  CONSTRAINT chk_order_batch_type CHECK (batch_type IN ('SINGLE', 'BATCH')),
  CONSTRAINT chk_order_entity_revision CHECK (entity_revision > 0)
) ENGINE=InnoDB;

CREATE TABLE release_order_items (
  id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  release_order_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  position INT UNSIGNED NOT NULL,
  action VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  collection_name VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  record_key VARCHAR(1024) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  target VARCHAR(1024) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  target_description VARCHAR(1024) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  base_before JSON NULL,
  effective_before JSON NULL,
  after_data JSON NULL,
  expected_record_revision BIGINT UNSIGNED NOT NULL,
  expected_collection_revision BIGINT UNSIGNED NOT NULL,
  preserve_sensitive_fields JSON NOT NULL,
  status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  active_conflict_key CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  entity_revision BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(6) NOT NULL,
  created_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_release_item_position (release_order_id, position),
  UNIQUE KEY uq_release_item_record (release_order_id, collection_name, record_key),
  UNIQUE KEY uq_release_item_active_conflict (active_conflict_key),
  CONSTRAINT fk_items_order FOREIGN KEY (release_order_id) REFERENCES release_orders(id) ON DELETE RESTRICT,
  CONSTRAINT fk_items_collection FOREIGN KEY (collection_name) REFERENCES configuration_collections(name) ON DELETE RESTRICT,
  CONSTRAINT chk_item_action CHECK (action IN ('ADD', 'MODIFY', 'DELETE')),
  CONSTRAINT chk_item_status CHECK (status IN ('PENDING', 'APPLIED', 'ROLLED_BACK', 'FAILED')),
  CONSTRAINT chk_item_before_after CHECK (
    (action = 'ADD' AND base_before IS NULL AND effective_before IS NULL AND after_data IS NOT NULL AND expected_record_revision = 0) OR
    (action = 'MODIFY' AND effective_before IS NOT NULL AND after_data IS NOT NULL AND expected_record_revision > 0) OR
    (action = 'DELETE' AND effective_before IS NOT NULL AND after_data IS NULL AND expected_record_revision > 0)
  ),
  CONSTRAINT chk_item_base_before CHECK (base_before IS NULL OR JSON_TYPE(base_before) = 'OBJECT'),
  CONSTRAINT chk_item_effective_before CHECK (effective_before IS NULL OR JSON_TYPE(effective_before) = 'OBJECT'),
  CONSTRAINT chk_item_after CHECK (after_data IS NULL OR JSON_TYPE(after_data) = 'OBJECT'),
  CONSTRAINT chk_item_sensitive_fields CHECK (JSON_TYPE(preserve_sensitive_fields) = 'ARRAY'),
  CONSTRAINT chk_item_collection_revision CHECK (expected_collection_revision > 0),
  CONSTRAINT chk_item_entity_revision CHECK (entity_revision > 0)
) ENGINE=InnoDB;

CREATE TABLE release_step_states (
  release_order_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  step_code VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  step_type VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  sequence_no INT UNSIGNED NOT NULL,
  status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  context JSON NOT NULL,
  approval JSON NULL,
  effect JSON NULL,
  compare_result JSON NULL,
  execute_count INT UNSIGNED NOT NULL DEFAULT 0,
  executed_at DATETIME(6) NULL,
  executed_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NULL,
  rolled_back_at DATETIME(6) NULL,
  rolled_back_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NULL,
  error_code VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
  error_message VARCHAR(1024) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NULL,
  entity_revision BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(6) NOT NULL,
  created_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  PRIMARY KEY (release_order_id, step_code),
  UNIQUE KEY uq_release_step_sequence (release_order_id, sequence_no),
  CONSTRAINT fk_steps_order FOREIGN KEY (release_order_id) REFERENCES release_orders(id) ON DELETE RESTRICT,
  CONSTRAINT chk_step_context CHECK (JSON_TYPE(context) = 'OBJECT'),
  CONSTRAINT chk_step_status CHECK (status IN ('PENDING', 'EXECUTING', 'EXECUTED', 'APPROVED', 'REJECTED', 'ROLLED_BACK', 'FAILED')),
  CONSTRAINT chk_step_entity_revision CHECK (entity_revision > 0)
) ENGINE=InnoDB;

CREATE TABLE release_action_requests (
  release_order_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  action_request_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  request_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  result_projection JSON NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (release_order_id, action_request_id),
  CONSTRAINT fk_action_requests_order FOREIGN KEY (release_order_id) REFERENCES release_orders(id) ON DELETE RESTRICT,
  CONSTRAINT chk_action_result_object CHECK (JSON_TYPE(result_projection) = 'OBJECT')
) ENGINE=InnoDB;

CREATE TABLE release_operation_logs (
  id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  release_order_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  release_item_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NULL,
  step_code VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NULL,
  action VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  result VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  actor_subject VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  actor_name VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  message VARCHAR(1024) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  error_code VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
  error_detail VARCHAR(1024) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NULL,
  trace_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_operation_order_time (release_order_id, created_at, id),
  CONSTRAINT fk_operation_order FOREIGN KEY (release_order_id) REFERENCES release_orders(id) ON DELETE RESTRICT,
  CONSTRAINT fk_operation_item FOREIGN KEY (release_item_id) REFERENCES release_order_items(id) ON DELETE RESTRICT,
  CONSTRAINT chk_operation_result CHECK (result IN ('SUCCEEDED', 'FAILED'))
) ENGINE=InnoDB;

CREATE TABLE configuration_overlays (
  id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  collection_name VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  region VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  environment VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  stage VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  record_key VARCHAR(1024) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  action VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  content JSON NULL,
  rollout_ranges JSON NOT NULL,
  config_revision BIGINT UNSIGNED NOT NULL,
  release_order_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  effective_from DATETIME(6) NULL,
  effective_until DATETIME(6) NULL,
  activated_revision BIGINT UNSIGNED NULL,
  activated_at DATETIME(6) NULL,
  expired_revision BIGINT UNSIGNED NULL,
  expired_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  created_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_overlay_scope_record (collection_name, region, environment, stage, record_key),
  KEY idx_overlay_environment_revision (collection_name, environment, config_revision),
  KEY idx_overlay_boundaries (activated_at, expired_at, effective_from, effective_until),
  CONSTRAINT fk_overlays_collection FOREIGN KEY (collection_name) REFERENCES configuration_collections(name) ON DELETE RESTRICT,
  CONSTRAINT fk_overlays_order FOREIGN KEY (release_order_id) REFERENCES release_orders(id) ON DELETE RESTRICT,
  CONSTRAINT chk_overlay_action CHECK (action IN ('ADD', 'MODIFY', 'DELETE')),
  CONSTRAINT chk_overlay_content CHECK ((action = 'DELETE' AND content IS NULL) OR (action <> 'DELETE' AND JSON_TYPE(content) = 'OBJECT')),
  CONSTRAINT chk_overlay_ranges CHECK (JSON_TYPE(rollout_ranges) = 'ARRAY'),
  CONSTRAINT chk_overlay_window CHECK (effective_until IS NULL OR effective_from IS NULL OR effective_until > effective_from),
  CONSTRAINT chk_overlay_activated_pair CHECK ((activated_revision IS NULL) = (activated_at IS NULL)),
  CONSTRAINT chk_overlay_expired_pair CHECK ((expired_revision IS NULL) = (expired_at IS NULL)),
  CONSTRAINT chk_overlay_expiry_order CHECK (expired_at IS NULL OR activated_at IS NULL OR expired_at >= activated_at),
  CONSTRAINT chk_overlay_revision CHECK (config_revision > 0)
) ENGINE=InnoDB;

CREATE TABLE configuration_versions (
  collection_name VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  environment VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  config_revision BIGINT UNSIGNED NOT NULL,
  base_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  overlay_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  release_order_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (collection_name, environment),
  KEY idx_versions_revision (config_revision, collection_name, environment),
  CONSTRAINT fk_versions_collection FOREIGN KEY (collection_name) REFERENCES configuration_collections(name) ON DELETE RESTRICT,
  CONSTRAINT fk_versions_order FOREIGN KEY (release_order_id) REFERENCES release_orders(id) ON DELETE RESTRICT,
  CONSTRAINT chk_version_revision CHECK (config_revision > 0)
) ENGINE=InnoDB;

CREATE TABLE configuration_change_log (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  collection_name VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  kind VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  region VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  environment VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  stage VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  record_key VARCHAR(1024) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  action VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  before_data JSON NULL,
  after_data JSON NULL,
  config_revision BIGINT UNSIGNED NOT NULL,
  release_order_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_change_collection_cursor (collection_name, id),
  KEY idx_change_release_cursor (release_order_id, id),
  KEY idx_change_revision (config_revision, id),
  CONSTRAINT fk_change_collection FOREIGN KEY (collection_name) REFERENCES configuration_collections(name) ON DELETE RESTRICT,
  CONSTRAINT fk_change_order FOREIGN KEY (release_order_id) REFERENCES release_orders(id) ON DELETE RESTRICT,
  CONSTRAINT chk_change_kind CHECK (kind IN ('BASE_RECORD', 'OVERLAY', 'METADATA')),
  CONSTRAINT chk_change_action CHECK (action IN ('ADD', 'MODIFY', 'DELETE')),
  CONSTRAINT chk_change_revision CHECK (config_revision > 0)
) ENGINE=InnoDB;

CREATE TABLE outbox_events (
  id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  sequence_no BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  aggregate_type VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  aggregate_id VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  event_type VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  payload_version INT UNSIGNED NOT NULL,
  payload JSON NOT NULL,
  idempotency_key VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  status VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  lease_revision BIGINT UNSIGNED NOT NULL,
  attempts INT UNSIGNED NOT NULL DEFAULT 0,
  next_attempt_at DATETIME(6) NOT NULL,
  locked_by VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NULL,
  locked_until DATETIME(6) NULL,
  last_error VARCHAR(1024) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  published_at DATETIME(6) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_outbox_sequence (sequence_no),
  UNIQUE KEY uq_outbox_idempotency (idempotency_key),
  KEY idx_outbox_relay (status, next_attempt_at, sequence_no),
  CONSTRAINT chk_outbox_payload CHECK (JSON_TYPE(payload) = 'OBJECT'),
  CONSTRAINT chk_outbox_payload_version CHECK (payload_version > 0),
  CONSTRAINT chk_outbox_status CHECK (status IN ('PENDING', 'PROCESSING', 'SENT', 'DEAD_LETTER')),
  CONSTRAINT chk_outbox_lease_revision CHECK (lease_revision > 0)
) ENGINE=InnoDB;

CREATE TABLE audit_records (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  occurred_at DATETIME(6) NOT NULL,
  principal_subject VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  principal_display_name VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL,
  action VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  resource_type VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  resource_id VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  region VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  environment VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  stage VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  result VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  before_data JSON NULL,
  after_data JSON NULL,
  metadata JSON NOT NULL,
  request_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  trace_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  PRIMARY KEY (id),
  KEY idx_audit_resource_time (resource_type, resource_id, occurred_at, id),
  KEY idx_audit_principal_time (principal_subject, occurred_at, id),
  KEY idx_audit_time (occurred_at, id),
  CONSTRAINT chk_audit_result CHECK (result IN ('SUCCEEDED', 'FAILED')),
  CONSTRAINT chk_audit_metadata CHECK (JSON_TYPE(metadata) = 'OBJECT')
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE audit_records;
DROP TABLE outbox_events;
DROP TABLE configuration_change_log;
DROP TABLE configuration_versions;
DROP TABLE configuration_overlays;
DROP TABLE release_operation_logs;
DROP TABLE release_action_requests;
DROP TABLE release_step_states;
DROP TABLE release_order_items;
DROP TABLE release_orders;
DROP TABLE release_templates;
DROP TABLE configuration_models;
DROP TABLE configuration_subscriptions;
DROP TABLE configuration_records;
DROP TABLE configuration_collections;
DROP TABLE configuration_revision_counters;

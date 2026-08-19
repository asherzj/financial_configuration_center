package mysqlsource_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"

	"github.com/asherzj/financial_configuration_center/internal/distribution/mysqlsource"
	readmodel "github.com/asherzj/financial_configuration_center/internal/distribution/readmodel"
	platformmysql "github.com/asherzj/financial_configuration_center/internal/platform/mysql"
)

func TestLoadEnvironmentCursorIncludesGlobalMetadata(t *testing.T) {
	dsn := isolatedDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	raw, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	fields, err := json.Marshal([]readmodel.FieldDefinition{{
		Name: "code", DisplayName: "Code", Type: readmodel.FieldTypeString, Required: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := json.Marshal([]string{"code"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `
		INSERT INTO configuration_collections (
			name, description, fields, key_fields, sdk_delivery_enabled, schema_version,
			status, config_revision, created_at, created_by, updated_at, updated_by
		) VALUES ('routes', '', ?, ?, TRUE, 1, 'ENABLED', 9, UTC_TIMESTAMP(6), 'test', UTC_TIMESTAMP(6), 'test')
	`, fields, keys); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("0", 64)
	if _, err := raw.ExecContext(ctx, `
		INSERT INTO configuration_versions (
			collection_name, environment, config_revision, base_digest, overlay_digest, updated_at
		) VALUES ('routes', 'production', 9, ?, ?, UTC_TIMESTAMP(6))
	`, digest, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `
		INSERT INTO configuration_subscriptions (
			id, consumer_id, collection_name, index_name, index_fields, cardinality,
			enabled, config_revision,
			created_at, created_by, updated_at, updated_by
		) VALUES (
			'00000000-0000-0000-0000-000000000001', 'payments-api', 'routes', 'by_code', JSON_ARRAY('code'), 'ONE_TO_ONE',
			TRUE, 9, UTC_TIMESTAMP(6), 'test', UTC_TIMESTAMP(6), 'test'
		), (
			'00000000-0000-0000-0000-000000000002', 'payments-api', 'routes', 'by_code_many', JSON_ARRAY('code'), 'ONE_TO_MANY',
			TRUE, 9, UTC_TIMESTAMP(6), 'test', UTC_TIMESTAMP(6), 'test'
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `
		INSERT INTO configuration_change_log (
			collection_name, kind, region, environment, stage, record_key, action,
			after_data, config_revision, created_at
		) VALUES ('routes', 'BASE_RECORD', 'cn', 'production', '', 'key', 'MODIFY', JSON_OBJECT(), 7, UTC_TIMESTAMP(6))
	`); err != nil {
		t.Fatal(err)
	}
	metadata, err := raw.ExecContext(ctx, `
		INSERT INTO configuration_change_log (
			collection_name, kind, region, environment, stage, record_key, action,
			after_data, config_revision, created_at
		) VALUES ('routes', 'METADATA', '', '', '', '', 'MODIFY', JSON_OBJECT(), 9, UTC_TIMESTAMP(6))
	`)
	if err != nil {
		t.Fatal(err)
	}
	metadataCursor, err := metadata.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}

	database, err := platformmysql.Open(ctx, platformmysql.Config{
		DSN: dsn, MaxOpenConns: 2, MaxIdleConns: 1,
		ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	source, err := mysqlsource.New(database)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := source.LoadEnvironmentPartial(ctx, "production")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Failures) != 0 || len(loaded.Inputs) != 1 {
		t.Fatalf("environment load = %+v", loaded)
	}
	if loaded.Inputs[0].Cursor != uint64(metadataCursor) {
		t.Fatalf("cursor = %d, want global metadata cursor %d", loaded.Inputs[0].Cursor, metadataCursor)
	}
	if len(loaded.Inputs[0].SubscribedConsumers) != 1 || loaded.Inputs[0].SubscribedConsumers[0] != "payments-api" {
		t.Fatalf("snapshot consumers = %v", loaded.Inputs[0].SubscribedConsumers)
	}
}

func isolatedDatabase(t *testing.T) string {
	t.Helper()
	base := os.Getenv("FINCONFIG_TEST_MYSQL_DSN")
	if base == "" {
		t.Skip("FINCONFIG_TEST_MYSQL_DSN is not set")
	}
	config, err := drivermysql.ParseDSN(base)
	if err != nil {
		t.Fatal(err)
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	databaseName := "finconfig_snapshot_" + hex.EncodeToString(random)
	adminConfig := config.Clone()
	adminConfig.DBName = ""
	admin, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs"); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec("DROP DATABASE `" + databaseName + "`")
		_ = admin.Close()
	})
	config.DBName = databaseName
	dsn := config.FormatDSN()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createSourceSchema(t, db)
	return dsn
}

func createSourceSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []string{
		`CREATE TABLE configuration_collections (
			name VARCHAR(128) PRIMARY KEY, description TEXT NOT NULL, fields JSON NOT NULL,
			key_fields JSON NOT NULL, sdk_delivery_enabled BOOLEAN NOT NULL,
			schema_version BIGINT UNSIGNED NOT NULL, status VARCHAR(32) NOT NULL,
			config_revision BIGINT UNSIGNED NOT NULL, created_at DATETIME(6) NOT NULL,
			created_by VARCHAR(128) NOT NULL, updated_at DATETIME(6) NOT NULL,
			updated_by VARCHAR(128) NOT NULL
		) ENGINE=InnoDB`,
		`CREATE TABLE configuration_versions (
			collection_name VARCHAR(128) NOT NULL, environment VARCHAR(64) NOT NULL,
			config_revision BIGINT UNSIGNED NOT NULL, base_digest CHAR(64) NOT NULL,
			overlay_digest CHAR(64) NOT NULL, updated_at DATETIME(6) NOT NULL,
			PRIMARY KEY (collection_name, environment)
		) ENGINE=InnoDB`,
		`CREATE TABLE configuration_subscriptions (
			id CHAR(36) PRIMARY KEY, consumer_id VARCHAR(128) NOT NULL,
			collection_name VARCHAR(128) NOT NULL, index_name VARCHAR(128) NOT NULL,
			index_fields JSON NOT NULL, cardinality VARCHAR(32) NOT NULL,
			enabled BOOLEAN NOT NULL, config_revision BIGINT UNSIGNED NOT NULL,
			created_at DATETIME(6) NOT NULL, created_by VARCHAR(128) NOT NULL,
			updated_at DATETIME(6) NOT NULL, updated_by VARCHAR(128) NOT NULL
		) ENGINE=InnoDB`,
		`CREATE TABLE configuration_change_log (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
			collection_name VARCHAR(128) NOT NULL, kind VARCHAR(32) NOT NULL,
			region VARCHAR(64) NOT NULL, environment VARCHAR(64) NOT NULL,
			stage VARCHAR(64) NOT NULL, record_key VARCHAR(512) NOT NULL,
			action VARCHAR(32) NOT NULL, after_data JSON NULL,
			config_revision BIGINT UNSIGNED NOT NULL, created_at DATETIME(6) NOT NULL
		) ENGINE=InnoDB`,
		`CREATE TABLE configuration_models (
			code VARCHAR(128) PRIMARY KEY, name VARCHAR(255) NOT NULL,
			collection_name VARCHAR(128) NOT NULL, definition JSON NOT NULL,
			enabled BOOLEAN NOT NULL, config_revision BIGINT UNSIGNED NOT NULL
		) ENGINE=InnoDB`,
		`CREATE TABLE release_templates (
			code VARCHAR(128) NOT NULL, model_code VARCHAR(128) NOT NULL,
			release_type_code VARCHAR(128) NOT NULL, active_slot CHAR(1) NULL
		) ENGINE=InnoDB`,
		`CREATE TABLE configuration_records (
			collection_name VARCHAR(128) NOT NULL, environment VARCHAR(64) NOT NULL,
			record_key VARCHAR(512) NOT NULL, data JSON NOT NULL,
			config_revision BIGINT UNSIGNED NOT NULL,
			PRIMARY KEY (collection_name, environment, record_key)
		) ENGINE=InnoDB`,
		`CREATE TABLE configuration_overlays (
			id CHAR(36) PRIMARY KEY, collection_name VARCHAR(128) NOT NULL,
			region VARCHAR(64) NOT NULL, environment VARCHAR(64) NOT NULL,
			stage VARCHAR(64) NOT NULL, record_key VARCHAR(512) NOT NULL,
			action VARCHAR(32) NOT NULL, content JSON NULL, rollout_ranges JSON NOT NULL,
			config_revision BIGINT UNSIGNED NOT NULL, release_order_id CHAR(36) NOT NULL,
			effective_from DATETIME(6) NULL, effective_until DATETIME(6) NULL,
			activated_revision BIGINT UNSIGNED NULL, activated_at DATETIME(6) NULL,
			expired_revision BIGINT UNSIGNED NULL, expired_at DATETIME(6) NULL,
			created_at DATETIME(6) NOT NULL, created_by VARCHAR(128) NOT NULL,
			updated_at DATETIME(6) NOT NULL, updated_by VARCHAR(128) NOT NULL
		) ENGINE=InnoDB`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

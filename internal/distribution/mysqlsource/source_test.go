package mysqlsource_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/mysqlsource"
	platformmysql "github.com/asherzj/financial_configuration_center/internal/platform/mysql"
	"github.com/asherzj/financial_configuration_center/internal/platform/mysql/migrations"
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
	fields, err := json.Marshal([]catalog.FieldDefinition{{
		Name: "code", DisplayName: "Code", Type: catalog.FieldTypeString, Required: true,
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
	if err := migrations.Up(context.Background(), db, migrationDirectory(t)); err != nil {
		t.Fatal(err)
	}
	return dsn
}

func migrationDirectory(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate migration test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../db/migrations/mysql"))
}

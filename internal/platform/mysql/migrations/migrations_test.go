package migrations_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/pressly/goose/v3"

	platformmysql "github.com/asherzj/financial_configuration_center/internal/platform/mysql"
	"github.com/asherzj/financial_configuration_center/internal/platform/mysql/migrations"
)

func TestExpectedVersionsMatchMigrationFiles(t *testing.T) {
	t.Parallel()
	collected, err := goose.CollectMigrations(migrationDirectory(t), 0, goose.MaxVersion)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]int64, len(collected))
	for index, migration := range collected {
		got[index] = migration.Version
	}
	if want := migrations.ExpectedVersions(); !slices.Equal(got, want) {
		t.Fatalf("migration files = %v, expected manifest = %v", got, want)
	}
}

func TestGooseMigrationUpDownUp(t *testing.T) {
	dsn := os.Getenv("FINCONFIG_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("FINCONFIG_TEST_MYSQL_DSN is not set")
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}

	directory := migrationDirectory(t)
	if err := migrations.Up(ctx, db, directory); err != nil {
		t.Fatal(err)
	}
	checked, err := platformmysql.Open(ctx, platformmysql.Config{
		DSN: dsn, MaxOpenConns: 2, MaxIdleConns: 1,
		ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = checked.Close() })
	if err := checked.CheckStartup(ctx); err != nil {
		t.Fatal(err)
	}
	assertBusinessTableCount(t, ctx, db, 16)
	assertExactBusinessTables(t, ctx, db)
	assertEnvironmentScopedRecordKey(t, ctx, db)
	assertRevisionColumns(t, ctx, db)
	assertSchemaRejectsInvalidRows(t, ctx, db)

	if err := migrations.DownOne(ctx, db, directory); err != nil {
		t.Fatal(err)
	}
	assertBusinessTableCount(t, ctx, db, 16)
	if err := migrations.DownOne(ctx, db, directory); err != nil {
		t.Fatal(err)
	}
	assertBusinessTableCount(t, ctx, db, 0)

	if err := migrations.Up(ctx, db, directory); err != nil {
		t.Fatal(err)
	}
	assertBusinessTableCount(t, ctx, db, 16)
}

func assertExactBusinessTables(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	want := migrations.ExpectedTables()
	rows, err := db.QueryContext(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = DATABASE()
		  AND table_name <> 'goose_db_version'
		ORDER BY table_name
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("business tables = %v, want %v", got, want)
	}
}

func migrationDirectory(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate migration test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../../db/migrations/mysql"))
}

func assertBusinessTableCount(t *testing.T, ctx context.Context, db *sql.DB, want int) {
	t.Helper()
	var got int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM information_schema.tables
		WHERE table_schema = DATABASE()
		  AND table_name <> 'goose_db_version'
	`).Scan(&got)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("business table count = %d, want %d", got, want)
	}
}

func assertEnvironmentScopedRecordKey(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT column_name
		FROM information_schema.key_column_usage
		WHERE table_schema = DATABASE()
		  AND table_name = 'configuration_records'
		  AND constraint_name = 'PRIMARY'
		ORDER BY ordinal_position
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"collection_name", "environment", "record_key"}
	if len(columns) != len(want) {
		t.Fatalf("configuration_records PK = %v, want %v", columns, want)
	}
	for index := range want {
		if columns[index] != want[index] {
			t.Fatalf("configuration_records PK = %v, want %v", columns, want)
		}
	}
}

func assertRevisionColumns(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	assertColumn := func(table, column, dataType, columnType, nullable string) {
		t.Helper()
		var gotDataType, gotColumnType, gotNullable string
		err := db.QueryRowContext(ctx, `
			SELECT data_type, column_type, is_nullable
			FROM information_schema.columns
			WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?
		`, table, column).Scan(&gotDataType, &gotColumnType, &gotNullable)
		if err != nil {
			t.Fatalf("read %s.%s: %v", table, column, err)
		}
		if gotDataType != dataType || gotColumnType != columnType || gotNullable != nullable {
			t.Fatalf("%s.%s = (%s,%s,%s), want (%s,%s,%s)", table, column,
				gotDataType, gotColumnType, gotNullable, dataType, columnType, nullable)
		}
	}
	assertColumn("configuration_records", "config_revision", "bigint", "bigint unsigned", "NO")
	assertColumn("release_orders", "entity_revision", "bigint", "bigint unsigned", "NO")
	assertColumn("outbox_events", "lease_revision", "bigint", "bigint unsigned", "NO")
	assertColumn("release_templates", "max_schedule_window_seconds", "bigint", "bigint unsigned", "NO")
}

func assertSchemaRejectsInvalidRows(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	insertCollection := `
		INSERT INTO configuration_collections (
			name, description, fields, key_fields, sdk_delivery_enabled,
			schema_version, status, config_revision, created_at, created_by, updated_at, updated_by
		) VALUES (?, '', ?, JSON_ARRAY('code'), TRUE, 1, 'ENABLED', 1, UTC_TIMESTAMP(6), 'test', UTC_TIMESTAMP(6), 'test')
	`
	if _, err := db.ExecContext(ctx, insertCollection, "invalid_fields", `{}`); err == nil {
		t.Fatal("collection with object fields passed array CHECK")
	}
	if _, err := db.ExecContext(ctx, insertCollection, "routes", `[{"name":"code"}]`); err != nil {
		t.Fatalf("insert valid collection fixture: %v", err)
	}
	insertRecord := `
		INSERT INTO configuration_records (
			collection_name, environment, record_key, data, config_revision,
			created_at, created_by, updated_at, updated_by
		) VALUES ('routes', ?, ?, ?, 1, UTC_TIMESTAMP(6), 'test', UTC_TIMESTAMP(6), 'test')
	`
	if _, err := db.ExecContext(ctx, insertRecord, "production", "array-data", `[]`); err == nil {
		t.Fatal("record with array data passed object CHECK")
	}
	if _, err := db.ExecContext(ctx, insertRecord, "", "empty-environment", `{}`); err == nil {
		t.Fatal("record with empty environment passed CHECK")
	}
}

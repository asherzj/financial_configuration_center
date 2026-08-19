package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/pressly/goose/v3"
)

var gooseDialectMu sync.Mutex

var expectedVersions = [...]int64{1, 2}

var expectedTables = [...]string{
	"audit_records",
	"configuration_change_log",
	"configuration_collections",
	"configuration_models",
	"configuration_overlays",
	"configuration_records",
	"configuration_revision_counters",
	"configuration_subscriptions",
	"configuration_versions",
	"outbox_events",
	"release_action_requests",
	"release_operation_logs",
	"release_order_items",
	"release_orders",
	"release_step_states",
	"release_templates",
}

// ExpectedVersions is the build-owned schema manifest. Services validate this
// exact applied set at startup; they never run migrations themselves.
func ExpectedVersions() []int64 {
	return append([]int64(nil), expectedVersions[:]...)
}

// ExpectedTables returns the exact FinConfig business-table manifest. The
// Goose bookkeeping table is intentionally not part of the domain schema.
func ExpectedTables() []string {
	return append([]string(nil), expectedTables[:]...)
}

func Up(ctx context.Context, db *sql.DB, directory string) error {
	return withMySQLDialect(func() error {
		if err := goose.UpContext(ctx, db, directory); err != nil {
			return fmt.Errorf("apply Goose migrations: %w", err)
		}
		return nil
	})
}

func DownOne(ctx context.Context, db *sql.DB, directory string) error {
	return withMySQLDialect(func() error {
		if err := goose.DownContext(ctx, db, directory); err != nil {
			return fmt.Errorf("roll back one Goose migration: %w", err)
		}
		return nil
	})
}

func Status(ctx context.Context, db *sql.DB, directory string) error {
	return withMySQLDialect(func() error {
		if err := goose.StatusContext(ctx, db, directory); err != nil {
			return fmt.Errorf("read Goose migration status: %w", err)
		}
		return nil
	})
}

func withMySQLDialect(run func() error) error {
	gooseDialectMu.Lock()
	defer gooseDialectMu.Unlock()
	if err := goose.SetDialect("mysql"); err != nil {
		return fmt.Errorf("configure Goose MySQL dialect: %w", err)
	}
	return run()
}

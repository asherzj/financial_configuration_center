package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/pressly/goose/v3"
)

var gooseDialectMu sync.Mutex

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

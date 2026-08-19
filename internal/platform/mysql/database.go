package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type Config struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

func (c Config) Validate() error {
	if c.DSN == "" {
		return errors.New("MySQL DSN is required")
	}
	parsed, err := drivermysql.ParseDSN(c.DSN)
	if err != nil {
		return errors.New("MySQL DSN is invalid")
	}
	separator := strings.LastIndexByte(c.DSN, '?')
	if separator < 0 {
		return errors.New("MySQL DSN must explicitly configure parseTime and UTC location")
	}
	parameters, err := url.ParseQuery(c.DSN[separator+1:])
	if err != nil || len(parameters["parseTime"]) != 1 || parameters.Get("parseTime") != "true" || len(parameters["loc"]) != 1 || parameters.Get("loc") != "UTC" || !parsed.ParseTime || parsed.Loc.String() != "UTC" {
		return errors.New("MySQL DSN must explicitly set parseTime=true and loc=UTC")
	}
	if parsed.Timeout <= 0 || parsed.ReadTimeout <= 0 || parsed.WriteTimeout <= 0 {
		return errors.New("MySQL DSN must set positive connect, read, and write timeouts")
	}
	if c.MaxOpenConns <= 0 {
		return errors.New("MySQL max open connections must be positive")
	}
	if c.MaxIdleConns < 0 || c.MaxIdleConns > c.MaxOpenConns {
		return errors.New("MySQL max idle connections must be between zero and max open connections")
	}
	if c.ConnMaxLifetime <= 0 {
		return errors.New("MySQL connection max lifetime must be positive")
	}
	if c.ConnMaxIdleTime <= 0 {
		return errors.New("MySQL connection max idle time must be positive")
	}
	return nil
}

type Database struct {
	gorm *gorm.DB
	sql  *sql.DB
}

func Open(ctx context.Context, config Config, options ...gorm.Option) (*Database, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	db, err := gorm.Open(gormmysql.Open(config.DSN), append([]gorm.Option{
		&gorm.Config{
			DisableForeignKeyConstraintWhenMigrating: true,
			SkipDefaultTransaction:                   true,
		},
	}, options...)...)
	if err != nil {
		return nil, fmt.Errorf("open MySQL through GORM: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get MySQL connection pool: %w", err)
	}
	sqlDB.SetMaxOpenConns(config.MaxOpenConns)
	sqlDB.SetMaxIdleConns(config.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(config.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(config.ConnMaxIdleTime)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping MySQL: %w", err)
	}
	return &Database{gorm: db, sql: sqlDB}, nil
}

func (d *Database) Close() error {
	if d == nil || d.sql == nil {
		return nil
	}
	return d.sql.Close()
}

func (d *Database) Ping(ctx context.Context) error {
	if d == nil || d.sql == nil {
		return errors.New("MySQL database is not initialized")
	}
	if err := d.sql.PingContext(ctx); err != nil {
		return fmt.Errorf("ping MySQL: %w", err)
	}
	return nil
}

// WithinTransaction is intentionally adapter-internal: application packages own
// their narrow transaction ports and their MySQL implementations compose those
// ports on top of this primitive without exposing *gorm.DB across the boundary.
func (d *Database) WithinTransaction(
	ctx context.Context,
	options *sql.TxOptions,
	run func(tx *gorm.DB) error,
) error {
	if d == nil || d.gorm == nil {
		return errors.New("MySQL database is not initialized")
	}
	if run == nil {
		return errors.New("MySQL transaction callback is required")
	}
	if err := d.gorm.WithContext(ctx).Transaction(run, options); err != nil {
		return fmt.Errorf("MySQL transaction: %w", err)
	}
	return nil
}

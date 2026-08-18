package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/asherzj/financial_configuration_center/internal/platform/mysql/migrations"
)

func main() {
	command := flag.String("command", "status", "one of: up, down-one, status")
	directory := flag.String("dir", "db/migrations/mysql", "Goose SQL migration directory")
	flag.Parse()

	dsn := os.Getenv("FINCONFIG_MYSQL_DSN")
	if dsn == "" {
		fatal(errors.New("FINCONFIG_MYSQL_DSN is required"))
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		fatal(fmt.Errorf("connect to MySQL: %w", err))
	}

	switch *command {
	case "up":
		err = migrations.Up(ctx, db, *directory)
	case "down-one":
		err = migrations.DownOne(ctx, db, *directory)
	case "status":
		err = migrations.Status(ctx, db, *directory)
	default:
		err = fmt.Errorf("unsupported command %q", *command)
	}
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

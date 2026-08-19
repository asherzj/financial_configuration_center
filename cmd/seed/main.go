package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/catalog/application"
	"github.com/asherzj/financial_configuration_center/internal/catalog/mysqlstore"
	platformmysql "github.com/asherzj/financial_configuration_center/internal/platform/mysql"
	"github.com/asherzj/financial_configuration_center/internal/seed"
)

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

func main() {
	dsn := os.Getenv("FINCONFIG_MYSQL_DSN")
	if dsn == "" {
		fatal(errors.New("FINCONFIG_MYSQL_DSN is required"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	database, err := platformmysql.Open(ctx, platformmysql.Config{DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 2, ConnMaxLifetime: 5 * time.Minute, ConnMaxIdleTime: time.Minute})
	if err != nil {
		fatal(err)
	}
	defer database.Close()
	repository, err := mysqlstore.New(database)
	if err != nil {
		fatal(err)
	}
	service, err := application.NewService(repository, systemClock{})
	if err != nil {
		fatal(err)
	}
	if err := seed.CatalogDemo(ctx, service); err != nil {
		fatal(err)
	}
	fmt.Println("FinConfig demo catalog is ready")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

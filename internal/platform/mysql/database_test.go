package mysql_test

import (
	"testing"
	"time"

	platformmysql "github.com/asherzj/financial_configuration_center/internal/platform/mysql"
)

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	valid := platformmysql.Config{
		DSN:             "user:password@tcp(mysql:3306)/finconfig?parseTime=true&loc=UTC",
		MaxOpenConns:    20,
		MaxIdleConns:    5,
		ConnMaxLifetime: 30 * time.Minute,
		ConnMaxIdleTime: 5 * time.Minute,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}

	tests := map[string]func(*platformmysql.Config){
		"missing DSN":        func(config *platformmysql.Config) { config.DSN = "" },
		"zero max open":      func(config *platformmysql.Config) { config.MaxOpenConns = 0 },
		"negative max idle":  func(config *platformmysql.Config) { config.MaxIdleConns = -1 },
		"idle exceeds open":  func(config *platformmysql.Config) { config.MaxIdleConns = 21 },
		"zero max lifetime":  func(config *platformmysql.Config) { config.ConnMaxLifetime = 0 },
		"zero max idle time": func(config *platformmysql.Config) { config.ConnMaxIdleTime = 0 },
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			config := valid
			mutate(&config)
			if err := config.Validate(); err == nil {
				t.Fatal("Validate() succeeded, want error")
			}
		})
	}
}

func TestNilDatabaseRejectsOperations(t *testing.T) {
	t.Parallel()

	var database *platformmysql.Database
	if err := database.Close(); err != nil {
		t.Fatalf("nil Close() = %v", err)
	}
}

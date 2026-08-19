package mysql

import (
	"strings"
	"testing"

	"github.com/asherzj/financial_configuration_center/internal/platform/mysql/migrations"
)

func TestValidateStartupFactsAcceptsSupportedMySQLAndExactSchema(t *testing.T) {
	t.Parallel()
	facts := validStartupFacts()
	if err := validateStartupFacts(facts, []int64{1, 2}, migrations.ExpectedTables()); err != nil {
		t.Fatalf("supported startup facts: %v", err)
	}
	facts.version = "8.0.46-commercial"
	if err := validateStartupFacts(facts, []int64{1, 2}, migrations.ExpectedTables()); err != nil {
		t.Fatalf("compatibility MySQL startup facts: %v", err)
	}

	facts = validStartupFacts()
	facts.migrations = []migrationState{
		{version: 2, applied: true}, {version: 2, applied: false}, {version: 1, applied: true},
		{version: 3, applied: false},
	}
	if err := validateStartupFacts(facts, []int64{1, 2}, migrations.ExpectedTables()); err != nil {
		t.Fatalf("latest applied and rolled-back unknown migration: %v", err)
	}
}

func TestValidateStartupFactsFailsClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		mutate   func(*startupFacts)
		versions []int64
		tables   []string
		want     string
	}{
		{name: "unsupported patch", mutate: func(f *startupFacts) { f.version = "8.4.10" }, want: "unsupported MySQL version"},
		{name: "MariaDB", mutate: func(f *startupFacts) { f.version = "8.4.11-MariaDB" }, want: "unsupported MySQL version"},
		{name: "engine", mutate: func(f *startupFacts) { f.defaultStorageEngine = "MyISAM" }, want: "InnoDB"},
		{name: "missing table", mutate: func(f *startupFacts) { delete(f.tables, "release_orders") }, want: "is missing"},
		{name: "table engine", mutate: func(f *startupFacts) {
			f.tables["release_orders"] = tableState{tableType: "BASE TABLE", engine: "MyISAM"}
		}, want: "InnoDB base table"},
		{name: "view replacement", mutate: func(f *startupFacts) { f.tables["release_orders"] = tableState{tableType: "VIEW"} }, want: "InnoDB base table"},
		{name: "named local time", mutate: func(f *startupFacts) { f.sessionTimeZone = "SYSTEM" }, want: "UTC"},
		{name: "offset", mutate: func(f *startupFacts) { f.utcOffsetSeconds = 3600 }, want: "UTC"},
		{name: "non strict", mutate: func(f *startupFacts) { f.sqlMode = "NO_ENGINE_SUBSTITUTION" }, want: "strict SQL mode"},
		{name: "allow invalid dates", mutate: func(f *startupFacts) { f.sqlMode = "STRICT_TRANS_TABLES,ALLOW_INVALID_DATES" }, want: "ALLOW_INVALID_DATES"},
		{name: "missing migration", mutate: func(f *startupFacts) { f.migrations = []migrationState{{version: 1, applied: true}} }, want: "schema migration 2"},
		{name: "rolled back migration", mutate: func(f *startupFacts) {
			f.migrations = []migrationState{{version: 2, applied: false}, {version: 2, applied: true}, {version: 1, applied: true}}
		}, want: "schema migration 2"},
		{name: "future migration", mutate: func(f *startupFacts) {
			f.migrations = append([]migrationState{{version: 3, applied: true}}, f.migrations...)
		}, want: "unexpected schema migration 3"},
		{name: "duplicate migration manifest", versions: []int64{1, 1}, want: "migration manifest"},
		{name: "unsorted table manifest", tables: []string{"release_orders", "audit_records"}, want: "table manifest"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			facts := validStartupFacts()
			if test.mutate != nil {
				test.mutate(&facts)
			}
			versions := test.versions
			if versions == nil {
				versions = []int64{1, 2}
			}
			tables := test.tables
			if tables == nil {
				tables = migrations.ExpectedTables()
			}
			err := validateStartupFacts(facts, versions, tables)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("startup error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestNilDatabaseRejectsStartupCheck(t *testing.T) {
	t.Parallel()
	var database *Database
	if err := database.CheckStartup(t.Context()); err == nil {
		t.Fatal("nil database startup check succeeded")
	}
}

func validStartupFacts() startupFacts {
	return startupFacts{
		version: "8.4.11", defaultStorageEngine: "InnoDB", sessionTimeZone: "+00:00",
		utcOffsetSeconds: 0, sqlMode: "STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION",
		tables: validTableFacts(),
		migrations: []migrationState{
			{version: 2, applied: true}, {version: 1, applied: true}, {version: 0, applied: true},
		},
	}
}

func validTableFacts() map[string]tableState {
	result := make(map[string]tableState)
	for _, name := range migrations.ExpectedTables() {
		result[name] = tableState{tableType: "BASE TABLE", engine: "InnoDB"}
	}
	return result
}

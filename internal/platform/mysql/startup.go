package mysql

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/asherzj/financial_configuration_center/internal/platform/mysql/migrations"
)

var mysqlVersionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$`)

type migrationState struct {
	version int64
	applied bool
}

type tableState struct {
	tableType string
	engine    string
}

type startupFacts struct {
	version              string
	defaultStorageEngine string
	sessionTimeZone      string
	utcOffsetSeconds     int64
	sqlMode              string
	tables               map[string]tableState
	migrations           []migrationState
}

// CheckStartup performs the read-only database compatibility and schema gate.
// Its build-owned manifests cannot be replaced by runtime configuration.
func (d *Database) CheckStartup(ctx context.Context) error {
	if d == nil || d.sql == nil {
		return errors.New("check MySQL startup: database is not initialized")
	}
	expectedVersions := migrations.ExpectedVersions()
	expectedTables := migrations.ExpectedTables()
	if err := validateManifests(expectedVersions, expectedTables); err != nil {
		return fmt.Errorf("check MySQL startup: %w", err)
	}

	facts := startupFacts{}
	if err := d.sql.QueryRowContext(ctx, `
		SELECT VERSION(), @@SESSION.default_storage_engine, @@SESSION.time_zone,
			TIMESTAMPDIFF(SECOND, UTC_TIMESTAMP(), NOW()), @@SESSION.sql_mode
	`).Scan(
		&facts.version, &facts.defaultStorageEngine, &facts.sessionTimeZone,
		&facts.utcOffsetSeconds, &facts.sqlMode,
	); err != nil {
		return fmt.Errorf("check MySQL startup capabilities: %w", err)
	}

	tableRows, err := d.sql.QueryContext(ctx, `
		SELECT table_name, table_type, COALESCE(engine, '')
		FROM information_schema.tables
		WHERE table_schema = DATABASE()
	`)
	if err != nil {
		return fmt.Errorf("check MySQL startup tables: %w", err)
	}
	facts.tables = make(map[string]tableState)
	for tableRows.Next() {
		var name string
		var state tableState
		if err := tableRows.Scan(&name, &state.tableType, &state.engine); err != nil {
			_ = tableRows.Close()
			return fmt.Errorf("check MySQL startup table row: %w", err)
		}
		facts.tables[name] = state
	}
	if err := tableRows.Err(); err != nil {
		_ = tableRows.Close()
		return fmt.Errorf("check MySQL startup tables: %w", err)
	}
	if err := tableRows.Close(); err != nil {
		return fmt.Errorf("check MySQL startup tables: %w", err)
	}

	rows, err := d.sql.QueryContext(ctx, `
		SELECT version_id, is_applied
		FROM goose_db_version
		ORDER BY id DESC
	`)
	if err != nil {
		return fmt.Errorf("check MySQL startup schema history: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var state migrationState
		if err := rows.Scan(&state.version, &state.applied); err != nil {
			return fmt.Errorf("check MySQL startup schema row: %w", err)
		}
		facts.migrations = append(facts.migrations, state)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("check MySQL startup schema history: %w", err)
	}
	if err := validateStartupFacts(facts, expectedVersions, expectedTables); err != nil {
		return fmt.Errorf("check MySQL startup: %w", err)
	}
	return nil
}

func validateStartupFacts(facts startupFacts, expectedMigrations []int64, expectedTables []string) error {
	if err := validateManifests(expectedMigrations, expectedTables); err != nil {
		return err
	}
	if err := validateMySQLVersion(facts.version); err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(facts.defaultStorageEngine), "InnoDB") {
		return errors.New("default storage engine must be InnoDB")
	}
	if err := validateTables(facts.tables, expectedTables); err != nil {
		return err
	}
	timeZone := strings.TrimSpace(facts.sessionTimeZone)
	if facts.utcOffsetSeconds != 0 || timeZone != "+00:00" && !strings.EqualFold(timeZone, "UTC") {
		return errors.New("MySQL session time zone must be UTC")
	}
	modes := make(map[string]struct{})
	for _, mode := range strings.Split(facts.sqlMode, ",") {
		mode = strings.ToUpper(strings.TrimSpace(mode))
		if mode != "" {
			modes[mode] = struct{}{}
		}
	}
	_, strictTransactions := modes["STRICT_TRANS_TABLES"]
	_, strictAll := modes["STRICT_ALL_TABLES"]
	if !strictTransactions && !strictAll {
		return errors.New("MySQL strict SQL mode is required")
	}
	if _, unsafe := modes["ALLOW_INVALID_DATES"]; unsafe {
		return errors.New("MySQL ALLOW_INVALID_DATES mode is forbidden")
	}
	return validateMigrationState(facts.migrations, expectedMigrations)
}

func validateManifests(expectedMigrations []int64, expectedTables []string) error {
	if len(expectedMigrations) == 0 {
		return errors.New("migration manifest must contain positive versions")
	}
	previousVersion := int64(0)
	for _, version := range expectedMigrations {
		if version <= 0 || version <= previousVersion {
			return errors.New("migration manifest must contain unique ascending positive versions")
		}
		previousVersion = version
	}
	if len(expectedTables) == 0 {
		return errors.New("table manifest must contain business tables")
	}
	previousTable := ""
	for _, name := range expectedTables {
		if name == "" || previousTable != "" && name <= previousTable {
			return errors.New("table manifest must contain unique sorted business tables")
		}
		previousTable = name
	}
	return nil
}

func validateTables(actual map[string]tableState, expected []string) error {
	for _, name := range expected {
		state, exists := actual[name]
		if !exists {
			return fmt.Errorf("FinConfig table %q is missing", name)
		}
		if state.tableType != "BASE TABLE" || !strings.EqualFold(state.engine, "InnoDB") {
			return fmt.Errorf("FinConfig table %q must be an InnoDB base table", name)
		}
	}
	return nil
}

func validateMySQLVersion(version string) error {
	version = strings.TrimSpace(version)
	if strings.Contains(strings.ToLower(version), "mariadb") {
		return fmt.Errorf("unsupported MySQL version %q", version)
	}
	matches := mysqlVersionPattern.FindStringSubmatch(version)
	if len(matches) != 4 {
		return fmt.Errorf("unsupported MySQL version %q", version)
	}
	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	patch, _ := strconv.Atoi(matches[3])
	if major != 8 || minor == 4 && patch < 11 || minor == 0 && patch < 46 || minor != 0 && minor != 4 {
		return fmt.Errorf("unsupported MySQL version %q", version)
	}
	return nil
}

func validateMigrationState(states []migrationState, expected []int64) error {
	want := make(map[int64]struct{}, len(expected))
	for _, version := range expected {
		want[version] = struct{}{}
	}
	latest := make(map[int64]bool)
	for _, state := range states {
		if state.version < 0 {
			return fmt.Errorf("unexpected schema migration %d", state.version)
		}
		if _, seen := latest[state.version]; !seen {
			latest[state.version] = state.applied
		}
	}
	for version, applied := range latest {
		if version == 0 || !applied {
			continue
		}
		if _, known := want[version]; !known {
			return fmt.Errorf("unexpected schema migration %d is applied", version)
		}
	}
	missing := make([]int64, 0)
	for _, version := range expected {
		if applied, exists := latest[version]; !exists || !applied {
			missing = append(missing, version)
		}
	}
	if len(missing) > 0 {
		sort.Slice(missing, func(left, right int) bool { return missing[left] < missing[right] })
		return fmt.Errorf("schema migration %d is not applied", missing[0])
	}
	return nil
}

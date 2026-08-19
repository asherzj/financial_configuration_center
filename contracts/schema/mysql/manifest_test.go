package mysql_test

import (
	"slices"
	"testing"

	schemamysql "github.com/asherzj/financial_configuration_center/contracts/schema/mysql"
)

func TestManifestReturnsDefensiveCopies(t *testing.T) {
	t.Parallel()

	versions := schemamysql.ExpectedVersions()
	tables := schemamysql.ExpectedTables()
	versions[0] = 99
	tables[0] = "changed"

	if got, want := schemamysql.ExpectedVersions(), []int64{1, 2}; !slices.Equal(got, want) {
		t.Fatalf("versions after caller mutation = %v, want %v", got, want)
	}
	if got := schemamysql.ExpectedTables()[0]; got != "audit_records" {
		t.Fatalf("first table after caller mutation = %q, want audit_records", got)
	}
}

// Package mysql owns the versioned MySQL schema compatibility manifest shared
// by independently released FinConfig processes. It does not execute
// migrations or access a database.
package mysql

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

// ExpectedVersions returns a copy of the build-owned Goose version manifest.
func ExpectedVersions() []int64 {
	return append([]int64(nil), expectedVersions[:]...)
}

// ExpectedTables returns a copy of the exact FinConfig business-table
// manifest. Goose's bookkeeping table is intentionally excluded.
func ExpectedTables() []string {
	return append([]string(nil), expectedTables[:]...)
}

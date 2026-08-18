package mysqlstore_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/configserver"
	"github.com/asherzj/financial_configuration_center/internal/distribution/mysqlsource"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	"github.com/asherzj/financial_configuration_center/internal/pagequery"
	platformmysql "github.com/asherzj/financial_configuration_center/internal/platform/mysql"
	"github.com/asherzj/financial_configuration_center/internal/platform/mysql/migrations"
	"github.com/asherzj/financial_configuration_center/internal/release/application"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
	"github.com/asherzj/financial_configuration_center/internal/release/mysqlstore"
	"github.com/asherzj/financial_configuration_center/sdk/finconfig"
	drivermysql "github.com/go-sql-driver/mysql"
)

func TestRealMySQLBaseFinalTransaction(t *testing.T) {
	dsn := isolatedDatabase(t)
	ctx := context.Background()
	database, err := platformmysql.Open(ctx, platformmysql.Config{
		DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 2,
		ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store, err := mysqlstore.New(database)
	if err != nil {
		t.Fatal(err)
	}
	clock := fixedClock{now: time.Date(2026, 8, 19, 7, 0, 0, 0, time.UTC)}
	service := application.NewService(store, &ids{values: []string{
		"018fb4a7-6c54-7d34-bc21-357b4e943c30",
		"018fb4a7-74b6-7a5f-a4d0-11c74002dadd",
	}}, clock)
	created, err := service.CreateBaseFinal(ctx, application.CreateBaseFinalCommand{
		IdempotencyKey: "create-request-1", ModelCode: "payment-route-admin",
		Scope: release.Scope{Region: "cn", Environment: "production"}, Actor: "operator@example.com",
		Items: []application.AddDraft{{Data: map[string]string{"route_code": "visa-cn", "priority": "+0007"}, ExpectedCollectionRevision: 7}},
	})
	if err != nil {
		t.Fatalf("CreateBaseFinal: %v", err)
	}
	if _, err := service.Act(ctx, application.ActCommand{OrderID: created.ID, ActionRequestID: "018fb4a7-7c43-7de2-bad4-5ea3fc262630", ExpectedRevision: 1, Action: application.ActionExecute, Actor: "operator@example.com"}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	advanced, err := service.Act(ctx, application.ActCommand{OrderID: created.ID, ActionRequestID: "018fb4a7-83c8-73aa-924d-9b57558d3200", ExpectedRevision: 2, Action: application.ActionAdvance, Actor: "operator@example.com"})
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	completed, err := service.Act(ctx, application.ActCommand{OrderID: created.ID, ActionRequestID: "018fb4a7-8a7e-786b-a60d-8d285f483a1a", ExpectedRevision: advanced.Revision, Action: application.ActionExecute, Actor: "operator@example.com"})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if completed.Status != release.OrderSucceeded {
		t.Fatalf("completed = %+v", completed)
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertCount(t, db, `SELECT COUNT(*) FROM configuration_records WHERE environment = 'production'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM configuration_records WHERE environment = 'staging'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM configuration_change_log`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM outbox_events WHERE status = 'PENDING'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM audit_records`, 2)
	var productionRevision, stagingRevision uint64
	if err := db.QueryRow(`SELECT config_revision FROM configuration_versions WHERE collection_name = 'payment_routes' AND environment = 'production'`).Scan(&productionRevision); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT config_revision FROM configuration_versions WHERE collection_name = 'payment_routes' AND environment = 'staging'`).Scan(&stagingRevision); err != nil {
		t.Fatal(err)
	}
	if productionRevision != 8 || stagingRevision != 7 {
		t.Fatalf("environment revisions = production %d, staging %d", productionRevision, stagingRevision)
	}

	distributionSource, err := mysqlsource.New(database)
	if err != nil {
		t.Fatal(err)
	}
	productionSnapshots, err := snapshot.NewManager(distributionSource, snapshot.IdentitySeed{ServerEpoch: "epoch-1", ServerInstanceID: "server-1", SnapshotInstance: "snapshot-1"}, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := productionSnapshots.Refresh(ctx, "production"); err != nil {
		t.Fatalf("refresh production snapshot: %v", err)
	}
	page, err := pagequery.New(productionSnapshots).Query(pagequery.Request{ModelCode: "payment-route-admin", Environment: "production", Type: pagequery.TypeAll})
	if err != nil {
		t.Fatalf("query production page: %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].Values["priority"] != "7" || page.CollectionRevision != 8 {
		t.Fatalf("production page = %+v", page)
	}
	configService := configserver.New(productionSnapshots, distributionSource)
	sdkClient, err := finconfig.New(finconfig.Config{
		ConsumerID: "payment-service", ClientID: "pod-1", Environment: "production",
		Transport: configTransport{service: configService},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sdkClient.Refresh(ctx); err != nil {
		t.Fatalf("SDK refresh: %v", err)
	}
	if sdkRecord, ok := sdkClient.GetByKey("payment_routes", page.Rows[0].RecordKey); !ok || sdkRecord.Values["priority"] != "7" {
		t.Fatalf("SDK record = %+v, %t", sdkRecord, ok)
	}

	stagingSnapshots, err := snapshot.NewManager(distributionSource, snapshot.IdentitySeed{ServerEpoch: "epoch-1", ServerInstanceID: "server-2", SnapshotInstance: "snapshot-2"}, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stagingSnapshots.Refresh(ctx, "staging"); err != nil {
		t.Fatalf("refresh staging snapshot: %v", err)
	}
	stagingPage, err := pagequery.New(stagingSnapshots).Query(pagequery.Request{ModelCode: "payment-route-admin", Environment: "staging", Type: pagequery.TypeOnlyData})
	if err != nil {
		t.Fatalf("query staging page: %v", err)
	}
	if len(stagingPage.Rows) != 0 || stagingPage.CollectionRevision != 7 {
		t.Fatalf("staging page = %+v", stagingPage)
	}
}

func isolatedDatabase(t *testing.T) string {
	t.Helper()
	base := os.Getenv("FINCONFIG_TEST_MYSQL_DSN")
	if base == "" {
		t.Skip("FINCONFIG_TEST_MYSQL_DSN is not set")
	}
	config, err := drivermysql.ParseDSN(base)
	if err != nil {
		t.Fatal(err)
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	databaseName := "finconfig_release_" + hex.EncodeToString(random)
	adminConfig := config.Clone()
	adminConfig.DBName = ""
	admin, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE `" + databaseName + "`") })
	config.DBName = databaseName
	dsn := config.FormatDSN()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrations.Up(context.Background(), db, migrationDirectory(t)); err != nil {
		t.Fatal(err)
	}
	seedCatalog(t, db)
	return dsn
}

func seedCatalog(t *testing.T, db *sql.DB) {
	t.Helper()
	defaultEnabled := "false"
	fields := []catalog.FieldDefinition{
		{Name: "route_code", DisplayName: "Route code", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 0},
		{Name: "priority", DisplayName: "Priority", Type: catalog.FieldTypeInt64, Required: true, DisplayOrder: 1},
		{Name: "enabled", DisplayName: "Enabled", Type: catalog.FieldTypeBool, Required: true, DefaultValue: &defaultEnabled, DisplayOrder: 2},
	}
	model := catalog.ModelSpec{
		Fields: []catalog.ModelField{
			{Name: "route_code", Type: catalog.FieldTypeString, Required: true, Editable: true, Queryable: true, UIControl: catalog.UIControlInput, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}},
			{Name: "priority", Type: catalog.FieldTypeInt64, Required: true, Editable: true, Queryable: true, UIControl: catalog.UIControlNumber, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}},
			{Name: "enabled", Type: catalog.FieldTypeBool, Required: true, Editable: true, Queryable: true, DefaultValue: &defaultEnabled, UIControl: catalog.UIControlBoolean, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}},
		},
		ProjectionFields: []string{"route_code", "priority", "enabled"}, KeyFields: []string{"route_code"}, DefaultPageSize: 20, MaxPageSize: 100,
	}
	fieldsJSON, _ := json.Marshal(fields)
	keysJSON, _ := json.Marshal([]string{"route_code"})
	modelJSON, _ := json.Marshal(model)
	now := time.Date(2026, 8, 19, 6, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`UPDATE configuration_revision_counters SET current_revision = 7, updated_at = ? WHERE counter_name = 'global'`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO configuration_collections (name, description, fields, key_fields, sdk_delivery_enabled, schema_version, status, config_revision, created_at, created_by, updated_at, updated_by) VALUES ('payment_routes', 'Routes', ?, ?, TRUE, 1, 'ENABLED', 1, ?, 'seed', ?, 'seed')`, fieldsJSON, keysJSON, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO configuration_models (code, name, collection_name, definition, enabled, config_revision, created_at, created_by, updated_at, updated_by) VALUES ('payment-route-admin', 'Payment routes', 'payment_routes', ?, TRUE, 2, ?, 'seed', ?, 'seed')`, modelJSON, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO release_templates (code, version, model_code, release_type_code, active_slot, final_effect, template, created_at, created_by) VALUES ('base-final', 1, 'payment-route-admin', 'direct', 'A', 'BASE_FINAL', JSON_OBJECT('steps', JSON_ARRAY('BASE_APPLY', 'COMPLETE')), ?, 'seed')`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO configuration_subscriptions (id, consumer_id, collection_name, index_name, index_fields, cardinality, enabled, config_revision, created_at, created_by, updated_at, updated_by) VALUES ('018fb4a7-91a7-70d7-8cd2-18820702cd67', 'payment-service', 'payment_routes', 'by_route_code', JSON_ARRAY('route_code'), 'ONE_TO_ONE', TRUE, 3, ?, 'seed', ?, 'seed')`, now, now); err != nil {
		t.Fatal(err)
	}
	emptyDigest := "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945"
	for _, environment := range []string{"production", "staging"} {
		if _, err := db.Exec(`INSERT INTO configuration_versions (collection_name, environment, config_revision, base_digest, overlay_digest, release_order_id, updated_at) VALUES ('payment_routes', ?, 7, ?, ?, NULL, ?)`, environment, emptyDigest, emptyDigest, now); err != nil {
			t.Fatal(err)
		}
	}
}

func migrationDirectory(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate migration test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../db/migrations/mysql"))
}

func assertCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("count for %q = %d, want %d", query, got, want)
	}
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type ids struct {
	values []string
	next   int
}

func (ids *ids) NewID() string {
	value := ids.values[ids.next]
	ids.next++
	return value
}

func (*ids) NewReleaseNumber(time.Time) string { return "REL-20260819-0001" }

type configTransport struct{ service *configserver.Service }

func (transport configTransport) GetSnapshot(ctx context.Context, request finconfig.SnapshotRequest) (finconfig.SnapshotResponse, error) {
	known := make([]configserver.Version, len(request.KnownVersions))
	for index, version := range request.KnownVersions {
		known[index] = configserver.Version{Collection: version.Collection, Revision: version.Revision, Digest: version.Digest}
	}
	response, err := transport.service.GetSnapshot(ctx, configserver.GetSnapshotRequest{
		ConsumerID: request.ConsumerID, ClientID: request.ClientID, Environment: request.Environment, KnownVersions: known,
	})
	if err != nil {
		return finconfig.SnapshotResponse{}, err
	}
	converted := finconfig.SnapshotResponse{
		Identity: finconfig.SnapshotIdentity{
			ServerEpoch: response.Identity.ServerEpoch, ServerInstanceID: response.Identity.ServerInstanceID,
			SnapshotInstance: response.Identity.SnapshotInstance, Generation: response.Identity.Generation,
		},
		Environment: response.Environment, DeletedCollections: response.DeletedCollections,
		Collections: make([]finconfig.CollectionPayload, len(response.Collections)),
	}
	for index, collection := range response.Collections {
		converted.Collections[index] = finconfig.CollectionPayload{Name: collection.Name, Revision: collection.Revision, Digest: collection.Digest, Records: make([]finconfig.Record, len(collection.Records))}
		for recordIndex, record := range collection.Records {
			converted.Collections[index].Records[recordIndex] = finconfig.Record{Key: record.RecordKey, Revision: record.RecordRevision, Values: record.Data}
		}
	}
	return converted, nil
}

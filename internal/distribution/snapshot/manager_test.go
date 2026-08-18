package snapshot_test

import (
	"context"
	"errors"
	"testing"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
)

func TestRefreshPublishesOneImmutableGeneration(t *testing.T) {
	t.Parallel()

	definition, model := snapshotCatalog(t)
	source := &stubSource{collections: []snapshot.CollectionInput{{
		Definition: definition,
		Models:     []catalog.CompiledModel{model},
		Version:    8,
		Records: []catalog.ConfigurationRecord{{
			Collection: definition.Name(), Environment: "production", RecordKey: "WyJ2aXNhLWNuIl0",
			Data: map[string]string{"route_code": "visa-cn", "priority": "7", "enabled": "false"}, ConfigRevision: 8,
		}},
	}}}
	manager, err := snapshot.NewManager(source, snapshot.IdentitySeed{
		ServerEpoch: "epoch-1", ServerInstanceID: "server-1", SnapshotInstance: "snapshot-1",
	}, fixedClock{now: time.Date(2026, 8, 19, 4, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	before := manager.Current()
	if before.Identity().Generation != 0 {
		t.Fatalf("initial generation = %d", before.Identity().Generation)
	}

	result, err := manager.Refresh(context.Background(), "production")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if result.Generation != 1 || result.CollectionCount != 1 {
		t.Fatalf("refresh result = %+v", result)
	}
	published := manager.Current()
	if published == before || published.Identity().Generation != 1 {
		t.Fatalf("snapshot was not atomically replaced: %+v", published.Identity())
	}
	record, ok := published.Record(definition.Name(), "WyJ2aXNhLWNuIl0")
	if !ok || record.Data["priority"] != "7" {
		t.Fatalf("published record = %+v, %t", record, ok)
	}

	record.Data["priority"] = "mutated"
	again, _ := manager.Current().Record(definition.Name(), record.RecordKey)
	if again.Data["priority"] != "7" {
		t.Fatal("published record was mutable through a reader")
	}
	source.collections[0].Records[0].Data["priority"] = "source-mutated"
	again, _ = manager.Current().Record(definition.Name(), record.RecordKey)
	if again.Data["priority"] != "7" {
		t.Fatal("published record retained source map ownership")
	}
}

func TestRefreshFailureRetainsLastKnownGood(t *testing.T) {
	t.Parallel()

	definition, model := snapshotCatalog(t)
	source := &stubSource{collections: []snapshot.CollectionInput{{Definition: definition, Models: []catalog.CompiledModel{model}, Version: 7}}}
	manager, err := snapshot.NewManager(source, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance"}, fixedClock{now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), "production"); err != nil {
		t.Fatal(err)
	}
	lastKnownGood := manager.Current()
	source.err = errors.New("mysql unavailable")
	if _, err := manager.Refresh(context.Background(), "production"); err == nil {
		t.Fatal("failed source refresh succeeded")
	}
	if manager.Current() != lastKnownGood || manager.Current().Identity().Generation != 1 {
		t.Fatal("refresh failure replaced last-known-good")
	}
}

type stubSource struct {
	collections []snapshot.CollectionInput
	err         error
}

func (source *stubSource) LoadEnvironment(context.Context, string) ([]snapshot.CollectionInput, error) {
	if source.err != nil {
		return nil, source.err
	}
	return source.collections, nil
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func snapshotCatalog(t *testing.T) (catalog.CollectionDefinition, catalog.CompiledModel) {
	t.Helper()
	defaultEnabled := "false"
	definition, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: "payment_routes", SDKDeliveryEnabled: true, SchemaVersion: 1, KeyFields: []string{"route_code"},
		Fields: []catalog.FieldDefinition{
			{Name: "route_code", DisplayName: "Route code", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "priority", DisplayName: "Priority", Type: catalog.FieldTypeInt64, Required: true, DisplayOrder: 1},
			{Name: "enabled", DisplayName: "Enabled", Type: catalog.FieldTypeBool, Required: true, DefaultValue: &defaultEnabled, DisplayOrder: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	model, err := catalog.CompileModel(definition, catalog.ModelSpec{
		Code: "payment-route-admin", Name: "Payment routes", Collection: definition.Name(),
		Fields: []catalog.ModelField{
			{Name: "route_code", Type: catalog.FieldTypeString, Required: true, Editable: true, Queryable: true, UIControl: catalog.UIControlInput, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}},
			{Name: "priority", Type: catalog.FieldTypeInt64, Required: true, Editable: true, Queryable: true, UIControl: catalog.UIControlNumber, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}},
			{Name: "enabled", Type: catalog.FieldTypeBool, Required: true, Editable: true, Queryable: true, DefaultValue: &defaultEnabled, UIControl: catalog.UIControlBoolean, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}},
		},
		ProjectionFields: []string{"route_code", "priority", "enabled"}, KeyFields: []string{"route_code"}, DefaultPageSize: 20, MaxPageSize: 100, ConfigRevision: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	return definition, model
}

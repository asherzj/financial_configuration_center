package pagequery_test

import (
	"context"
	"testing"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	"github.com/asherzj/financial_configuration_center/internal/pagequery"
)

func TestAllReturnsRowsAndModelDrivenInteractionMetadata(t *testing.T) {
	t.Parallel()

	manager, model, keys := querySnapshot(t)
	querier := pagequery.New(manager)
	one := int32(1)
	result, err := querier.Query(pagequery.Request{
		ModelCode: model.Code(), Environment: "production", Type: pagequery.TypeAll,
		Page: pagequery.PageSpec{Number: &one, Size: &one},
	})
	if err != nil {
		t.Fatalf("Query ALL: %v", err)
	}
	if result.PageNumber != 1 || result.PageSize != 1 || result.TotalNumber != 2 || result.TotalPages != 2 {
		t.Fatalf("page = %+v", result)
	}
	if len(result.Rows) != 1 || result.Rows[0].RecordKey != keys[0] || result.Rows[0].Values["priority"] != "1" {
		t.Fatalf("rows are not stable/projected: %+v", result.Rows)
	}
	if len(result.ProjectionFields) != 3 || len(result.InteractionFields) != 3 {
		t.Fatalf("ALL metadata is incomplete: projection=%v fields=%+v", result.ProjectionFields, result.InteractionFields)
	}
	first := result.InteractionFields[0]
	if first.Name != "route_code" || !first.Projected || !first.KeyField || !first.Editable || !first.Queryable || len(first.AllowedFilterOperators) == 0 {
		t.Fatalf("interaction field is incomplete: %+v", first)
	}
	if result.CollectionRevision != 8 || result.Snapshot.Generation != 1 {
		t.Fatalf("authority metadata = revision %d snapshot %+v", result.CollectionRevision, result.Snapshot)
	}

	result.Rows[0].Values["priority"] = "mutated"
	again, err := querier.Query(pagequery.Request{ModelCode: model.Code(), Environment: "production", Type: pagequery.TypeAll})
	if err != nil {
		t.Fatal(err)
	}
	if again.Rows[0].Values["priority"] != "1" || again.PageSize != 20 {
		t.Fatalf("query leaked mutation or ignored default size: %+v", again)
	}
}

func TestOnlyDataOmitsInteractionMetadataAndRejectsInvalidPage(t *testing.T) {
	t.Parallel()

	manager, model, _ := querySnapshot(t)
	querier := pagequery.New(manager)
	result, err := querier.Query(pagequery.Request{ModelCode: model.Code(), Environment: "production", Type: pagequery.TypeOnlyData})
	if err != nil {
		t.Fatalf("Query ONLY_DATA: %v", err)
	}
	if len(result.Rows) != 2 || result.ProjectionFields != nil || result.InteractionFields != nil {
		t.Fatalf("ONLY_DATA response = %+v", result)
	}
	zero := int32(0)
	if _, err := querier.Query(pagequery.Request{ModelCode: model.Code(), Environment: "production", Type: pagequery.TypeOnlyData, Page: pagequery.PageSpec{Number: &zero}}); err == nil {
		t.Fatal("explicit page zero succeeded")
	}
	if _, err := querier.Query(pagequery.Request{ModelCode: model.Code(), Environment: "staging", Type: pagequery.TypeOnlyData}); err == nil {
		t.Fatal("querying a different environment from the snapshot succeeded")
	}
}

type source struct{ input []snapshot.CollectionInput }

func (source source) LoadEnvironment(context.Context, string) ([]snapshot.CollectionInput, error) {
	return source.input, nil
}

type clock struct{}

func (clock) Now() time.Time { return time.Date(2026, 8, 19, 5, 0, 0, 0, time.UTC) }

func querySnapshot(t *testing.T) (*snapshot.Manager, catalog.CompiledModel, []string) {
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
			{Name: "route_code", Type: catalog.FieldTypeString, Required: true, Editable: true, Queryable: true, UIControl: catalog.UIControlInput, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact, catalog.FilterContains}},
			{Name: "priority", Type: catalog.FieldTypeInt64, Required: true, Editable: true, Queryable: true, UIControl: catalog.UIControlNumber, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact, catalog.FilterClosedRange}},
			{Name: "enabled", Type: catalog.FieldTypeBool, Required: true, Editable: true, Queryable: true, DefaultValue: &defaultEnabled, UIControl: catalog.UIControlBoolean, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}},
		},
		ProjectionFields: []string{"route_code", "priority", "enabled"}, KeyFields: []string{"route_code"}, DefaultPageSize: 20, MaxPageSize: 100, ConfigRevision: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := definition.NewRecord("production", map[string]string{"route_code": "a", "priority": "1"})
	second, _ := definition.NewRecord("production", map[string]string{"route_code": "b", "priority": "2"})
	first.ConfigRevision, second.ConfigRevision = 8, 8
	manager, err := snapshot.NewManager(source{input: []snapshot.CollectionInput{{
		Definition: definition, Models: []catalog.CompiledModel{model}, Version: 8,
		Records: []catalog.ConfigurationRecord{second, first},
	}}}, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance"}, clock{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), "production"); err != nil {
		t.Fatal(err)
	}
	return manager, model, []string{first.RecordKey, second.RecordKey}
}

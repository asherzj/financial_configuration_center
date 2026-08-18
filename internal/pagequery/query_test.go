package pagequery_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
	"github.com/asherzj/financial_configuration_center/internal/pagequery"
)

func TestAllReturnsRowsAndModelDrivenInteractionMetadata(t *testing.T) {
	t.Parallel()

	manager, model, keys := querySnapshot(t)
	querier := pagequery.New(manager)
	one := int32(1)
	result, err := querier.Query(pagequery.Request{
		ModelCode: model.Code(), Region: "cn", Environment: "production", Type: pagequery.TypeAll,
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
	if len(result.ProjectionFields) != 3 || len(result.InteractionFields) != 3 || len(result.ReleaseTypes) != 2 {
		t.Fatalf("ALL metadata is incomplete: projection=%v fields=%+v", result.ProjectionFields, result.InteractionFields)
	}
	if result.ReleaseTypes[0].Code != "direct" || !result.ReleaseTypes[0].Available || result.ReleaseTypes[1].UnavailableReasonCode != "TEMPLATE_DISABLED" {
		t.Fatalf("release types = %+v", result.ReleaseTypes)
	}
	first := result.InteractionFields[0]
	if first.Name != "route_code" || !first.Projected || !first.KeyField || !first.Editable || !first.Queryable || len(first.AllowedFilterOperators) == 0 || len(first.ValidationRules) != 1 {
		t.Fatalf("interaction field is incomplete: %+v", first)
	}
	if result.CollectionRevision != 8 || result.Snapshot.Generation != 1 {
		t.Fatalf("authority metadata = revision %d snapshot %+v", result.CollectionRevision, result.Snapshot)
	}

	result.Rows[0].Values["priority"] = "mutated"
	again, err := querier.Query(pagequery.Request{ModelCode: model.Code(), Region: "cn", Environment: "production", Type: pagequery.TypeAll})
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
	result, err := querier.Query(pagequery.Request{ModelCode: model.Code(), Region: "cn", Environment: "production", Type: pagequery.TypeOnlyData})
	if err != nil {
		t.Fatalf("Query ONLY_DATA: %v", err)
	}
	if len(result.Rows) != 2 || result.ProjectionFields != nil || result.InteractionFields != nil || result.ReleaseTypes != nil {
		t.Fatalf("ONLY_DATA response = %+v", result)
	}
	zero := int32(0)
	if _, err := querier.Query(pagequery.Request{ModelCode: model.Code(), Region: "cn", Environment: "production", Type: pagequery.TypeOnlyData, Page: pagequery.PageSpec{Number: &zero}}); err == nil {
		t.Fatal("explicit page zero succeeded")
	}
	if _, err := querier.Query(pagequery.Request{ModelCode: model.Code(), Region: "cn", Environment: "staging", Type: pagequery.TypeOnlyData}); err == nil {
		t.Fatal("querying a different environment from the snapshot succeeded")
	}
}

func TestOnlyDataCompilesTypedConditionsBeforePagination(t *testing.T) {
	t.Parallel()
	manager, model, keys := querySnapshot(t)
	querier := pagequery.New(manager)
	pageNumber := int32(99)
	result, err := querier.Query(pagequery.Request{
		ModelCode: model.Code(), Region: "cn", Environment: "production", Type: pagequery.TypeOnlyData,
		Page: pagequery.PageSpec{Number: &pageNumber},
		Conditions: []pagequery.FilterCondition{
			{Field: "route_code", Operator: catalog.FilterContains, Value: &pagequery.ScalarValue{Type: catalog.FieldTypeString, Canonical: "b"}},
			{Field: "priority", Operator: catalog.FilterClosedRange, Lower: &pagequery.ScalarValue{Canonical: "+2"}, Upper: &pagequery.ScalarValue{Canonical: "2"}},
		},
	})
	if err != nil {
		t.Fatalf("Query conditions: %v", err)
	}
	if len(result.Rows) != 1 || result.Rows[0].RecordKey != keys[1] || result.PageNumber != 1 || result.TotalNumber != 1 {
		t.Fatalf("filtered page = %+v", result)
	}
	_, err = querier.Query(pagequery.Request{
		ModelCode: model.Code(), Region: "cn", Environment: "production", Type: pagequery.TypeOnlyData,
		Conditions: []pagequery.FilterCondition{{
			Field: "priority", Operator: catalog.FilterExact, Value: &pagequery.ScalarValue{Canonical: "1"},
			Set: []pagequery.ScalarValue{{Canonical: "2"}},
		}},
	})
	if err == nil {
		t.Fatal("malformed EXACT condition succeeded")
	}
}

func TestQueryPageReturnsScopeEffectiveValuesAndBaseDiff(t *testing.T) {
	t.Parallel()

	manager, model, keys := querySnapshot(t)
	activation := catalog.ConfigRevision(9)
	manager, err := snapshot.NewManager(source{input: []snapshot.CollectionInput{{
		Definition: mustDefinition(t), Models: []catalog.CompiledModel{model}, Version: 10,
		Records: []catalog.ConfigurationRecord{
			mustRecord(t, mustDefinition(t), "production", map[string]string{"route_code": "a", "priority": "1"}, 8),
			mustRecord(t, mustDefinition(t), "production", map[string]string{"route_code": "b", "priority": "2"}, 8),
		},
		OverlayRules: []overlay.Rule{
			{ID: "blue", Collection: "payment_routes", Scope: overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"}, RecordKey: keys[0], Action: overlay.ActionModify, Content: map[string]string{"route_code": "a", "priority": "7", "enabled": "false"}, ConfigRevision: 9, ActivatedRevision: &activation},
			{ID: "green", Collection: "payment_routes", Scope: overlay.Scope{Region: "cn", Environment: "production", Stage: "green"}, RecordKey: keys[0], Action: overlay.ActionModify, Content: map[string]string{"route_code": "a", "priority": "9", "enabled": "false"}, ConfigRevision: 10, ActivatedRevision: &activation},
		},
	}}}, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "scoped"}, clock{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), "production"); err != nil {
		t.Fatal(err)
	}
	querier := pagequery.New(manager)
	blue, err := querier.Query(pagequery.Request{ModelCode: model.Code(), Region: "cn", Environment: "production", Stage: "blue", Type: pagequery.TypeAll})
	if err != nil {
		t.Fatalf("Query blue: %v", err)
	}
	green, err := querier.Query(pagequery.Request{ModelCode: model.Code(), Region: "cn", Environment: "production", Stage: "green", Type: pagequery.TypeAll})
	if err != nil {
		t.Fatalf("Query green: %v", err)
	}
	if blue.Rows[0].Values["priority"] != "7" || green.Rows[0].Values["priority"] != "9" {
		t.Fatalf("scope effective values: blue=%+v green=%+v", blue.Rows[0], green.Rows[0])
	}
	if !blue.Rows[0].BasePresent || blue.Rows[0].BaseValues["priority"] != "1" || len(blue.Rows[0].ChangedFields) != 1 || blue.Rows[0].ChangedFields[0] != "priority" {
		t.Fatalf("blue base diff = %+v", blue.Rows[0])
	}
}

func TestAllResolvesCollectionOptionsAndMasksSensitiveProjection(t *testing.T) {
	t.Parallel()
	providerDefinition, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: "providers", KeyFields: []string{"code"}, SchemaVersion: 1,
		Fields: []catalog.FieldDefinition{
			{Name: "code", DisplayName: "Code", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "label", DisplayName: "Label", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	credentialDefinition, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: "credentials", KeyFields: []string{"name"}, SchemaVersion: 1,
		Fields: []catalog.FieldDefinition{
			{Name: "name", DisplayName: "Name", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "provider", DisplayName: "Provider", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 1},
			{Name: "secret", DisplayName: "Secret", Type: catalog.FieldTypeString, Required: true, Sensitive: true, DisplayOrder: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	model, err := catalog.CompileModel(credentialDefinition, catalog.ModelSpec{
		Code: "credential-admin", Name: "Credentials", Collection: credentialDefinition.Name(),
		Fields: []catalog.ModelField{
			{Name: "name", Type: catalog.FieldTypeString, Required: true, Editable: true, Queryable: true, UIControl: catalog.UIControlInput, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}},
			{Name: "provider", Type: catalog.FieldTypeString, Required: true, Editable: true, Queryable: true, UIControl: catalog.UIControlSelect, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}, OptionSource: &catalog.OptionSourceDefinition{Kind: catalog.OptionSourceCollection, Collection: "providers", ValueField: "code", LabelField: "label", Limit: 100}},
			{Name: "secret", Type: catalog.FieldTypeString, Required: true, Sensitive: true, Editable: true, UIControl: catalog.UIControlInput},
		},
		ProjectionFields: []string{"name", "provider", "secret"}, KeyFields: []string{"name"}, DefaultPageSize: 20, MaxPageSize: 100, ConfigRevision: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	providerA := mustRecord(t, providerDefinition, "production", map[string]string{"code": "stripe", "label": "Stripe"}, 2)
	providerB := mustRecord(t, providerDefinition, "production", map[string]string{"code": "adyen", "label": "Adyen"}, 2)
	credential := mustRecord(t, credentialDefinition, "production", map[string]string{"name": "primary", "provider": "stripe", "secret": "plaintext"}, 3)
	manager, err := snapshot.NewManager(source{input: []snapshot.CollectionInput{
		{Definition: providerDefinition, Version: 2, Records: []catalog.ConfigurationRecord{providerA, providerB}},
		{Definition: credentialDefinition, Models: []catalog.CompiledModel{model}, Version: 3, Records: []catalog.ConfigurationRecord{credential}},
	}}, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "options"}, clock{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), "production"); err != nil {
		t.Fatal(err)
	}
	result, err := pagequery.New(manager).Query(pagequery.Request{ModelCode: model.Code(), Region: "cn", Environment: "production", Type: pagequery.TypeAll})
	if err != nil {
		t.Fatalf("Query ALL: %v", err)
	}
	providerField := result.InteractionFields[1]
	if len(providerField.Options) != 2 || providerField.Options[0].Code != "adyen" || providerField.Options[1].Label != "Stripe" {
		t.Fatalf("resolved collection options = %+v", providerField.Options)
	}
	if _, leaked := result.Rows[0].Values["secret"]; leaked || !reflect.DeepEqual(result.Rows[0].MaskedFields, []string{"secret"}) {
		t.Fatalf("sensitive row leaked = %+v", result.Rows[0])
	}
	if _, err := pagequery.New(manager).Query(pagequery.Request{
		ModelCode: model.Code(), Region: "cn", Environment: "production", Type: pagequery.TypeOnlyData,
		Conditions: []pagequery.FilterCondition{{Field: "provider", Operator: catalog.FilterExact, Value: &pagequery.ScalarValue{Canonical: "missing"}}},
	}); err == nil {
		t.Fatal("missing collection option query selection succeeded")
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
	definition := mustDefinition(t)
	defaultEnabled := "false"
	model, err := catalog.CompileModel(definition, catalog.ModelSpec{
		Code: "payment-route-admin", Name: "Payment routes", Collection: definition.Name(),
		Fields: []catalog.ModelField{
			{Name: "route_code", Type: catalog.FieldTypeString, Required: true, Editable: true, Queryable: true, UIControl: catalog.UIControlInput, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact, catalog.FilterContains}, ValidationRules: []catalog.ValidationRule{{Kind: catalog.ValidationMinLength, Params: map[string]string{"value": "1"}, Message: "route code is required"}}},
			{Name: "priority", Type: catalog.FieldTypeInt64, Required: true, Editable: true, Queryable: true, UIControl: catalog.UIControlNumber, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact, catalog.FilterClosedRange}},
			{Name: "enabled", Type: catalog.FieldTypeBool, Required: true, Editable: true, Queryable: true, DefaultValue: &defaultEnabled, UIControl: catalog.UIControlBoolean, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}},
		},
		ProjectionFields: []string{"route_code", "priority", "enabled"}, KeyFields: []string{"route_code"}, DefaultPageSize: 20, MaxPageSize: 100,
		ReleaseTypes: []catalog.ReleaseTypeDefinition{
			{Code: "direct", Name: "Direct", TemplateCode: "base-final", Enabled: true, Available: true},
			{Code: "approval", Name: "Approval", TemplateCode: "approval-final", Enabled: true, UnavailableReasonCode: "TEMPLATE_DISABLED"},
		},
		ConfigRevision: 7,
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

func mustDefinition(t *testing.T) catalog.CollectionDefinition {
	t.Helper()
	defaultEnabled := "false"
	definition, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: "payment_routes", SDKDeliveryEnabled: true, SchemaVersion: 1, KeyFields: []string{"route_code"},
		Fields: []catalog.FieldDefinition{
			{Name: "route_code", DisplayName: "Route code", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 0, ValidationRules: []catalog.ValidationRule{{Kind: catalog.ValidationMinLength, Params: map[string]string{"value": "1"}, Message: "route code is required"}}},
			{Name: "priority", DisplayName: "Priority", Type: catalog.FieldTypeInt64, Required: true, DisplayOrder: 1},
			{Name: "enabled", DisplayName: "Enabled", Type: catalog.FieldTypeBool, Required: true, DefaultValue: &defaultEnabled, DisplayOrder: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func mustRecord(t *testing.T, definition catalog.CollectionDefinition, environment string, data map[string]string, revision catalog.ConfigRevision) catalog.ConfigurationRecord {
	t.Helper()
	record, err := definition.NewRecord(environment, data)
	if err != nil {
		t.Fatal(err)
	}
	record.ConfigRevision = revision
	return record
}

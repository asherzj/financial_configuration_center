package domain_test

import (
	"testing"

	"github.com/asherzj/financial_configuration_center/internal/catalog/domain"
)

func routeCollectionSpec() domain.CollectionSpec {
	defaultEnabled := "false"
	return domain.CollectionSpec{
		Name:        "payment_routes",
		Description: "Payment routing configuration",
		Fields: []domain.FieldDefinition{
			{Name: "route_code", DisplayName: "Route code", Type: domain.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "priority", DisplayName: "Priority", Type: domain.FieldTypeInt64, Required: true, DisplayOrder: 1},
			{Name: "enabled", DisplayName: "Enabled", Type: domain.FieldTypeBool, Required: true, DefaultValue: &defaultEnabled, DisplayOrder: 2},
			{Name: "credential", DisplayName: "Credential", Type: domain.FieldTypeString, Sensitive: true, DisplayOrder: 3},
		},
		KeyFields:          []string{"route_code"},
		SDKDeliveryEnabled: true,
		SchemaVersion:      1,
	}
}

func TestCompileCollectionOwnsCanonicalRecordCreation(t *testing.T) {
	t.Parallel()

	definition, err := domain.CompileCollection(routeCollectionSpec())
	if err != nil {
		t.Fatalf("CompileCollection: %v", err)
	}
	record, err := definition.NewRecord(" production ", map[string]string{
		"route_code": "visa-cn",
		"priority":   "+0007",
	})
	if err != nil {
		t.Fatalf("NewRecord: %v", err)
	}
	if record.Collection != "payment_routes" || record.Environment != "production" {
		t.Fatalf("record identity = %+v", record)
	}
	if record.Data["priority"] != "7" || record.Data["enabled"] != "false" {
		t.Fatalf("record data was not canonical/defaulted: %#v", record.Data)
	}
	wantKey, err := domain.EncodeKey([]string{"route_code"}, map[string]string{"route_code": "visa-cn"})
	if err != nil {
		t.Fatal(err)
	}
	if record.RecordKey != wantKey {
		t.Fatalf("RecordKey = %q, want %q", record.RecordKey, wantKey)
	}

	record.Data["priority"] = "mutated"
	again, err := definition.NewRecord("production", map[string]string{"route_code": "visa-cn", "priority": "7"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Data["priority"] != "7" {
		t.Fatal("record construction retained caller mutation")
	}
}

func TestCompileCollectionRejectsInvalidContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*domain.CollectionSpec)
	}{
		{name: "no fields", mutate: func(spec *domain.CollectionSpec) { spec.Fields = nil }},
		{name: "duplicate field", mutate: func(spec *domain.CollectionSpec) { spec.Fields[1].Name = spec.Fields[0].Name }},
		{name: "duplicate display order", mutate: func(spec *domain.CollectionSpec) { spec.Fields[1].DisplayOrder = spec.Fields[0].DisplayOrder }},
		{name: "unknown key field", mutate: func(spec *domain.CollectionSpec) { spec.KeyFields = []string{"missing"} }},
		{name: "sensitive key field", mutate: func(spec *domain.CollectionSpec) { spec.KeyFields = []string{"credential"} }},
		{name: "bad default", mutate: func(spec *domain.CollectionSpec) { invalid := "not-bool"; spec.Fields[2].DefaultValue = &invalid }},
		{name: "zero schema version", mutate: func(spec *domain.CollectionSpec) { spec.SchemaVersion = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			spec := routeCollectionSpec()
			tt.mutate(&spec)
			if _, err := domain.CompileCollection(spec); err == nil {
				t.Fatal("CompileCollection succeeded")
			}
		})
	}
}

func TestCollectionRejectsIncompleteOrUnknownRecordFields(t *testing.T) {
	t.Parallel()

	definition, err := domain.CompileCollection(routeCollectionSpec())
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []map[string]string{
		{"route_code": "visa-cn"},
		{"route_code": "visa-cn", "priority": "one"},
		{"route_code": "visa-cn", "priority": "1", "unknown": "value"},
	} {
		if _, err := definition.NewRecord("production", data); err == nil {
			t.Fatalf("NewRecord(%#v) succeeded", data)
		}
	}
	if _, err := definition.NewRecord(" ", map[string]string{"route_code": "visa-cn", "priority": "1"}); err == nil {
		t.Fatal("empty environment succeeded")
	}
}

func TestCompileModelRequiresCollectionSemanticsToMatch(t *testing.T) {
	t.Parallel()

	definition, err := domain.CompileCollection(routeCollectionSpec())
	if err != nil {
		t.Fatal(err)
	}
	model := domain.ModelSpec{
		Code:       "payment-route-admin",
		Name:       "Payment routes",
		Collection: "payment_routes",
		Fields: []domain.ModelField{
			{Name: "route_code", Type: domain.FieldTypeString, Required: true, Editable: true, Queryable: true, UIControl: domain.UIControlInput, AllowedFilterOperators: []domain.FilterOperator{domain.FilterExact}},
			{Name: "priority", Type: domain.FieldTypeInt64, Required: true, Editable: true, Queryable: true, UIControl: domain.UIControlNumber, AllowedFilterOperators: []domain.FilterOperator{domain.FilterExact}},
			{Name: "enabled", Type: domain.FieldTypeBool, Required: true, Editable: true, Queryable: true, DefaultValue: ptr("false"), UIControl: domain.UIControlBoolean, AllowedFilterOperators: []domain.FilterOperator{domain.FilterExact}},
			{Name: "credential", Type: domain.FieldTypeString, Sensitive: true, Editable: true, UIControl: domain.UIControlInput},
		},
		ProjectionFields: []string{"route_code", "priority", "enabled"},
		KeyFields:        []string{"route_code"},
		DefaultPageSize:  20,
		MaxPageSize:      100,
		ConfigRevision:   7,
	}
	compiled, err := domain.CompileModel(definition, model)
	if err != nil {
		t.Fatalf("CompileModel: %v", err)
	}
	if compiled.Code() != model.Code || compiled.Collection() != definition.Name() {
		t.Fatalf("compiled model identity is wrong: %s/%s", compiled.Code(), compiled.Collection())
	}

	model.Fields[1].Type = domain.FieldTypeString
	if _, err := domain.CompileModel(definition, model); err == nil {
		t.Fatal("model with mismatched field type succeeded")
	}
}

func ptr(value string) *string { return &value }

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
		ReleaseTypes: []domain.ReleaseTypeDefinition{
			{Code: "direct", Name: "Direct", TemplateCode: "base-final", Enabled: true, Available: true},
		},
		ConfigRevision: 7,
	}
	compiled, err := domain.CompileModel(definition, model)
	if err != nil {
		t.Fatalf("CompileModel: %v", err)
	}
	if compiled.Code() != model.Code || compiled.Collection() != definition.Name() {
		t.Fatalf("compiled model identity is wrong: %s/%s", compiled.Code(), compiled.Collection())
	}
	if got := compiled.ReleaseTypes(); len(got) != 1 || got[0].Code != "direct" || !got[0].Available {
		t.Fatalf("compiled release types = %+v", got)
	}
	model.ReleaseTypes[0].Code = "changed"
	if compiled.ReleaseTypes()[0].Code != "direct" {
		t.Fatal("compiled release types leaked source mutation")
	}

	model.Fields[1].Type = domain.FieldTypeString
	if _, err := domain.CompileModel(definition, model); err == nil {
		t.Fatal("model with mismatched field type succeeded")
	}
}

func TestCompileModelRejectsInvalidReleaseTypes(t *testing.T) {
	t.Parallel()
	definition, err := domain.CompileCollection(routeCollectionSpec())
	if err != nil {
		t.Fatal(err)
	}
	base := domain.ModelSpec{
		Code: "model", Name: "Model", Collection: definition.Name(),
		Fields: []domain.ModelField{
			{Name: "route_code", Type: domain.FieldTypeString, Required: true, UIControl: domain.UIControlInput},
			{Name: "priority", Type: domain.FieldTypeInt64, Required: true, UIControl: domain.UIControlNumber},
			{Name: "enabled", Type: domain.FieldTypeBool, Required: true, DefaultValue: ptr("false"), UIControl: domain.UIControlBoolean},
			{Name: "credential", Type: domain.FieldTypeString, Sensitive: true, UIControl: domain.UIControlInput},
		},
		ProjectionFields: []string{"route_code"}, KeyFields: []string{"route_code"}, DefaultPageSize: 20, MaxPageSize: 100, ConfigRevision: 1,
	}
	for _, releaseTypes := range [][]domain.ReleaseTypeDefinition{
		{{Code: "direct", Name: "", TemplateCode: "base-final", Enabled: true, Available: true}},
		{{Code: "direct", Name: "Direct", TemplateCode: "base-final", Available: true}},
		{{Code: "direct", Name: "Direct", TemplateCode: "base-final"}, {Code: "direct", Name: "Duplicate", TemplateCode: "other"}},
	} {
		base.ReleaseTypes = releaseTypes
		if _, err := domain.CompileModel(definition, base); err == nil {
			t.Fatalf("invalid release types compiled: %+v", releaseTypes)
		}
	}
}

func TestCompileModelRequiresTypedOptionsAndProtectsSensitiveFields(t *testing.T) {
	t.Parallel()
	definition := sensitiveOptionCollection(t)
	base := domain.ModelSpec{
		Code: "credential-admin", Name: "Credentials", Collection: definition.Name(),
		Fields: []domain.ModelField{
			{Name: "name", Type: domain.FieldTypeString, Required: true, Editable: true, Queryable: true, UIControl: domain.UIControlSelect, AllowedFilterOperators: []domain.FilterOperator{domain.FilterExact}},
			{Name: "secret", Type: domain.FieldTypeString, Required: true, Sensitive: true, Editable: true, UIControl: domain.UIControlInput},
		},
		ProjectionFields: []string{"name", "secret"}, KeyFields: []string{"name"}, DefaultPageSize: 20, MaxPageSize: 100, ConfigRevision: 3,
	}
	if _, err := domain.CompileModel(definition, base); err == nil {
		t.Fatal("SELECT without option source compiled")
	}
	base.Fields[0].OptionSource = &domain.OptionSourceDefinition{Kind: domain.OptionSourceStatic, StaticOptions: []domain.SelectOptionDefinition{{Code: "visa", Label: "Visa"}, {Code: "disabled", Label: "Legacy", Disabled: true}}}
	model, err := domain.CompileModel(definition, base)
	if err != nil {
		t.Fatalf("CompileModel: %v", err)
	}
	options := model.Fields()[0].OptionSource.StaticOptions
	if len(options) != 2 || options[1].Code != "visa" {
		t.Fatalf("compiled static options = %+v", options)
	}
	base.Fields[1].Queryable = true
	base.Fields[1].AllowedFilterOperators = []domain.FilterOperator{domain.FilterExact}
	if _, err := domain.CompileModel(definition, base); err == nil {
		t.Fatal("sensitive queryable field compiled")
	}
	base.Fields[1].Queryable = false
	base.Fields[1].AllowedFilterOperators = nil
	base.Fields[1].Editable = false
	base.AutoFillRules = []domain.AutoFillRule{{Field: "secret", Source: domain.AutoFillActorSubject}}
	if _, err := domain.CompileModel(definition, base); err == nil {
		t.Fatal("sensitive auto-fill field compiled")
	}
}

func TestValidationRulesAreCompiledEnforcedAndImmutable(t *testing.T) {
	t.Parallel()
	rules := []domain.ValidationRule{
		{Kind: domain.ValidationRegex, Params: map[string]string{"pattern": `^[a-z][a-z0-9-]+$`}, Message: "must be a slug"},
		{Kind: domain.ValidationMinLength, Params: map[string]string{"value": "3"}, Message: "too short"},
		{Kind: domain.ValidationMaxLength, Params: map[string]string{"value": "12"}, Message: "too long"},
	}
	definition, err := domain.CompileCollection(domain.CollectionSpec{
		Name: "validated", KeyFields: []string{"code"}, SchemaVersion: 1,
		Fields: []domain.FieldDefinition{{Name: "code", DisplayName: "Code", Type: domain.FieldTypeString, Required: true, DisplayOrder: 0, ValidationRules: rules}},
	})
	if err != nil {
		t.Fatal(err)
	}
	model, err := domain.CompileModel(definition, domain.ModelSpec{
		Code: "validated-admin", Name: "Validated", Collection: definition.Name(),
		Fields:           []domain.ModelField{{Name: "code", Type: domain.FieldTypeString, Required: true, Editable: true, UIControl: domain.UIControlInput, ValidationRules: rules}},
		ProjectionFields: []string{"code"}, KeyFields: []string{"code"}, DefaultPageSize: 20, MaxPageSize: 100, ConfigRevision: 1,
	})
	if err != nil {
		t.Fatalf("CompileModel: %v", err)
	}
	if _, err := definition.NewRecord("production", map[string]string{"code": "UP"}); err == nil {
		t.Fatal("record violating regex and minimum length was accepted")
	}
	if _, err := definition.NewRecord("production", map[string]string{"code": "valid-code"}); err != nil {
		t.Fatalf("valid record: %v", err)
	}
	rules[0].Params["pattern"] = ".*"
	if model.Fields()[0].ValidationRules[0].Params["pattern"] != `^[a-z][a-z0-9-]+$` {
		t.Fatal("compiled model leaked validation rule mutation")
	}
	mismatch := model.Fields()
	mismatch[0].ValidationRules[1].Params["value"] = "4"
	if _, err := domain.CompileModel(definition, domain.ModelSpec{
		Code: "mismatch", Name: "Mismatch", Collection: definition.Name(), Fields: mismatch,
		ProjectionFields: []string{"code"}, KeyFields: []string{"code"}, DefaultPageSize: 20, MaxPageSize: 100, ConfigRevision: 1,
	}); err == nil {
		t.Fatal("model validation rules differing from collection compiled")
	}
}

func TestCompileModelRequiresClosedTypedAutoFillRules(t *testing.T) {
	t.Parallel()
	definition, err := domain.CompileCollection(domain.CollectionSpec{
		Name: "generated_records", KeyFields: []string{"id"}, SchemaVersion: 1,
		Fields: []domain.FieldDefinition{
			{Name: "id", DisplayName: "ID", Type: domain.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "created_at", DisplayName: "Created at", Type: domain.FieldTypeTimestamp, Required: true, DisplayOrder: 1},
			{Name: "created_by", DisplayName: "Created by", Type: domain.FieldTypeString, Required: true, DisplayOrder: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	base := domain.ModelSpec{
		Code: "generated-admin", Name: "Generated", Collection: definition.Name(),
		Fields: []domain.ModelField{
			{Name: "id", Type: domain.FieldTypeString, Required: true, UIControl: domain.UIControlInput},
			{Name: "created_at", Type: domain.FieldTypeTimestamp, Required: true, UIControl: domain.UIControlTime},
			{Name: "created_by", Type: domain.FieldTypeString, Required: true, UIControl: domain.UIControlInput},
		},
		ProjectionFields: []string{"id", "created_at", "created_by"}, KeyFields: []string{"id"}, DefaultPageSize: 20, MaxPageSize: 100, ConfigRevision: 1,
		AutoFillRules: []domain.AutoFillRule{
			{Field: "id", Source: domain.AutoFillUUID},
			{Field: "created_at", Source: domain.AutoFillCurrentTime},
			{Field: "created_by", Source: domain.AutoFillActorSubject},
		},
	}
	model, err := domain.CompileModel(definition, base)
	if err != nil {
		t.Fatalf("CompileModel: %v", err)
	}
	if got := model.AutoFillRules(); len(got) != 3 || got[0].Field != "created_at" || got[2].Source != domain.AutoFillUUID {
		t.Fatalf("auto-fill rules = %+v", got)
	}
	base.AutoFillRules[0].Source = domain.AutoFillCurrentTime
	if _, err := domain.CompileModel(definition, base); err == nil {
		t.Fatal("type-incompatible auto-fill rule compiled")
	}
	base.AutoFillRules[0].Source = domain.AutoFillSource("CALLER_VALUE")
	if _, err := domain.CompileModel(definition, base); err == nil {
		t.Fatal("open auto-fill source compiled")
	}
}

func sensitiveOptionCollection(t *testing.T) domain.CollectionDefinition {
	t.Helper()
	definition, err := domain.CompileCollection(domain.CollectionSpec{
		Name: "credentials", KeyFields: []string{"name"}, SchemaVersion: 1,
		Fields: []domain.FieldDefinition{
			{Name: "name", DisplayName: "Name", Type: domain.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "secret", DisplayName: "Secret", Type: domain.FieldTypeString, Required: true, Sensitive: true, DisplayOrder: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func ptr(value string) *string { return &value }

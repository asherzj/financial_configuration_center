package domain_test

import (
	"reflect"
	"testing"

	domain "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
)

func TestResolveSelectOptionsUsesEffectiveFilteredCollectionRecords(t *testing.T) {
	t.Parallel()
	definition, err := domain.CompileCollection(domain.CollectionSpec{
		Name: "providers", KeyFields: []string{"code", "region"}, SchemaVersion: 1,
		Fields: []domain.FieldDefinition{
			{Name: "code", DisplayName: "Code", Type: domain.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "region", DisplayName: "Region", Type: domain.FieldTypeString, Required: true, DisplayOrder: 1},
			{Name: "label", DisplayName: "Label", Type: domain.FieldTypeString, Required: true, DisplayOrder: 2},
			{Name: "enabled", DisplayName: "Enabled", Type: domain.FieldTypeBool, Required: true, DisplayOrder: 3},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	record := func(values map[string]string) domain.ConfigurationRecord {
		result, recordErr := definition.NewRecord("production", values)
		if recordErr != nil {
			t.Fatal(recordErr)
		}
		return result
	}
	options, err := domain.ResolveSelectOptions(domain.OptionSourceDefinition{
		Kind: domain.OptionSourceCollection, Collection: "providers", ValueField: "code", LabelField: "label",
		FixedFilters: []domain.OptionFixedFilter{{Field: "enabled", Value: "TRUE"}}, Limit: 10,
	}, definition, []domain.ConfigurationRecord{
		record(map[string]string{"code": "stripe", "region": "cn", "label": "Stripe", "enabled": "true"}),
		record(map[string]string{"code": "stripe", "region": "us", "label": "Stripe", "enabled": "true"}),
		record(map[string]string{"code": "legacy", "region": "cn", "label": "Legacy", "enabled": "false"}),
		record(map[string]string{"code": "adyen", "region": "cn", "label": "Adyen", "enabled": "true"}),
	})
	if err != nil {
		t.Fatalf("ResolveSelectOptions: %v", err)
	}
	want := []domain.SelectOptionDefinition{{Code: "adyen", Label: "Adyen"}, {Code: "stripe", Label: "Stripe"}}
	if !reflect.DeepEqual(options, want) {
		t.Fatalf("options = %#v, want %#v", options, want)
	}
}

func TestResolveSelectOptionsRejectsConflictingLabelsAndLimitOverflow(t *testing.T) {
	t.Parallel()
	definition, err := domain.CompileCollection(domain.CollectionSpec{
		Name: "providers", KeyFields: []string{"id"}, SchemaVersion: 1,
		Fields: []domain.FieldDefinition{
			{Name: "id", DisplayName: "ID", Type: domain.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "code", DisplayName: "Code", Type: domain.FieldTypeString, Required: true, DisplayOrder: 1},
			{Name: "label", DisplayName: "Label", Type: domain.FieldTypeString, Required: true, DisplayOrder: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	record := func(id, code, label string) domain.ConfigurationRecord {
		result, recordErr := definition.NewRecord("production", map[string]string{"id": id, "code": code, "label": label})
		if recordErr != nil {
			t.Fatal(recordErr)
		}
		return result
	}
	source := domain.OptionSourceDefinition{Kind: domain.OptionSourceCollection, Collection: "providers", ValueField: "code", LabelField: "label", Limit: 2}
	if _, err := domain.ResolveSelectOptions(source, definition, []domain.ConfigurationRecord{record("1", "stripe", "Stripe"), record("2", "stripe", "Stripe CN")}); err == nil {
		t.Fatal("conflicting option labels were accepted")
	}
	source.Limit = 1
	if _, err := domain.ResolveSelectOptions(source, definition, []domain.ConfigurationRecord{record("1", "stripe", "Stripe"), record("2", "adyen", "Adyen")}); err == nil {
		t.Fatal("option source limit overflow was accepted")
	}
}

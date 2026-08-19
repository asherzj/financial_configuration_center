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
	diagnostics := manager.Diagnostics()
	if diagnostics.LastErrorCode != "SNAPSHOT_REFRESH_FAILED" || diagnostics.Identity.Generation != 1 || len(diagnostics.Collections) != 1 {
		t.Fatalf("failure diagnostics = %+v", diagnostics)
	}
}

func TestRefreshRetainsFailedOptionDependencyGroupAndPublishesIndependentCollection(t *testing.T) {
	t.Parallel()
	inputs := dependencyInputs(t, 7)
	source := &partialStubSource{load: snapshot.EnvironmentLoad{Inputs: inputs}}
	manager, err := snapshot.NewManager(source, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance"}, fixedClock{now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), "production"); err != nil {
		t.Fatal(err)
	}

	updated := dependencyInputs(t, 8)
	source.load = snapshot.EnvironmentLoad{
		Inputs:   []snapshot.CollectionInput{updated[0], updated[2]},
		Failures: map[string]error{"providers": errors.New("provider rows are invalid")},
	}
	result, err := manager.Refresh(context.Background(), "production")
	if err != nil {
		t.Fatal(err)
	}
	if result.Generation != 2 || result.CollectionCount != 3 || len(result.FailedGroups) != 1 {
		t.Fatalf("partial refresh result = %+v", result)
	}
	if got := result.FailedGroups[0].Collections; len(got) != 2 || got[0] != "payment_routes" || got[1] != "providers" {
		t.Fatalf("failed dependency group = %v", got)
	}
	diagnostics := manager.Diagnostics()
	if diagnostics.LastErrorCode != "" || len(diagnostics.FailedDependencyGroups) != 1 || len(diagnostics.FailedDependencyGroups[0]) != 2 || diagnostics.FailedDependencyGroups[0][0] != "payment_routes" {
		t.Fatalf("partial diagnostics = %+v", diagnostics)
	}
	current := manager.Current()
	if revision, _ := current.CollectionVersion("payment_routes"); revision != 7 {
		t.Fatalf("consumer revision = %d, want last-known-good 7", revision)
	}
	if revision, _ := current.CollectionVersion("providers"); revision != 7 {
		t.Fatalf("provider revision = %d, want last-known-good 7", revision)
	}
	if revision, _ := current.CollectionVersion("feature_flags"); revision != 8 {
		t.Fatalf("independent revision = %d, want 8", revision)
	}
}

func TestRefreshBuildFailureRetainsDependencyGroupAndAllFailureDoesNotPublish(t *testing.T) {
	t.Parallel()
	inputs := dependencyInputs(t, 7)
	source := &partialStubSource{load: snapshot.EnvironmentLoad{Inputs: inputs}}
	manager, err := snapshot.NewManager(source, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance"}, fixedClock{now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), "production"); err != nil {
		t.Fatal(err)
	}

	updated := dependencyInputs(t, 8)
	updated[1].Records = []catalog.ConfigurationRecord{{
		Collection: "providers", Environment: "production", RecordKey: "not-canonical",
		Data: map[string]string{"code": "visa", "label": "Visa"}, ConfigRevision: 8,
	}}
	source.load = snapshot.EnvironmentLoad{Inputs: updated}
	result, err := manager.Refresh(context.Background(), "production")
	if err != nil || result.Generation != 2 || len(result.FailedGroups) != 1 {
		t.Fatalf("build partial refresh = %+v, %v", result, err)
	}
	if revision, _ := manager.Current().CollectionVersion("feature_flags"); revision != 8 {
		t.Fatalf("independent revision = %d, want 8", revision)
	}

	lastKnownGood := manager.Current()
	source.load = snapshot.EnvironmentLoad{Failures: map[string]error{
		"payment_routes": errors.New("routes unavailable"),
		"providers":      errors.New("providers unavailable"),
		"feature_flags":  errors.New("flags unavailable"),
	}}
	if _, err := manager.Refresh(context.Background(), "production"); err == nil {
		t.Fatal("all-failed refresh succeeded")
	}
	if manager.Current() != lastKnownGood || manager.Current().Identity().Generation != 2 {
		t.Fatal("all-failed refresh replaced last-known-good")
	}
}

type stubSource struct {
	collections []snapshot.CollectionInput
	err         error
}

type partialStubSource struct {
	load snapshot.EnvironmentLoad
	err  error
}

func (source *partialStubSource) LoadEnvironment(context.Context, string) ([]snapshot.CollectionInput, error) {
	if source.err != nil {
		return nil, source.err
	}
	return source.load.Inputs, nil
}

func (source *partialStubSource) LoadEnvironmentPartial(context.Context, string) (snapshot.EnvironmentLoad, error) {
	return source.load, source.err
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

func dependencyInputs(t *testing.T, revision catalog.ConfigRevision) []snapshot.CollectionInput {
	t.Helper()
	providers, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: "providers", KeyFields: []string{"code"}, SchemaVersion: 1,
		Fields: []catalog.FieldDefinition{
			{Name: "code", DisplayName: "Code", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "label", DisplayName: "Label", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	routes, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: "payment_routes", KeyFields: []string{"route_code"}, SchemaVersion: 1,
		Fields: []catalog.FieldDefinition{
			{Name: "route_code", DisplayName: "Route", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "provider", DisplayName: "Provider", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	model, err := catalog.CompileModel(routes, catalog.ModelSpec{
		Code: "payment-route-admin", Name: "Payment routes", Collection: routes.Name(), ConfigRevision: revision,
		Fields: []catalog.ModelField{
			{Name: "route_code", Type: catalog.FieldTypeString, Required: true, Editable: true, UIControl: catalog.UIControlInput},
			{Name: "provider", Type: catalog.FieldTypeString, Required: true, Editable: true, UIControl: catalog.UIControlSelect, OptionSource: &catalog.OptionSourceDefinition{
				Kind: catalog.OptionSourceCollection, Collection: providers.Name(), ValueField: "code", LabelField: "label", Limit: 100,
			}},
		},
		ProjectionFields: []string{"route_code", "provider"}, KeyFields: []string{"route_code"}, DefaultPageSize: 20, MaxPageSize: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	flags, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: "feature_flags", KeyFields: []string{"name"}, SchemaVersion: 1,
		Fields: []catalog.FieldDefinition{{Name: "name", DisplayName: "Name", Type: catalog.FieldTypeString, Required: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return []snapshot.CollectionInput{
		{Definition: routes, Models: []catalog.CompiledModel{model}, Version: revision},
		{Definition: providers, Version: revision},
		{Definition: flags, Version: revision},
	}
}

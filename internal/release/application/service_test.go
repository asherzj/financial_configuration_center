package application_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/release/application"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
)

func TestBaseFinalApplicationIsTheOnlyRecordWritePath(t *testing.T) {
	t.Parallel()

	definition, model := compiledCatalog(t)
	store := newFakeUnitOfWork(definition, model)
	service := application.NewService(store, &sequenceIDs{values: []string{"order-1", "item-1"}}, fixedClock{now: time.Date(2026, 8, 19, 3, 0, 0, 0, time.UTC)})

	created, err := service.CreateBaseFinal(context.Background(), application.CreateBaseFinalCommand{
		IdempotencyKey: "create-request-1",
		ModelCode:      model.Code(),
		Scope:          release.Scope{Region: "cn", Environment: "production"},
		Actor:          "operator@example.com",
		Items: []application.AddDraft{{
			Data:                       map[string]string{"route_code": "visa-cn", "priority": "+0007"},
			ExpectedRecordRevision:     0,
			ExpectedCollectionRevision: 7,
		}},
	})
	if err != nil {
		t.Fatalf("CreateBaseFinal: %v", err)
	}
	if created.Status != release.OrderInProgress || created.CurrentStep != release.StepBaseApply || created.Revision != 1 {
		t.Fatalf("created order = %+v", created)
	}
	if len(store.records["production"]) != 0 {
		t.Fatal("creating a ReleaseOrder directly changed configuration")
	}

	executed, err := service.Act(context.Background(), application.ActCommand{
		OrderID:          created.ID,
		ActionRequestID:  "execute-request-1",
		ExpectedRevision: 1,
		Action:           application.ActionExecute,
		Actor:            "operator@example.com",
	})
	if err != nil {
		t.Fatalf("execute BASE_APPLY: %v", err)
	}
	if executed.Revision != 2 || store.revisions["production"] != 8 || store.outboxEvents != 1 {
		t.Fatalf("base apply did not atomically advance facts: order=%+v version=%d outbox=%d", executed, store.revisions["production"], store.outboxEvents)
	}
	if len(store.records["production"]) != 1 || len(store.records["staging"]) != 0 {
		t.Fatalf("environment isolation failed: production=%#v staging=%#v", store.records["production"], store.records["staging"])
	}

	advanced, err := service.Act(context.Background(), application.ActCommand{
		OrderID: created.ID, ActionRequestID: "advance-request-1", ExpectedRevision: 2,
		Action: application.ActionAdvance, Actor: "operator@example.com",
	})
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	completed, err := service.Act(context.Background(), application.ActCommand{
		OrderID: created.ID, ActionRequestID: "complete-request-1", ExpectedRevision: advanced.Revision,
		Action: application.ActionExecute, Actor: "operator@example.com",
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if completed.Status != release.OrderSucceeded || completed.Revision != 4 {
		t.Fatalf("completed order = %+v", completed)
	}
}

func TestCreateBaseFinalRejectsStalePageRevision(t *testing.T) {
	t.Parallel()

	definition, model := compiledCatalog(t)
	store := newFakeUnitOfWork(definition, model)
	service := application.NewService(store, &sequenceIDs{values: []string{"order", "item"}}, fixedClock{now: time.Now().UTC()})
	_, err := service.CreateBaseFinal(context.Background(), application.CreateBaseFinalCommand{
		IdempotencyKey: "request", ModelCode: model.Code(), Scope: release.Scope{Region: "cn", Environment: "production"}, Actor: "actor",
		Items: []application.AddDraft{{Data: map[string]string{"route_code": "visa", "priority": "1"}, ExpectedCollectionRevision: 6}},
	})
	if !errors.Is(err, release.ErrAborted) {
		t.Fatalf("CreateBaseFinal stale error = %v, want ErrAborted", err)
	}
	if len(store.orders) != 0 {
		t.Fatal("stale create persisted an order")
	}
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type sequenceIDs struct {
	values []string
	next   int
}

func (ids *sequenceIDs) NewID() string {
	value := ids.values[ids.next]
	ids.next++
	return value
}

func (ids *sequenceIDs) NewReleaseNumber(time.Time) string { return "REL-20260819-0001" }

type fakeUnitOfWork struct {
	definition   catalog.CollectionDefinition
	model        catalog.CompiledModel
	revisions    map[string]catalog.ConfigRevision
	records      map[string]map[string]catalog.ConfigurationRecord
	orders       map[string]*release.Order
	global       catalog.ConfigRevision
	outboxEvents int
}

func newFakeUnitOfWork(definition catalog.CollectionDefinition, model catalog.CompiledModel) *fakeUnitOfWork {
	return &fakeUnitOfWork{
		definition: definition,
		model:      model,
		revisions:  map[string]catalog.ConfigRevision{"production": 7, "staging": 7},
		records: map[string]map[string]catalog.ConfigurationRecord{
			"production": {},
			"staging":    {},
		},
		orders: make(map[string]*release.Order),
		global: 7,
	}
}

func (store *fakeUnitOfWork) WithinTransaction(ctx context.Context, work func(application.Transaction) error) error {
	return work((*fakeTransaction)(store))
}

type fakeTransaction fakeUnitOfWork

func (transaction *fakeTransaction) LoadCatalog(_ context.Context, modelCode string) (application.CatalogBundle, error) {
	if modelCode != transaction.model.Code() {
		return application.CatalogBundle{}, fmt.Errorf("model not found")
	}
	return application.CatalogBundle{Definition: transaction.definition, Model: transaction.model, Template: application.TemplateRef{Code: "base-final", Version: 1, ReleaseTypeCode: "direct"}}, nil
}

func (transaction *fakeTransaction) LoadBaseAuthority(_ context.Context, collection, environment string, recordKeys []string) (release.BaseAuthority, error) {
	authority := release.BaseAuthority{CollectionRevision: transaction.revisions[environment], Records: make(map[string]*catalog.ConfigurationRecord, len(recordKeys))}
	for _, key := range recordKeys {
		record, exists := transaction.records[environment][key]
		if exists {
			copy := record
			copy.Data = cloneMap(record.Data)
			authority.Records[key] = &copy
		}
	}
	return authority, nil
}

func (transaction *fakeTransaction) InsertOrder(_ context.Context, order *release.Order) error {
	if _, exists := transaction.orders[order.ID()]; exists {
		return fmt.Errorf("duplicate order")
	}
	transaction.orders[order.ID()] = order.Clone()
	return nil
}

func (transaction *fakeTransaction) LoadOrderForUpdate(_ context.Context, orderID string) (*release.Order, error) {
	order, exists := transaction.orders[orderID]
	if !exists {
		return nil, fmt.Errorf("order not found")
	}
	return order.Clone(), nil
}

func (transaction *fakeTransaction) AllocateConfigRevision(context.Context) (catalog.ConfigRevision, error) {
	transaction.global++
	return transaction.global, nil
}

func (transaction *fakeTransaction) ApplyBaseEffect(_ context.Context, orderID string, effect release.BaseEffect, revision catalog.ConfigRevision) error {
	for _, change := range effect.Changes {
		record := change.After
		record.ConfigRevision = revision
		record.Data = cloneMap(record.Data)
		transaction.records[effect.Environment][record.RecordKey] = record
	}
	transaction.revisions[effect.Environment] = revision
	transaction.outboxEvents++
	return nil
}

func (transaction *fakeTransaction) SaveOrder(_ context.Context, order *release.Order) error {
	transaction.orders[order.ID()] = order.Clone()
	return nil
}

func compiledCatalog(t *testing.T) (catalog.CollectionDefinition, catalog.CompiledModel) {
	t.Helper()
	defaultEnabled := "false"
	definition, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: "payment_routes", Description: "routes", SDKDeliveryEnabled: true, SchemaVersion: 1,
		Fields: []catalog.FieldDefinition{
			{Name: "route_code", DisplayName: "Route code", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "priority", DisplayName: "Priority", Type: catalog.FieldTypeInt64, Required: true, DisplayOrder: 1},
			{Name: "enabled", DisplayName: "Enabled", Type: catalog.FieldTypeBool, Required: true, DefaultValue: &defaultEnabled, DisplayOrder: 2},
		},
		KeyFields: []string{"route_code"},
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

func cloneMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

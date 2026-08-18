package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
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
		OrderID:             created.ID,
		ActionRequestID:     "execute-request-1",
		ExpectedRevision:    1,
		ExpectedCurrentStep: "base-apply",
		Action:              application.ActionExecute,
		Actor:               "operator@example.com",
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
		OrderID: created.ID, ActionRequestID: "advance-request-1", ExpectedRevision: 2, ExpectedCurrentStep: "base-apply",
		Action: application.ActionAdvance, Actor: "operator@example.com",
	})
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	completed, err := service.Act(context.Background(), application.ActCommand{
		OrderID: created.ID, ActionRequestID: "complete-request-1", ExpectedRevision: advanced.Revision, ExpectedCurrentStep: "complete",
		Action: application.ActionExecute, Actor: "operator@example.com",
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if completed.Status != release.OrderSucceeded || completed.Revision != 4 {
		t.Fatalf("completed order = %+v", completed)
	}
}

func TestOverlayFinalApplicationAppliesAndRollsBackAtomically(t *testing.T) {
	t.Parallel()
	definition, model := compiledCatalog(t)
	store := newFakeUnitOfWork(definition, model)
	template, err := release.CompileTemplate([]byte(`{"steps":[
		{"code":"apply-overlay","type":"OVERLAY_APPLY","params":{}},
		{"code":"complete","type":"COMPLETE","params":{}}
	]}`), release.FinalEffectOverlay)
	if err != nil {
		t.Fatal(err)
	}
	store.template = application.TemplateRef{Code: "overlay-final", Version: 1, ReleaseTypeCode: "scope", Definition: template}
	base, err := definition.NewRecord("production", map[string]string{"route_code": "visa", "priority": "1"})
	if err != nil {
		t.Fatal(err)
	}
	base.ConfigRevision = 5
	store.records["production"][base.RecordKey] = base
	now := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	service := application.NewService(store, &sequenceIDs{values: []string{"order-overlay", "item-overlay"}}, fixedClock{now: now})

	created, err := service.CreateRelease(context.Background(), application.CreateReleaseCommand{
		IdempotencyKey: "create-overlay", ModelCode: model.Code(), ReleaseTypeCode: "scope",
		Scope: release.Scope{Region: "cn", Environment: "production", Stage: "blue"}, Actor: "operator",
		Items: []application.ReleaseDraft{{
			Action: release.ChangeModify, BaseBefore: base.Data, EffectiveBefore: base.Data,
			After:                  map[string]string{"route_code": "visa", "priority": "2", "enabled": "false"},
			ExpectedRecordRevision: 5, ExpectedCollectionRevision: 7,
		}},
	})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	if created.CurrentStep != release.StepOverlayApply || len(store.overlays["production"]) != 0 {
		t.Fatalf("create changed overlay state: view=%+v overlays=%#v", created, store.overlays)
	}

	executed, err := service.Act(context.Background(), application.ActCommand{
		OrderID: created.ID, ActionRequestID: "execute-overlay", ExpectedRevision: 1,
		ExpectedCurrentStep: "apply-overlay", Action: application.ActionExecute, Actor: "operator",
	})
	if err != nil {
		t.Fatalf("execute overlay: %v", err)
	}
	rule := store.overlays["production"]["blue"][base.RecordKey]
	if rule == nil || rule.Content["priority"] != "2" || store.revisions["production"] != 8 || store.outboxEvents != 1 {
		t.Fatalf("overlay apply facts: rule=%#v revision=%d outbox=%d", rule, store.revisions["production"], store.outboxEvents)
	}

	rolledBack, err := service.Act(context.Background(), application.ActCommand{
		OrderID: created.ID, ActionRequestID: "rollback-overlay", ExpectedRevision: executed.Revision,
		ExpectedCurrentStep: "apply-overlay", Action: application.ActionRollback, Actor: "operator",
	})
	if err != nil {
		t.Fatalf("rollback overlay: %v", err)
	}
	if rolledBack.Status != release.OrderRolledBack || store.overlays["production"]["blue"][base.RecordKey] != nil || store.revisions["production"] != 9 || store.outboxEvents != 2 {
		t.Fatalf("rollback facts: view=%+v overlays=%#v revision=%d outbox=%d", rolledBack, store.overlays, store.revisions["production"], store.outboxEvents)
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

func TestCreateAndActionRequestsAreReplaySafe(t *testing.T) {
	t.Parallel()

	definition, model := compiledCatalog(t)
	store := newFakeUnitOfWork(definition, model)
	service := application.NewService(store, &sequenceIDs{values: []string{"order-1", "item-1", "order-2", "item-2"}}, fixedClock{now: time.Now().UTC()})
	create := application.CreateBaseFinalCommand{
		IdempotencyKey: "create-request", ModelCode: model.Code(), Scope: release.Scope{Region: "cn", Environment: "production"}, Actor: "actor",
		Items: []application.AddDraft{{Data: map[string]string{"route_code": "visa", "priority": "1"}, ExpectedCollectionRevision: 7}},
	}
	created, err := service.CreateBaseFinal(context.Background(), create)
	if err != nil {
		t.Fatal(err)
	}
	replayedCreate, err := service.CreateBaseFinal(context.Background(), create)
	if err != nil {
		t.Fatalf("replay create: %v", err)
	}
	if !reflect.DeepEqual(replayedCreate, created) || len(store.orders) != 1 {
		t.Fatalf("create replay = %+v, orders = %d; want %+v, 1", replayedCreate, len(store.orders), created)
	}
	changedCreate := create
	changedCreate.Items = []application.AddDraft{{Data: map[string]string{"route_code": "visa", "priority": "2"}, ExpectedCollectionRevision: 7}}
	if _, err := service.CreateBaseFinal(context.Background(), changedCreate); !errors.Is(err, release.ErrIdempotencyKeyReused) {
		t.Fatalf("changed request with reused create key = %v, want ErrIdempotencyKeyReused", err)
	}

	action := application.ActCommand{OrderID: created.ID, ActionRequestID: "action-request", ExpectedRevision: 1, ExpectedCurrentStep: "base-apply", Action: application.ActionExecute, Actor: "actor"}
	executed, err := service.Act(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	replayedAction, err := service.Act(context.Background(), action)
	if err != nil {
		t.Fatalf("replay action: %v", err)
	}
	if !reflect.DeepEqual(replayedAction, executed) || store.outboxEvents != 1 {
		t.Fatalf("action replay = %+v, outbox = %d; want %+v, 1", replayedAction, store.outboxEvents, executed)
	}

	action.Action = application.ActionAdvance
	if _, err := service.Act(context.Background(), action); !errors.Is(err, release.ErrIdempotencyKeyReused) {
		t.Fatalf("changed request with reused action ID = %v, want ErrIdempotencyKeyReused", err)
	}
	action.ActionRequestID = "new-stale-action"
	action.Action = application.ActionExecute
	if _, err := service.Act(context.Background(), action); !errors.Is(err, release.ErrAborted) {
		t.Fatalf("new action with stale authority = %v, want ErrAborted", err)
	}
}

func TestManualApprovalApplicationJourney(t *testing.T) {
	t.Parallel()
	definition, model := compiledCatalog(t)
	store := newFakeUnitOfWork(definition, model)
	compiled, err := release.CompileTemplate([]byte(`{"steps":[
		{"code":"review","type":"MANUAL_REVIEW","requiredRoles":["RELEASE_APPROVER"],"params":{"selfApprovalPolicy":"DENY_PRODUCTION"}},
		{"code":"apply","type":"BASE_APPLY","params":{"cleanupScopeOverlay":true}},
		{"code":"complete","type":"COMPLETE","params":{}}
	]}`), release.FinalEffectBase)
	if err != nil {
		t.Fatal(err)
	}
	store.template = application.TemplateRef{Code: "approval", Version: 1, ReleaseTypeCode: "approval", Definition: compiled}
	service := application.NewService(store, &sequenceIDs{values: []string{"order", "item"}}, fixedClock{now: time.Now().UTC()})
	created, err := service.CreateBaseFinal(context.Background(), application.CreateBaseFinalCommand{
		IdempotencyKey: "create", ModelCode: model.Code(), ReleaseTypeCode: "approval", Scope: release.Scope{Region: "cn", Environment: "production"}, Actor: "creator",
		Items: []application.AddDraft{{Data: map[string]string{"route_code": "visa", "priority": "1"}, ExpectedCollectionRevision: 7}},
	})
	if err != nil || created.CurrentStep != release.StepManualReview {
		t.Fatalf("create = %+v, %v", created, err)
	}
	if !created.CanExecute || created.CanApprove || created.CanReject || created.CanAdvance {
		t.Fatalf("create capabilities = %+v", created)
	}
	acted, err := service.Act(context.Background(), application.ActCommand{OrderID: created.ID, ActionRequestID: "submit", ExpectedRevision: 1, ExpectedCurrentStep: "review", Action: application.ActionExecute, Actor: "creator"})
	if err != nil || acted.Revision != 2 {
		t.Fatalf("submit = %+v, %v", acted, err)
	}
	if acted.CanExecute || !acted.CanApprove || !acted.CanReject || acted.CanAdvance {
		t.Fatalf("review capabilities = %+v", acted)
	}
	if _, err := service.Act(context.Background(), application.ActCommand{OrderID: created.ID, ActionRequestID: "self-approve", ExpectedRevision: 2, ExpectedCurrentStep: "review", Action: application.ActionApprove, Actor: "creator", Roles: []string{"RELEASE_APPROVER"}}); !errors.Is(err, release.ErrForbidden) {
		t.Fatalf("self approve = %v", err)
	}
	approved, err := service.Act(context.Background(), application.ActCommand{OrderID: created.ID, ActionRequestID: "approve", ExpectedRevision: 2, ExpectedCurrentStep: "review", Action: application.ActionApprove, Actor: "approver", Roles: []string{"RELEASE_APPROVER"}, Comment: "approved"})
	if err != nil || approved.Revision != 3 {
		t.Fatalf("approve = %+v, %v", approved, err)
	}
	if !approved.CanAdvance || approved.CanApprove || approved.CanReject {
		t.Fatalf("approved capabilities = %+v", approved)
	}
	advanced, err := service.Act(context.Background(), application.ActCommand{OrderID: created.ID, ActionRequestID: "advance-review", ExpectedRevision: 3, ExpectedCurrentStep: "review", Action: application.ActionAdvance, Actor: "operator"})
	if err != nil || advanced.CurrentStep != release.StepBaseApply {
		t.Fatalf("advance review = %+v, %v", advanced, err)
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
	definition    catalog.CollectionDefinition
	model         catalog.CompiledModel
	revisions     map[string]catalog.ConfigRevision
	records       map[string]map[string]catalog.ConfigurationRecord
	overlays      map[string]map[string]map[string]*overlay.Rule
	orders        map[string]*release.Order
	createResults map[string]application.StoredRequestResult
	actionResults map[string]application.StoredRequestResult
	template      application.TemplateRef
	global        catalog.ConfigRevision
	outboxEvents  int
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
		overlays: map[string]map[string]map[string]*overlay.Rule{
			"production": {},
			"staging":    {},
		},
		orders:        make(map[string]*release.Order),
		createResults: make(map[string]application.StoredRequestResult),
		actionResults: make(map[string]application.StoredRequestResult),
		global:        7,
	}
}

func (store *fakeUnitOfWork) WithinTransaction(ctx context.Context, work func(application.Transaction) error) error {
	return work((*fakeTransaction)(store))
}

type fakeTransaction fakeUnitOfWork

func (transaction *fakeTransaction) LoadCatalog(_ context.Context, modelCode, releaseTypeCode string) (application.CatalogBundle, error) {
	if modelCode != transaction.model.Code() {
		return application.CatalogBundle{}, fmt.Errorf("model not found")
	}
	template := transaction.template
	if template.Code == "" {
		template = application.TemplateRef{Code: "base-final", Version: 1, ReleaseTypeCode: releaseTypeCode}
	}
	return application.CatalogBundle{Definition: transaction.definition, Model: transaction.model, Template: template}, nil
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

func (transaction *fakeTransaction) LoadOverlayRules(_ context.Context, collection string, scope release.Scope, recordKeys []string) ([]overlay.Rule, error) {
	allowed := make(map[string]struct{}, len(recordKeys))
	for _, key := range recordKeys {
		allowed[key] = struct{}{}
	}
	rules := make([]overlay.Rule, 0)
	for _, stage := range []string{"", scope.Stage} {
		for key, rule := range transaction.overlays[scope.Environment][stage] {
			if rule == nil || rule.Collection != collection || rule.Scope.Region != scope.Region {
				continue
			}
			if _, exists := allowed[key]; !exists {
				continue
			}
			rules = append(rules, cloneOverlayRule(*rule))
		}
	}
	return rules, nil
}

func (transaction *fakeTransaction) FindCreateResult(_ context.Context, actor, idempotencyKey string) (application.StoredRequestResult, bool, error) {
	result, found := transaction.createResults[actor+"\x00"+idempotencyKey]
	return result, found, nil
}

func (transaction *fakeTransaction) InsertOrder(_ context.Context, order *release.Order) error {
	if _, exists := transaction.orders[order.ID()]; exists {
		return fmt.Errorf("duplicate order")
	}
	transaction.orders[order.ID()] = order.Clone()
	state := order.State()
	step := state.Steps[state.CurrentStep]
	steps := make([]application.StepView, len(state.Steps))
	for index, stateStep := range state.Steps {
		steps[index] = application.StepView{Code: stateStep.Code, Type: stateStep.Type, Status: stateStep.Status}
	}
	transaction.createResults[state.CreatedBy+"\x00"+state.IdempotencyKey] = application.StoredRequestResult{
		RequestDigest: state.RequestDigest,
		Result: application.OrderView{
			ID: state.ID, Status: state.Status, CurrentStepCode: step.Code, CurrentStep: step.Type, CurrentStepStatus: step.Status,
			Revision: state.Revision, CanExecute: step.Status == release.StepPending, Steps: steps,
		},
	}
	return nil
}

func (transaction *fakeTransaction) LoadOrderForUpdate(_ context.Context, orderID string) (*release.Order, error) {
	order, exists := transaction.orders[orderID]
	if !exists {
		return nil, fmt.Errorf("order not found")
	}
	return order.Clone(), nil
}

func (transaction *fakeTransaction) FindActionResult(_ context.Context, orderID, actionRequestID string) (application.StoredRequestResult, bool, error) {
	result, found := transaction.actionResults[orderID+"\x00"+actionRequestID]
	return result, found, nil
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

func (transaction *fakeTransaction) ApplyOverlayEffect(_ context.Context, _ string, effect release.OverlayEffect) error {
	if transaction.revisions[effect.Scope.Environment] != effect.PreviousRevision {
		return release.ErrAborted
	}
	stages := transaction.overlays[effect.Scope.Environment]
	if stages[effect.Scope.Stage] == nil {
		stages[effect.Scope.Stage] = make(map[string]*overlay.Rule)
	}
	for _, change := range effect.Changes {
		if change.NewRule == nil {
			delete(stages[effect.Scope.Stage], change.RecordKey)
			continue
		}
		cloned := cloneOverlayRule(*change.NewRule)
		stages[effect.Scope.Stage][change.RecordKey] = &cloned
	}
	transaction.revisions[effect.Scope.Environment] = effect.AppliedRevision
	transaction.outboxEvents++
	return nil
}

func (transaction *fakeTransaction) SaveOrder(_ context.Context, order *release.Order) error {
	transaction.orders[order.ID()] = order.Clone()
	return nil
}

func (transaction *fakeTransaction) RecordAction(context.Context, application.ActionRecord) error {
	return nil
}

func (transaction *fakeTransaction) InsertActionResult(_ context.Context, orderID, actionRequestID, requestDigest string, result application.OrderView, _ time.Time) error {
	transaction.actionResults[orderID+"\x00"+actionRequestID] = application.StoredRequestResult{RequestDigest: requestDigest, Result: result}
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

func cloneOverlayRule(source overlay.Rule) overlay.Rule {
	encoded, _ := json.Marshal(source)
	var cloned overlay.Rule
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

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

func TestCreateBaseFinalGeneratesAutoFillAfterReplayCheck(t *testing.T) {
	t.Parallel()
	definition, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: "generated", KeyFields: []string{"id"}, SchemaVersion: 1,
		Fields: []catalog.FieldDefinition{
			{Name: "id", DisplayName: "ID", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "owner", DisplayName: "Owner", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 1},
			{Name: "display", DisplayName: "Display", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 2},
			{Name: "created_at", DisplayName: "Created at", Type: catalog.FieldTypeTimestamp, Required: true, DisplayOrder: 3},
			{Name: "source", DisplayName: "Source", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 4},
			{Name: "payload", DisplayName: "Payload", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 5},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	model, err := catalog.CompileModel(definition, catalog.ModelSpec{
		Code: "generated-admin", Name: "Generated", Collection: definition.Name(),
		Fields: []catalog.ModelField{
			{Name: "id", Type: catalog.FieldTypeString, Required: true, UIControl: catalog.UIControlInput},
			{Name: "owner", Type: catalog.FieldTypeString, Required: true, UIControl: catalog.UIControlInput},
			{Name: "display", Type: catalog.FieldTypeString, Required: true, UIControl: catalog.UIControlInput},
			{Name: "created_at", Type: catalog.FieldTypeTimestamp, Required: true, UIControl: catalog.UIControlTime},
			{Name: "source", Type: catalog.FieldTypeString, Required: true, UIControl: catalog.UIControlInput},
			{Name: "payload", Type: catalog.FieldTypeString, Required: true, Editable: true, UIControl: catalog.UIControlInput},
		},
		ProjectionFields: []string{"id", "owner", "display", "created_at", "source", "payload"}, KeyFields: []string{"id"}, DefaultPageSize: 20, MaxPageSize: 100, ConfigRevision: 7,
		AutoFillRules: []catalog.AutoFillRule{
			{Field: "id", Source: catalog.AutoFillUUID},
			{Field: "owner", Source: catalog.AutoFillActorSubject},
			{Field: "display", Source: catalog.AutoFillActorName},
			{Field: "created_at", Source: catalog.AutoFillCurrentTime},
			{Field: "source", Source: catalog.AutoFillConstant, Value: "admin"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeUnitOfWork(definition, model)
	ids := &sequenceIDs{values: []string{"generated-id", "order", "item"}}
	createdAt := time.Date(2026, 8, 19, 22, 30, 0, 123, time.UTC)
	service := application.NewService(store, ids, fixedClock{now: createdAt})
	command := application.CreateBaseFinalCommand{
		IdempotencyKey: "auto-fill", ModelCode: model.Code(), Scope: release.Scope{Region: "cn", Environment: "production"},
		Actor: "operator@example.com", ActorName: "Operator", Items: []application.AddDraft{{
			Data: map[string]string{
				"id": "caller-id", "owner": "caller-owner", "display": "Caller", "created_at": "2000-01-01T00:00:00Z", "source": "caller", "payload": "kept",
			},
			ExpectedCollectionRevision: 7,
		}},
	}
	created, err := service.CreateBaseFinal(context.Background(), command)
	if err != nil {
		t.Fatalf("CreateBaseFinal: %v", err)
	}
	item := store.orders[created.ID].Items()[0]
	want := map[string]string{
		"id": "generated-id", "owner": "operator@example.com", "display": "Operator",
		"created_at": createdAt.Format(time.RFC3339Nano), "source": "admin", "payload": "kept",
	}
	if !reflect.DeepEqual(item.After.Data, want) {
		t.Fatalf("auto-filled after = %#v, want %#v", item.After.Data, want)
	}
	replay := command
	replay.Items = []application.AddDraft{{Data: map[string]string{
		"id": "another-spoof", "owner": "another-spoof", "display": "Spoof", "created_at": "2030-01-01T00:00:00Z", "source": "spoof", "payload": "kept",
	}, ExpectedCollectionRevision: 7}}
	replayed, err := service.CreateBaseFinal(context.Background(), replay)
	if err != nil || !reflect.DeepEqual(replayed, created) || ids.next != 3 {
		t.Fatalf("auto-fill replay = %+v, error=%v, generated IDs=%d", replayed, err, ids.next)
	}
}

func TestCreateOverlayPreservesAutoFilledKeyAndRegeneratesOtherFields(t *testing.T) {
	t.Parallel()
	definition, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: "owned", KeyFields: []string{"id"}, SchemaVersion: 1,
		Fields: []catalog.FieldDefinition{
			{Name: "id", DisplayName: "ID", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "owner", DisplayName: "Owner", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 1},
			{Name: "payload", DisplayName: "Payload", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	model, err := catalog.CompileModel(definition, catalog.ModelSpec{
		Code: "owned-admin", Name: "Owned", Collection: definition.Name(),
		Fields: []catalog.ModelField{
			{Name: "id", Type: catalog.FieldTypeString, Required: true, UIControl: catalog.UIControlInput},
			{Name: "owner", Type: catalog.FieldTypeString, Required: true, UIControl: catalog.UIControlInput},
			{Name: "payload", Type: catalog.FieldTypeString, Required: true, Editable: true, UIControl: catalog.UIControlInput},
		},
		ProjectionFields: []string{"id", "owner", "payload"}, KeyFields: []string{"id"}, DefaultPageSize: 20, MaxPageSize: 100, ConfigRevision: 7,
		AutoFillRules: []catalog.AutoFillRule{{Field: "id", Source: catalog.AutoFillUUID}, {Field: "owner", Source: catalog.AutoFillActorSubject}},
	})
	if err != nil {
		t.Fatal(err)
	}
	template, err := release.CompileTemplate([]byte(`{"steps":[{"code":"apply","type":"OVERLAY_APPLY","params":{}},{"code":"complete","type":"COMPLETE","params":{}}]}`), release.FinalEffectOverlay)
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeUnitOfWork(definition, model)
	store.template = application.TemplateRef{Code: "scope-final", Version: 1, ReleaseTypeCode: "scope", Definition: template}
	base, _ := definition.NewRecord("production", map[string]string{"id": "stable-id", "owner": "creator", "payload": "before"})
	base.ConfigRevision = 5
	store.records["production"][base.RecordKey] = base
	service := application.NewService(store, &sequenceIDs{values: []string{"item", "order"}}, fixedClock{now: time.Date(2026, 8, 19, 22, 45, 0, 0, time.UTC)})
	created, err := service.CreateRelease(context.Background(), application.CreateReleaseCommand{
		IdempotencyKey: "auto-modify", ModelCode: model.Code(), ReleaseTypeCode: "scope",
		Scope: release.Scope{Region: "cn", Environment: "production", Stage: "blue"}, Actor: "modifier",
		Items: []application.ReleaseDraft{{
			Action: release.ChangeModify, BaseBefore: base.Data, EffectiveBefore: base.Data,
			After:                  map[string]string{"id": "caller-tried-to-change", "owner": "spoof", "payload": "after"},
			ExpectedRecordRevision: 5, ExpectedCollectionRevision: 7,
		}},
	})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	after := store.orders[created.ID].Items()[0].After
	if after.RecordKey != base.RecordKey || after.Data["id"] != "stable-id" || after.Data["owner"] != "modifier" || after.Data["payload"] != "after" {
		t.Fatalf("auto-filled modify = %+v", after)
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

func TestBaseFinalApplicationModifiesDeletesAndRestoresScopedFacts(t *testing.T) {
	t.Parallel()
	definition, model := compiledCatalog(t)
	store := newFakeUnitOfWork(definition, model)
	template, err := release.CompileTemplate([]byte(`{"steps":[
		{"code":"apply-base","type":"BASE_APPLY","params":{"cleanupScopeOverlay":true}},
		{"code":"complete","type":"COMPLETE","params":{}}
	]}`), release.FinalEffectBase)
	if err != nil {
		t.Fatal(err)
	}
	store.template = application.TemplateRef{Code: "base-final", Version: 1, ReleaseTypeCode: "direct", Definition: template}
	modified, err := definition.NewRecord("production", map[string]string{"route_code": "visa", "priority": "1"})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := definition.NewRecord("production", map[string]string{"route_code": "amex", "priority": "2"})
	if err != nil {
		t.Fatal(err)
	}
	modified.ConfigRevision = 7
	deleted.ConfigRevision = 7
	store.records["production"][modified.RecordKey] = modified
	store.records["production"][deleted.RecordKey] = deleted
	activated := catalog.ConfigRevision(7)
	previousRule := &overlay.Rule{
		ID: "previous-rule", Collection: definition.Name(),
		Scope:     overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey: modified.RecordKey, Action: overlay.ActionModify,
		Content:        map[string]string{"route_code": "visa", "priority": "7", "enabled": "false"},
		ConfigRevision: 7, ReleaseOrderID: "previous-order", ActivatedRevision: &activated,
		CreatedAt: time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC), CreatedBy: "previous",
		UpdatedAt: time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC), UpdatedBy: "previous",
	}
	store.overlays["production"]["blue"] = map[string]*overlay.Rule{modified.RecordKey: previousRule}
	now := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	service := application.NewService(store, &sequenceIDs{values: []string{"base-order", "modify-item", "delete-item"}}, fixedClock{now: now})

	created, err := service.CreateRelease(context.Background(), application.CreateReleaseCommand{
		IdempotencyKey: "modify-delete", ModelCode: model.Code(), ReleaseTypeCode: "direct",
		Scope: release.Scope{Region: "cn", Environment: "production", Stage: "blue"}, Actor: "operator",
		Items: []application.ReleaseDraft{
			{
				Action: release.ChangeModify, BaseBefore: modified.Data,
				EffectiveBefore:        map[string]string{"route_code": "visa", "priority": "7", "enabled": "false"},
				After:                  map[string]string{"route_code": "visa", "priority": "2", "enabled": "false"},
				ExpectedRecordRevision: 7, ExpectedCollectionRevision: 7,
			},
			{
				Action: release.ChangeDelete, BaseBefore: deleted.Data, EffectiveBefore: deleted.Data,
				ExpectedRecordRevision: 7, ExpectedCollectionRevision: 7,
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	executed, err := service.Act(context.Background(), application.ActCommand{
		OrderID: created.ID, ActionRequestID: "execute-base", ExpectedRevision: created.Revision,
		ExpectedCurrentStep: "apply-base", Action: application.ActionExecute, Actor: "operator",
	})
	if err != nil {
		t.Fatalf("execute base: %v", err)
	}
	current := store.records["production"][modified.RecordKey]
	if current.Data["priority"] != "2" || current.ConfigRevision != 8 {
		t.Fatalf("modified base = %+v", current)
	}
	if _, exists := store.records["production"][deleted.RecordKey]; exists || store.overlays["production"]["blue"][modified.RecordKey] != nil {
		t.Fatalf("base apply did not delete the target and scoped overlay")
	}

	rolledBack, err := service.Act(context.Background(), application.ActCommand{
		OrderID: created.ID, ActionRequestID: "rollback-base", ExpectedRevision: executed.Revision,
		ExpectedCurrentStep: "apply-base", Action: application.ActionRollback, Actor: "operator",
	})
	if err != nil {
		t.Fatalf("rollback base: %v", err)
	}
	restoredModified := store.records["production"][modified.RecordKey]
	restoredDeleted := store.records["production"][deleted.RecordKey]
	restoredRule := store.overlays["production"]["blue"][modified.RecordKey]
	if rolledBack.Status != release.OrderRolledBack || !reflect.DeepEqual(restoredModified.Data, modified.Data) || !reflect.DeepEqual(restoredDeleted.Data, deleted.Data) || !reflect.DeepEqual(restoredRule, previousRule) {
		t.Fatalf("rollback did not restore exact facts: view=%+v modified=%+v deleted=%+v rule=%+v", rolledBack, restoredModified, restoredDeleted, restoredRule)
	}
	if store.revisions["production"] != 9 || store.outboxEvents != 2 {
		t.Fatalf("rollback revision=%d outbox=%d", store.revisions["production"], store.outboxEvents)
	}
}

func TestPercentageRolloutApplicationPersistsTemporaryRule(t *testing.T) {
	t.Parallel()
	definition, model := compiledCatalog(t)
	store := newFakeUnitOfWork(definition, model)
	template, err := release.CompileTemplate([]byte(`{"steps":[
		{"code":"percent-10","type":"PERCENT_ROLLOUT","params":{"ranges":[{"start":0,"end":9}]}},
		{"code":"promote","type":"BASE_APPLY","params":{"cleanupScopeOverlay":true}},
		{"code":"complete","type":"COMPLETE","params":{}}
	]}`), release.FinalEffectBase)
	if err != nil {
		t.Fatal(err)
	}
	store.template = application.TemplateRef{Code: "percent-final", Version: 1, ReleaseTypeCode: "percentage", Definition: template}
	service := application.NewService(store, &sequenceIDs{values: []string{"percent-order", "percent-item"}}, fixedClock{now: time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)})

	created, err := service.CreateRelease(context.Background(), application.CreateReleaseCommand{
		IdempotencyKey: "create-percent", ModelCode: model.Code(), ReleaseTypeCode: "percentage",
		Scope: release.Scope{Region: "cn", Environment: "production", Stage: "blue"}, Actor: "operator",
		Items: []application.ReleaseDraft{{
			Action: release.ChangeAdd, After: map[string]string{"route_code": "visa", "priority": "9"},
			ExpectedCollectionRevision: 7,
		}},
	})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	if created.CurrentStep != release.StepPercentRollout || !created.CanExecute {
		t.Fatalf("created percentage order = %+v", created)
	}
	executed, err := service.Act(context.Background(), application.ActCommand{
		OrderID: created.ID, ActionRequestID: "execute-percent", ExpectedRevision: 1,
		ExpectedCurrentStep: "percent-10", Action: application.ActionExecute, Actor: "operator",
	})
	if err != nil {
		t.Fatalf("execute percentage: %v", err)
	}
	key, _ := catalog.EncodeKey([]string{"route_code"}, map[string]string{"route_code": "visa"})
	rule := store.overlays["production"]["blue"][key]
	if executed.CurrentStepStatus != release.StepExecuted || rule == nil || rule.ReleaseOrderID != created.ID || !reflect.DeepEqual(rule.RolloutRanges, []overlay.BucketRange{{Start: 0, End: 9}}) || store.revisions["production"] != 8 || store.outboxEvents != 1 {
		t.Fatalf("percentage facts: view=%+v rule=%+v revision=%d outbox=%d", executed, rule, store.revisions["production"], store.outboxEvents)
	}
}

func TestCompareApplicationUsesTemplatePreviewBucketAndPersistsMismatch(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name          string
		previewBucket int
		wantMatch     bool
	}{
		{name: "matching rollout bucket", previewBucket: 6, wantMatch: true},
		{name: "outside rollout bucket", previewBucket: 42, wantMatch: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			definition, model := compiledCatalog(t)
			store := newFakeUnitOfWork(definition, model)
			templateJSON := fmt.Sprintf(`{"steps":[
				{"code":"percent-10","type":"PERCENT_ROLLOUT","params":{"ranges":[{"start":0,"end":9}]}},
				{"code":"compare","type":"COMPARE","params":{"mode":"EFFECTIVE","previewBucket":%d}},
				{"code":"promote","type":"BASE_APPLY","params":{"cleanupScopeOverlay":true}},
				{"code":"complete","type":"COMPLETE","params":{}}
			]}`, test.previewBucket)
			template, err := release.CompileTemplate([]byte(templateJSON), release.FinalEffectBase)
			if err != nil {
				t.Fatal(err)
			}
			store.template = application.TemplateRef{Code: "percent-compare", Version: 1, ReleaseTypeCode: "percentage", Definition: template}
			service := application.NewService(store, &sequenceIDs{values: []string{"compare-order", "compare-item"}}, fixedClock{now: time.Date(2026, 8, 19, 19, 0, 0, 0, time.UTC)})
			created, err := service.CreateRelease(context.Background(), application.CreateReleaseCommand{
				IdempotencyKey: "create-compare", ModelCode: model.Code(), ReleaseTypeCode: "percentage",
				Scope: release.Scope{Region: "cn", Environment: "production", Stage: "blue"}, Actor: "operator",
				Items: []application.ReleaseDraft{{Action: release.ChangeAdd, After: map[string]string{"route_code": "visa", "priority": "9"}, ExpectedCollectionRevision: 7}},
			})
			if err != nil {
				t.Fatal(err)
			}
			executed, err := service.Act(context.Background(), application.ActCommand{OrderID: created.ID, ActionRequestID: "percent", ExpectedRevision: 1, ExpectedCurrentStep: "percent-10", Action: application.ActionExecute, Actor: "operator"})
			if err != nil {
				t.Fatal(err)
			}
			advanced, err := service.Act(context.Background(), application.ActCommand{OrderID: created.ID, ActionRequestID: "advance", ExpectedRevision: executed.Revision, ExpectedCurrentStep: "percent-10", Action: application.ActionAdvance, Actor: "operator"})
			if err != nil {
				t.Fatal(err)
			}
			compared, compareErr := service.Act(context.Background(), application.ActCommand{OrderID: created.ID, ActionRequestID: "compare", ExpectedRevision: advanced.Revision, ExpectedCurrentStep: "compare", Action: application.ActionExecute, Actor: "operator"})
			persisted := store.orders[created.ID].CurrentStep()
			if test.wantMatch {
				if compareErr != nil || compared.CurrentStepStatus != release.StepExecuted || persisted.CompareResult == nil || len(persisted.CompareResult.DiffKeys) != 0 {
					t.Fatalf("matched compare: view=%+v step=%+v error=%v", compared, persisted, compareErr)
				}
			} else {
				if !errors.Is(compareErr, release.ErrFailedPrecondition) || persisted.Status != release.StepPending || persisted.CompareResult == nil || len(persisted.CompareResult.DiffKeys) != 1 {
					t.Fatalf("mismatched compare: view=%+v step=%+v error=%v", compared, persisted, compareErr)
				}
			}
		})
	}
}

func TestReleasePreservesSensitiveAuthorityAndRejectsDisabledSelection(t *testing.T) {
	t.Parallel()
	definition, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: "credentials", KeyFields: []string{"name"}, SchemaVersion: 1,
		Fields: []catalog.FieldDefinition{
			{Name: "name", DisplayName: "Name", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "provider", DisplayName: "Provider", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 1},
			{Name: "note", DisplayName: "Note", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 2},
			{Name: "secret", DisplayName: "Secret", Type: catalog.FieldTypeString, Required: true, Sensitive: true, DisplayOrder: 3},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	model, err := catalog.CompileModel(definition, catalog.ModelSpec{
		Code: "credential-admin", Name: "Credentials", Collection: definition.Name(),
		Fields: []catalog.ModelField{
			{Name: "name", Type: catalog.FieldTypeString, Required: true, Editable: true, Queryable: true, UIControl: catalog.UIControlInput, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}},
			{Name: "provider", Type: catalog.FieldTypeString, Required: true, Editable: true, Queryable: true, UIControl: catalog.UIControlSelect, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}, OptionSource: &catalog.OptionSourceDefinition{Kind: catalog.OptionSourceStatic, StaticOptions: []catalog.SelectOptionDefinition{{Code: "active", Label: "Active"}, {Code: "legacy", Label: "Legacy", Disabled: true}}}},
			{Name: "note", Type: catalog.FieldTypeString, Required: true, Editable: true, Queryable: true, UIControl: catalog.UIControlInput, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}},
			{Name: "secret", Type: catalog.FieldTypeString, Required: true, Sensitive: true, Editable: true, UIControl: catalog.UIControlInput},
		},
		ProjectionFields: []string{"name", "provider", "note", "secret"}, KeyFields: []string{"name"}, DefaultPageSize: 20, MaxPageSize: 100, ConfigRevision: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	template, err := release.CompileTemplate([]byte(`{"steps":[{"code":"apply","type":"OVERLAY_APPLY","params":{}},{"code":"complete","type":"COMPLETE","params":{}}]}`), release.FinalEffectOverlay)
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeUnitOfWork(definition, model)
	store.template = application.TemplateRef{Code: "scope-final", Version: 1, ReleaseTypeCode: "scope", Definition: template}
	base, err := definition.NewRecord("production", map[string]string{"name": "primary", "provider": "active", "note": "before", "secret": "authority-secret"})
	if err != nil {
		t.Fatal(err)
	}
	base.ConfigRevision = 5
	store.records["production"][base.RecordKey] = base
	service := application.NewService(store, &sequenceIDs{values: []string{"item", "order"}}, fixedClock{now: time.Date(2026, 8, 19, 20, 0, 0, 0, time.UTC)})
	command := application.CreateReleaseCommand{
		IdempotencyKey: "preserve", ModelCode: model.Code(), ReleaseTypeCode: "scope",
		Scope: release.Scope{Region: "cn", Environment: "production", Stage: "blue"}, Actor: "operator",
		Items: []application.ReleaseDraft{{
			Action:                  release.ChangeModify,
			BaseBefore:              map[string]string{"name": "primary", "provider": "active", "note": "before"},
			EffectiveBefore:         map[string]string{"name": "primary", "provider": "active", "note": "before"},
			After:                   map[string]string{"name": "primary", "provider": "legacy", "note": "after"},
			PreserveSensitiveFields: []string{"secret"}, ExpectedRecordRevision: 5, ExpectedCollectionRevision: 7,
		}},
	}
	if _, err := service.CreateRelease(context.Background(), command); !errors.Is(err, release.ErrFailedPrecondition) {
		t.Fatalf("disabled option error = %v", err)
	}
	command.Items[0].After["provider"] = "active"
	created, err := service.CreateRelease(context.Background(), command)
	if err != nil {
		t.Fatalf("CreateRelease preserve: %v", err)
	}
	item := store.orders[created.ID].Items()[0]
	if item.After == nil || item.After.Data["secret"] != "authority-secret" || !reflect.DeepEqual(item.PreserveSensitiveFields, []string{"secret"}) {
		t.Fatalf("preserved item = %+v", item)
	}
}

func TestReleaseRejectsStaleCollectionSelectionButKeepsHistoricalValue(t *testing.T) {
	t.Parallel()
	providerDefinition, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: "providers", KeyFields: []string{"code"}, SchemaVersion: 1,
		Fields: []catalog.FieldDefinition{
			{Name: "code", DisplayName: "Code", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "label", DisplayName: "Label", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 1},
			{Name: "enabled", DisplayName: "Enabled", Type: catalog.FieldTypeBool, Required: true, DisplayOrder: 2},
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
			{Name: "note", DisplayName: "Note", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	model, err := catalog.CompileModel(credentialDefinition, catalog.ModelSpec{
		Code: "credential-admin", Name: "Credentials", Collection: credentialDefinition.Name(),
		Fields: []catalog.ModelField{
			{Name: "name", Type: catalog.FieldTypeString, Required: true, Editable: true, Queryable: true, UIControl: catalog.UIControlInput, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}},
			{Name: "provider", Type: catalog.FieldTypeString, Required: true, Editable: true, Queryable: true, UIControl: catalog.UIControlSelect, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}, OptionSource: &catalog.OptionSourceDefinition{
				Kind: catalog.OptionSourceCollection, Collection: "providers", ValueField: "code", LabelField: "label",
				FixedFilters: []catalog.OptionFixedFilter{{Field: "enabled", Value: "true"}}, Limit: 100,
			}},
			{Name: "note", Type: catalog.FieldTypeString, Required: true, Editable: true, UIControl: catalog.UIControlInput},
		},
		ProjectionFields: []string{"name", "provider", "note"}, KeyFields: []string{"name"}, DefaultPageSize: 20, MaxPageSize: 100, ConfigRevision: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	template, err := release.CompileTemplate([]byte(`{"steps":[{"code":"apply","type":"OVERLAY_APPLY","params":{}},{"code":"complete","type":"COMPLETE","params":{}}]}`), release.FinalEffectOverlay)
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeUnitOfWork(credentialDefinition, model)
	store.template = application.TemplateRef{Code: "scope-final", Version: 1, ReleaseTypeCode: "scope", Definition: template}
	base, _ := credentialDefinition.NewRecord("production", map[string]string{"name": "primary", "provider": "stripe", "note": "before"})
	base.ConfigRevision = 5
	store.records["production"][base.RecordKey] = base
	stripe, _ := providerDefinition.NewRecord("production", map[string]string{"code": "stripe", "label": "Stripe", "enabled": "false"})
	adyen, _ := providerDefinition.NewRecord("production", map[string]string{"code": "adyen", "label": "Adyen", "enabled": "true"})
	store.optionFacts["providers"] = application.OptionCollectionAuthority{Definition: providerDefinition, Records: []catalog.ConfigurationRecord{stripe, adyen}}
	service := application.NewService(store, &sequenceIDs{values: []string{"item", "order"}}, fixedClock{now: time.Date(2026, 8, 19, 21, 0, 0, 0, time.UTC)})
	command := application.CreateReleaseCommand{
		IdempotencyKey: "dynamic-option", ModelCode: model.Code(), ReleaseTypeCode: "scope",
		Scope: release.Scope{Region: "cn", Environment: "production", Stage: "blue"}, Actor: "operator",
		Items: []application.ReleaseDraft{{
			Action: release.ChangeModify, BaseBefore: base.Data, EffectiveBefore: base.Data,
			After:                  map[string]string{"name": "primary", "provider": "missing", "note": "after"},
			ExpectedRecordRevision: 5, ExpectedCollectionRevision: 7,
		}},
	}
	if _, err := service.CreateRelease(context.Background(), command); !errors.Is(err, release.ErrFailedPrecondition) {
		t.Fatalf("stale dynamic selection error = %v", err)
	}
	command.Items[0].After["provider"] = "stripe"
	created, err := service.CreateRelease(context.Background(), command)
	if err != nil {
		t.Fatalf("keep historical disabled selection: %v", err)
	}
	if got := store.orders[created.ID].Items()[0].After.Data["provider"]; got != "stripe" {
		t.Fatalf("historical provider = %q", got)
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
	optionFacts   map[string]application.OptionCollectionAuthority
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
		optionFacts:   make(map[string]application.OptionCollectionAuthority),
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

func (transaction *fakeTransaction) LoadOptionAuthorities(_ context.Context, collections []string, _ release.Scope) (map[string]application.OptionCollectionAuthority, error) {
	result := make(map[string]application.OptionCollectionAuthority, len(collections))
	for _, name := range collections {
		facts, exists := transaction.optionFacts[name]
		if !exists {
			continue
		}
		facts.Records = append([]catalog.ConfigurationRecord(nil), facts.Records...)
		facts.Rules = append([]overlay.Rule(nil), facts.Rules...)
		result[name] = facts
	}
	return result, nil
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

func (transaction *fakeTransaction) ApplyBaseEffect(_ context.Context, orderID string, effect release.BaseEffect) error {
	if transaction.revisions[effect.Scope.Environment] != effect.PreviousRevision {
		return release.ErrAborted
	}
	for _, change := range effect.Changes {
		switch change.Action {
		case release.ChangeAdd, release.ChangeModify:
			record := *change.After
			record.ConfigRevision = effect.AppliedRevision
			record.Data = cloneMap(record.Data)
			transaction.records[effect.Scope.Environment][record.RecordKey] = record
		case release.ChangeDelete:
			delete(transaction.records[effect.Scope.Environment], change.Before.RecordKey)
		}
	}
	stages := transaction.overlays[effect.Scope.Environment]
	if stages[effect.Scope.Stage] == nil {
		stages[effect.Scope.Stage] = make(map[string]*overlay.Rule)
	}
	for _, change := range effect.OverlayChanges {
		if change.NewRule == nil {
			delete(stages[effect.Scope.Stage], change.RecordKey)
		} else {
			cloned := cloneOverlayRule(*change.NewRule)
			stages[effect.Scope.Stage][change.RecordKey] = &cloned
		}
	}
	transaction.revisions[effect.Scope.Environment] = effect.AppliedRevision
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

func (transaction *fakeTransaction) ApplyPercentEffect(_ context.Context, _ string, effect release.PercentEffect) error {
	return transaction.applyRuleChanges(effect.Scope, effect.PreviousRevision, effect.AppliedRevision, effect.Changes)
}

func (transaction *fakeTransaction) applyRuleChanges(scope release.Scope, previousRevision, appliedRevision catalog.ConfigRevision, changes []release.OverlayRuleChange) error {
	if transaction.revisions[scope.Environment] != previousRevision {
		return release.ErrAborted
	}
	stages := transaction.overlays[scope.Environment]
	if stages[scope.Stage] == nil {
		stages[scope.Stage] = make(map[string]*overlay.Rule)
	}
	for _, change := range changes {
		if change.NewRule == nil {
			delete(stages[scope.Stage], change.RecordKey)
			continue
		}
		cloned := cloneOverlayRule(*change.NewRule)
		stages[scope.Stage][change.RecordKey] = &cloned
	}
	transaction.revisions[scope.Environment] = appliedRevision
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

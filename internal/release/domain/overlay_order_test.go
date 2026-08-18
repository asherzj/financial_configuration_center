package domain_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
)

func TestOverlayFinalExecutionCapturesExactPreviousAndNewRules(t *testing.T) {
	t.Parallel()
	template, err := release.CompileTemplate([]byte(`{"steps":[
		{"code":"apply-overlay","type":"OVERLAY_APPLY","params":{}},
		{"code":"complete","type":"COMPLETE","params":{}}
	]}`), release.FinalEffectOverlay)
	if err != nil {
		t.Fatal(err)
	}
	base := catalog.ConfigurationRecord{
		Collection: "payment_routes", Environment: "production", RecordKey: "route-a",
		Data: map[string]string{"endpoint": "base"}, ConfigRevision: 5,
	}
	effectiveBefore := base
	effectiveBefore.Data = map[string]string{"endpoint": "old-blue"}
	desired := base
	desired.Data = map[string]string{"endpoint": "new-blue"}
	previousActivation := catalog.ConfigRevision(6)
	previous := overlay.Rule{
		ID: "previous-rule", Collection: "payment_routes",
		Scope:     overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey: "route-a", Action: overlay.ActionModify,
		Content: map[string]string{"endpoint": "old-blue"}, ConfigRevision: 6,
		ReleaseOrderID: "older-release", ActivatedRevision: &previousActivation,
	}
	createdAt := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	order, err := release.NewOverlayFinalOrder(release.OverlayFinalOrderSpec{
		ID: "release-a", ReleaseNumber: "REL-1", IdempotencyKey: "create-a", ModelCode: "model",
		TemplateCode: "overlay-final", TemplateVersion: 1, ReleaseTypeCode: "scope", RequestDigest: zeroDigest,
		Scope:     release.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		CreatedBy: "creator", CreatedAt: createdAt, Template: template,
		Items: []release.OverlayFinalItemSpec{{
			ID: "item-a", Action: release.ChangeModify, BaseBefore: &base,
			EffectiveBefore: &effectiveBefore, After: &desired,
			ExpectedRecordRevision: 5, ExpectedCollectionRevision: 7,
		}},
	})
	if err != nil {
		t.Fatalf("NewOverlayFinalOrder: %v", err)
	}

	effect, err := order.ExecuteOverlay(release.OverlayAuthority{
		CollectionRevision: 7,
		BaseRecords:        map[string]*catalog.ConfigurationRecord{"route-a": &base},
		Rules:              map[string]*overlay.Rule{"route-a": &previous},
	}, 8, "operator", createdAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("ExecuteOverlay: %v", err)
	}
	if effect.EffectVersion != 1 || len(effect.Changes) != 1 {
		t.Fatalf("overlay effect = %+v", effect)
	}
	change := effect.Changes[0]
	if !reflect.DeepEqual(change.PreviousRule, &previous) {
		t.Fatalf("previous rule = %#v, want %#v", change.PreviousRule, previous)
	}
	if change.NewRule == nil || change.NewRule.Action != overlay.ActionModify || change.NewRule.Content["endpoint"] != "new-blue" || change.NewRule.ConfigRevision != 8 || change.NewRule.ReleaseOrderID != "release-a" {
		t.Fatalf("new rule = %#v", change.NewRule)
	}
	step := order.CurrentStep()
	if step.Status != release.StepExecuted || step.Effect == nil || step.Effect.Overlay == nil || !reflect.DeepEqual(*step.Effect.Overlay, effect) {
		t.Fatalf("executed step did not persist effect: %+v", step)
	}
}

func TestOverlayFinalRollbackRestoresExactPreviousRule(t *testing.T) {
	t.Parallel()
	order, previous, executed := executedOverlayOrder(t)
	order, err := release.RestoreOrder(order.State())
	if err != nil {
		t.Fatalf("RestoreOrder: %v", err)
	}
	rolledBackAt := time.Date(2026, 8, 19, 8, 2, 0, 0, time.UTC)

	compensation, err := order.RollbackOverlay(2, 8, 9, "operator", rolledBackAt)
	if err != nil {
		t.Fatalf("RollbackOverlay: %v", err)
	}
	if compensation.PreviousRevision != 8 || compensation.AppliedRevision != 9 || len(compensation.Changes) != 1 {
		t.Fatalf("compensation = %+v", compensation)
	}
	change := compensation.Changes[0]
	if !reflect.DeepEqual(change.PreviousRule, executed.Changes[0].NewRule) || !reflect.DeepEqual(change.NewRule, &previous) {
		t.Fatalf("inverse change = %#v, want current new -> exact previous", change)
	}
	if order.Status() != release.OrderRolledBack || order.Items()[0].Status != release.ItemRolledBack || order.Items()[0].ActiveConflictKey != "" {
		t.Fatalf("rolled back order status=%s item=%+v", order.Status(), order.Items()[0])
	}
	step := order.CurrentStep()
	if step.Status != release.StepRolledBack || step.RolledBackAt == nil || step.RolledBackBy != "operator" || step.Effect == nil || !reflect.DeepEqual(*step.Effect.Overlay, executed) {
		t.Fatalf("rolled back step lost original effect: %+v", step)
	}
}

func TestOverlayFinalRollbackRejectsChangedCollectionAuthority(t *testing.T) {
	t.Parallel()
	order, _, _ := executedOverlayOrder(t)

	_, err := order.RollbackOverlay(2, 9, 10, "operator", time.Date(2026, 8, 19, 8, 2, 0, 0, time.UTC))
	if !errors.Is(err, release.ErrAborted) {
		t.Fatalf("RollbackOverlay error = %v, want ErrAborted", err)
	}
	if order.Status() != release.OrderInProgress || order.CurrentStep().Status != release.StepExecuted || order.Revision() != 2 {
		t.Fatalf("stale rollback mutated order: status=%s step=%+v revision=%d", order.Status(), order.CurrentStep(), order.Revision())
	}
}

func TestOverlayFinalRollbackRestoresPriorAbsence(t *testing.T) {
	t.Parallel()
	template, err := release.CompileTemplate([]byte(`{"steps":[
		{"code":"apply-overlay","type":"OVERLAY_APPLY","params":{}},
		{"code":"complete","type":"COMPLETE","params":{}}
	]}`), release.FinalEffectOverlay)
	if err != nil {
		t.Fatal(err)
	}
	desired := catalog.ConfigurationRecord{
		Collection: "payment_routes", Environment: "production", RecordKey: "route-new", Data: map[string]string{"endpoint": "blue"},
	}
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	order, err := release.NewOverlayFinalOrder(release.OverlayFinalOrderSpec{
		ID: "release-add", ReleaseNumber: "REL-2", IdempotencyKey: "create-add", ModelCode: "model",
		TemplateCode: "overlay-final", TemplateVersion: 1, ReleaseTypeCode: "scope", RequestDigest: zeroDigest,
		Scope: release.Scope{Region: "cn", Environment: "production", Stage: "green"}, CreatedBy: "creator", CreatedAt: now, Template: template,
		Items: []release.OverlayFinalItemSpec{{
			ID: "item-add", Action: release.ChangeAdd, After: &desired, ExpectedCollectionRevision: 7,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	executed, err := order.ExecuteOverlay(release.OverlayAuthority{
		CollectionRevision: 7,
		BaseRecords:        map[string]*catalog.ConfigurationRecord{"route-new": nil},
		Rules:              map[string]*overlay.Rule{"route-new": nil},
	}, 8, "operator", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if executed.Changes[0].PreviousRule != nil || executed.Changes[0].NewRule == nil || executed.Changes[0].NewRule.Action != overlay.ActionAdd {
		t.Fatalf("ADD effect = %+v", executed)
	}
	compensation, err := order.RollbackOverlay(2, 8, 9, "operator", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if compensation.Changes[0].PreviousRule == nil || compensation.Changes[0].NewRule != nil {
		t.Fatalf("absence compensation = %+v", compensation)
	}
}

func executedOverlayOrder(t *testing.T) (*release.Order, overlay.Rule, release.OverlayEffect) {
	t.Helper()
	template, err := release.CompileTemplate([]byte(`{"steps":[
		{"code":"apply-overlay","type":"OVERLAY_APPLY","params":{}},
		{"code":"complete","type":"COMPLETE","params":{}}
	]}`), release.FinalEffectOverlay)
	if err != nil {
		t.Fatal(err)
	}
	base := catalog.ConfigurationRecord{Collection: "payment_routes", Environment: "production", RecordKey: "route-a", Data: map[string]string{"endpoint": "base"}, ConfigRevision: 5}
	effective := base
	effective.Data = map[string]string{"endpoint": "old-blue"}
	desired := base
	desired.Data = map[string]string{"endpoint": "new-blue"}
	activation := catalog.ConfigRevision(6)
	previous := overlay.Rule{
		ID: "previous-rule", Collection: "payment_routes", Scope: overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey: "route-a", Action: overlay.ActionModify, Content: map[string]string{"endpoint": "old-blue"},
		ConfigRevision: 6, ReleaseOrderID: "older-release", ActivatedRevision: &activation,
	}
	now := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	order, err := release.NewOverlayFinalOrder(release.OverlayFinalOrderSpec{
		ID: "release-a", ReleaseNumber: "REL-1", IdempotencyKey: "create-a", ModelCode: "model",
		TemplateCode: "overlay-final", TemplateVersion: 1, ReleaseTypeCode: "scope", RequestDigest: zeroDigest,
		Scope: release.Scope{Region: "cn", Environment: "production", Stage: "blue"}, CreatedBy: "creator", CreatedAt: now, Template: template,
		Items: []release.OverlayFinalItemSpec{{ID: "item-a", Action: release.ChangeModify, BaseBefore: &base, EffectiveBefore: &effective, After: &desired, ExpectedRecordRevision: 5, ExpectedCollectionRevision: 7}},
	})
	if err != nil {
		t.Fatal(err)
	}
	executed, err := order.ExecuteOverlay(release.OverlayAuthority{
		CollectionRevision: 7, BaseRecords: map[string]*catalog.ConfigurationRecord{"route-a": &base}, Rules: map[string]*overlay.Rule{"route-a": &previous},
	}, 8, "operator", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return order, previous, executed
}

const zeroDigest = "0000000000000000000000000000000000000000000000000000000000000000"

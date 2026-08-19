package domain_test

import (
	"errors"
	"testing"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
)

func TestSucceededBaseOrderPlansReverseItemsAndRejectsTargetDrift(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 20, 4, 0, 0, 0, time.UTC)
	after := catalog.ConfigurationRecord{Collection: "routes", Environment: "production", RecordKey: "visa", Data: map[string]string{"code": "visa", "value": "new"}}
	current := after
	current.ConfigRevision = 8
	order := restoreSucceededOrder(t, release.Item{ID: "item", Action: release.ChangeAdd, Collection: "routes", RecordKey: "visa", After: &after, Status: release.ItemApplied}, release.StepEffectBase, now)

	plan, err := order.PlanCompensation(release.CompensationAuthority{
		CollectionRevision: 8,
		BaseRecords:        map[string]*catalog.ConfigurationRecord{"visa": &current},
		EffectiveRecords:   map[string]*catalog.ConfigurationRecord{"visa": &current},
	})
	if err != nil {
		t.Fatalf("PlanCompensation: %v", err)
	}
	if plan.FinalEffect != release.FinalEffectBase || len(plan.Items) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	item := plan.Items[0]
	if item.Action != release.ChangeDelete || item.BaseBefore == nil || item.EffectiveBefore == nil || item.After != nil || item.ExpectedRecordRevision != 8 || item.ExpectedCollectionRevision != 8 {
		t.Fatalf("reverse item = %+v", item)
	}

	drifted := current
	drifted.Data = map[string]string{"code": "visa", "value": "third-party"}
	if _, err := order.PlanCompensation(release.CompensationAuthority{
		CollectionRevision: 9,
		BaseRecords:        map[string]*catalog.ConfigurationRecord{"visa": &drifted},
		EffectiveRecords:   map[string]*catalog.ConfigurationRecord{"visa": &drifted},
	}); !errors.Is(err, release.ErrAborted) {
		t.Fatalf("drift error = %v, want ErrAborted", err)
	}
}

func TestSucceededOverlayDeletePlansEffectiveAddOverExistingBase(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 20, 4, 30, 0, 0, time.UTC)
	base := catalog.ConfigurationRecord{Collection: "routes", Environment: "production", RecordKey: "visa", Data: map[string]string{"code": "visa", "value": "old"}, ConfigRevision: 5}
	before := base
	before.ConfigRevision = 0
	order := restoreSucceededOrder(t, release.Item{
		ID: "item", Action: release.ChangeDelete, Collection: "routes", RecordKey: "visa",
		BaseBefore: &before, EffectiveBefore: &before, ExpectedRecordRevision: 5, ExpectedCollectionRevision: 7, Status: release.ItemApplied,
	}, release.StepEffectOverlay, now)

	plan, err := order.PlanCompensation(release.CompensationAuthority{
		CollectionRevision: 8,
		BaseRecords:        map[string]*catalog.ConfigurationRecord{"visa": &base},
		EffectiveRecords:   map[string]*catalog.ConfigurationRecord{"visa": nil},
	})
	if err != nil {
		t.Fatalf("PlanCompensation: %v", err)
	}
	item := plan.Items[0]
	if plan.FinalEffect != release.FinalEffectOverlay || item.Action != release.ChangeAdd || item.BaseBefore == nil || item.EffectiveBefore != nil || item.After == nil || item.After.Data["value"] != "old" || item.ExpectedRecordRevision != 5 {
		t.Fatalf("overlay reverse item = %+v", item)
	}
}

func restoreSucceededOrder(t *testing.T, item release.Item, effectType release.StepEffectType, now time.Time) *release.Order {
	t.Helper()
	envelope := &release.StepEffectEnvelope{EffectVersion: 1, EffectType: effectType}
	switch effectType {
	case release.StepEffectBase:
		envelope.Base = &release.BaseEffect{
			EffectVersion: 1, Collection: item.Collection, Scope: release.Scope{Region: "cn", Environment: "production", Stage: "blue"},
			PreviousRevision: 7, AppliedRevision: 8,
			Changes:    []release.BaseChange{{Action: item.Action, Before: item.BaseBefore, After: item.After}},
			ExecutedAt: now, ExecutedBy: "operator",
		}
	case release.StepEffectOverlay:
		activation := catalog.ConfigRevision(8)
		envelope.Overlay = &release.OverlayEffect{
			EffectVersion: 1, Collection: item.Collection, Scope: release.Scope{Region: "cn", Environment: "production", Stage: "blue"},
			PreviousRevision: 7, AppliedRevision: 8,
			Changes: []release.OverlayRuleChange{{RecordKey: item.RecordKey, NewRule: &overlay.Rule{
				ID: "rule", Collection: item.Collection, Scope: overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
				RecordKey: item.RecordKey, Action: overlay.ActionDelete, ConfigRevision: 8, ReleaseOrderID: "source", ActivatedRevision: &activation,
				CreatedAt: now, CreatedBy: "operator", UpdatedAt: now, UpdatedBy: "operator",
			}}},
			ExecutedAt: now, ExecutedBy: "operator",
		}
	default:
		t.Fatalf("unsupported effect type %q", effectType)
	}
	completed := now.Add(time.Minute)
	order, err := release.RestoreOrder(release.OrderState{
		ID: "source", ReleaseNumber: "REL-SOURCE", IdempotencyKey: "source", RequestDigest: zeroDigest,
		ModelCode: "model", TemplateCode: "source-template", TemplateVersion: 1, ReleaseTypeCode: "direct",
		Scope:     release.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		CreatedBy: "creator", CreatedAt: now.Add(-time.Hour), UpdatedBy: "operator", UpdatedAt: completed, CompletedAt: &completed,
		Status: release.OrderSucceeded, Revision: 4, CurrentStep: 1,
		Steps: []release.StepState{
			{Code: "apply", Type: map[release.StepEffectType]release.StepType{release.StepEffectBase: release.StepBaseApply, release.StepEffectOverlay: release.StepOverlayApply}[effectType], Status: release.StepExecuted, Effect: envelope},
			{Code: "complete", Type: release.StepComplete, Status: release.StepExecuted},
		},
		Items: []release.Item{item},
	})
	if err != nil {
		t.Fatalf("RestoreOrder: %v", err)
	}
	return order
}

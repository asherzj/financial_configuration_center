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

func TestPercentageRolloutExpandsOrderOwnedRuleMonotonically(t *testing.T) {
	t.Parallel()
	order, after := newPercentageOrder(t)
	now := time.Date(2026, 8, 19, 14, 0, 0, 0, time.UTC)

	first, err := order.ExecutePercentRollout(release.OverlayAuthority{
		CollectionRevision: 7,
		BaseRecords:        map[string]*catalog.ConfigurationRecord{after.RecordKey: nil},
		Rules:              map[string]*overlay.Rule{after.RecordKey: nil},
	}, 8, "operator", now)
	if err != nil {
		t.Fatalf("ExecutePercentRollout first: %v", err)
	}
	if first.AddedRanges[0] != (overlay.BucketRange{Start: 0, End: 9}) || first.Changes[0].PreviousRule != nil {
		t.Fatalf("first effect = %+v", first)
	}
	firstRule := first.Changes[0].NewRule
	if firstRule == nil || firstRule.ReleaseOrderID != order.ID() || firstRule.Action != overlay.ActionAdd || !reflect.DeepEqual(firstRule.RolloutRanges, []overlay.BucketRange{{Start: 0, End: 9}}) {
		t.Fatalf("first rollout rule = %+v", firstRule)
	}
	if order.CurrentStep().Effect == nil || order.CurrentStep().Effect.Percent == nil {
		t.Fatalf("percentage effect envelope = %+v", order.CurrentStep().Effect)
	}
	if err := order.Advance(2, "operator", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	second, err := order.ExecutePercentRollout(release.OverlayAuthority{
		CollectionRevision: 8,
		BaseRecords:        map[string]*catalog.ConfigurationRecord{after.RecordKey: nil},
		Rules:              map[string]*overlay.Rule{after.RecordKey: firstRule},
	}, 9, "operator", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("ExecutePercentRollout second: %v", err)
	}
	secondRule := second.Changes[0].NewRule
	if second.PreviousRevision != 8 || second.AppliedRevision != 9 || !reflect.DeepEqual(second.AddedRanges, []overlay.BucketRange{{Start: 10, End: 49}}) || !reflect.DeepEqual(secondRule.RolloutRanges, []overlay.BucketRange{{Start: 0, End: 49}}) {
		t.Fatalf("second effect = %+v", second)
	}
}

func TestPercentageRolloutRejectsThirdPartyRuleAndStaleRevision(t *testing.T) {
	t.Parallel()
	order, after := newPercentageOrder(t)
	activation := catalog.ConfigRevision(7)
	thirdParty := overlay.Rule{
		ID: "other", Collection: after.Collection,
		Scope:     overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey: after.RecordKey, Action: overlay.ActionAdd, Content: after.Data,
		RolloutRanges: []overlay.BucketRange{{Start: 50, End: 59}}, ConfigRevision: 7,
		ReleaseOrderID: "other-order", ActivatedRevision: &activation,
	}
	authority := release.OverlayAuthority{
		CollectionRevision: 7,
		BaseRecords:        map[string]*catalog.ConfigurationRecord{after.RecordKey: nil},
		Rules:              map[string]*overlay.Rule{after.RecordKey: &thirdParty},
	}
	if _, err := order.ExecutePercentRollout(authority, 8, "operator", time.Now().UTC()); !errors.Is(err, release.ErrAborted) {
		t.Fatalf("third-party rule error = %v", err)
	}
	if order.Revision() != 1 || order.CurrentStep().Status != release.StepPending {
		t.Fatalf("rejected rollout mutated order: revision=%d step=%+v", order.Revision(), order.CurrentStep())
	}
	authority.Rules[after.RecordKey] = nil
	authority.CollectionRevision = 8
	if _, err := order.ExecutePercentRollout(authority, 9, "operator", time.Now().UTC()); !errors.Is(err, release.ErrAborted) {
		t.Fatalf("stale first rollout error = %v", err)
	}
}

func TestPercentagePromotionAndRollbackRestoreBaseAndRanges(t *testing.T) {
	t.Parallel()
	order, after := newPercentageOrder(t)
	now := time.Date(2026, 8, 19, 17, 0, 0, 0, time.UTC)
	first, err := order.ExecutePercentRollout(release.OverlayAuthority{
		CollectionRevision: 7, BaseRecords: map[string]*catalog.ConfigurationRecord{after.RecordKey: nil}, Rules: map[string]*overlay.Rule{after.RecordKey: nil},
	}, 8, "operator", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := order.Advance(2, "operator", now); err != nil {
		t.Fatal(err)
	}
	second, err := order.ExecutePercentRollout(release.OverlayAuthority{
		CollectionRevision: 8, BaseRecords: map[string]*catalog.ConfigurationRecord{after.RecordKey: nil}, Rules: map[string]*overlay.Rule{after.RecordKey: first.Changes[0].NewRule},
	}, 9, "operator", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := order.Advance(4, "operator", now); err != nil {
		t.Fatal(err)
	}

	promoted, err := order.ExecuteBase(release.BaseAuthority{
		CollectionRevision: 9,
		Records:            map[string]*catalog.ConfigurationRecord{after.RecordKey: nil},
		Rules:              map[string]*overlay.Rule{after.RecordKey: second.Changes[0].NewRule},
	}, 10, "operator", now)
	if err != nil {
		t.Fatalf("ExecuteBase promotion: %v", err)
	}
	if promoted.AppliedRevision != 10 || promoted.Changes[0].Action != release.ChangeAdd || promoted.OverlayChanges[0].PreviousRule == nil || promoted.OverlayChanges[0].NewRule != nil {
		t.Fatalf("promotion effect = %+v", promoted)
	}
	if order.CurrentStep().Effect == nil || order.CurrentStep().Effect.Base == nil {
		t.Fatalf("base effect envelope = %+v", order.CurrentStep().Effect)
	}
	applied := after
	applied.ConfigRevision = 10
	compensation, err := order.RollbackBase(6, release.BaseAuthority{
		CollectionRevision: 10,
		Records:            map[string]*catalog.ConfigurationRecord{after.RecordKey: &applied},
		Rules:              map[string]*overlay.Rule{after.RecordKey: nil},
	}, 11, "operator", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("RollbackBase: %v", err)
	}
	if compensation.Changes[0].Action != release.ChangeDelete || compensation.Changes[0].After != nil || compensation.OverlayChanges[0].NewRule == nil || !reflect.DeepEqual(compensation.OverlayChanges[0].NewRule.RolloutRanges, []overlay.BucketRange{{Start: 0, End: 49}}) {
		t.Fatalf("base compensation = %+v", compensation)
	}
	if order.Status() != release.OrderRolledBack || order.CurrentStep().Status != release.StepRolledBack {
		t.Fatalf("rolled back order = status %s step %+v", order.Status(), order.CurrentStep())
	}
}

func TestRollbackAllInvertsEveryExecutedEffectInReverseOrder(t *testing.T) {
	t.Parallel()
	order, after := newPercentageOrder(t)
	now := time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC)
	first, err := order.ExecutePercentRollout(release.OverlayAuthority{
		CollectionRevision: 7, BaseRecords: map[string]*catalog.ConfigurationRecord{after.RecordKey: nil}, Rules: map[string]*overlay.Rule{after.RecordKey: nil},
	}, 8, "operator", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := order.Advance(2, "operator", now); err != nil {
		t.Fatal(err)
	}
	second, err := order.ExecutePercentRollout(release.OverlayAuthority{
		CollectionRevision: 8, BaseRecords: map[string]*catalog.ConfigurationRecord{after.RecordKey: nil}, Rules: map[string]*overlay.Rule{after.RecordKey: first.Changes[0].NewRule},
	}, 9, "operator", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := order.Advance(4, "operator", now); err != nil {
		t.Fatal(err)
	}
	if _, err := order.ExecuteBase(release.BaseAuthority{
		CollectionRevision: 9, Records: map[string]*catalog.ConfigurationRecord{after.RecordKey: nil}, Rules: map[string]*overlay.Rule{after.RecordKey: second.Changes[0].NewRule},
	}, 10, "operator", now); err != nil {
		t.Fatal(err)
	}
	applied := after
	applied.ConfigRevision = 10
	plan, err := order.RollbackAll(6, release.BaseAuthority{
		CollectionRevision: 10, Records: map[string]*catalog.ConfigurationRecord{after.RecordKey: &applied}, Rules: map[string]*overlay.Rule{after.RecordKey: nil},
	}, 11, "operator", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("RollbackAll: %v", err)
	}
	if plan.EffectType != release.StepEffectBase || plan.Base == nil || plan.Base.Changes[0].Action != release.ChangeDelete {
		t.Fatalf("rollback plan = %+v", plan)
	}
	if len(plan.Base.OverlayChanges) != 3 {
		t.Fatalf("inverse overlay chain = %+v", plan.Base.OverlayChanges)
	}
	if first := plan.Base.OverlayChanges[0]; first.PreviousRule != nil || first.NewRule == nil || !reflect.DeepEqual(first.NewRule.RolloutRanges, []overlay.BucketRange{{Start: 0, End: 49}}) {
		t.Fatalf("base inverse = %+v", first)
	}
	if last := plan.Base.OverlayChanges[2]; last.PreviousRule == nil || last.NewRule != nil {
		t.Fatalf("oldest percent inverse = %+v", last)
	}
	state := order.State()
	if order.Status() != release.OrderRolledBack || order.CurrentStep().Code != "percent-10" {
		t.Fatalf("rolled back order = status %s current %+v", order.Status(), order.CurrentStep())
	}
	for _, step := range state.Steps[:3] {
		if step.Status != release.StepRolledBack {
			t.Fatalf("effect step was not rolled back: %+v", step)
		}
	}
}

func TestRollbackAllRejectsThirdPartyDriftWithoutMutatingOrder(t *testing.T) {
	t.Parallel()
	order, after := newPercentageOrder(t)
	now := time.Date(2026, 8, 20, 2, 30, 0, 0, time.UTC)
	first, err := order.ExecutePercentRollout(release.OverlayAuthority{
		CollectionRevision: 7, BaseRecords: map[string]*catalog.ConfigurationRecord{after.RecordKey: nil}, Rules: map[string]*overlay.Rule{after.RecordKey: nil},
	}, 8, "operator", now)
	if err != nil {
		t.Fatal(err)
	}
	thirdParty := *first.Changes[0].NewRule
	thirdParty.ReleaseOrderID = "other-order"
	before := order.State()
	if _, err := order.RollbackAll(2, release.BaseAuthority{
		CollectionRevision: 8, Records: map[string]*catalog.ConfigurationRecord{after.RecordKey: nil}, Rules: map[string]*overlay.Rule{after.RecordKey: &thirdParty},
	}, 9, "operator", now.Add(time.Minute)); !errors.Is(err, release.ErrAborted) {
		t.Fatalf("third-party rollback = %v", err)
	}
	if !reflect.DeepEqual(order.State(), before) {
		t.Fatal("failed rollback mutated order")
	}
}

func TestComparePersistsDigestsAndKeepsMismatchPending(t *testing.T) {
	t.Parallel()
	order, after := newCompareOrder(t)
	now := time.Date(2026, 8, 19, 18, 0, 0, 0, time.UTC)

	mismatch := after
	mismatch.Data = map[string]string{"route_code": "visa", "priority": "8"}
	result, matched, err := order.ExecuteCompare(1, []catalog.ConfigurationRecord{mismatch}, "operator", now)
	if err != nil {
		t.Fatalf("ExecuteCompare mismatch: %v", err)
	}
	if matched || !reflect.DeepEqual(result.DiffKeys, []string{after.RecordKey}) || result.ExpectedDigest.Value == result.ActualDigest.Value {
		t.Fatalf("mismatch result = %+v matched=%t", result, matched)
	}
	step := order.CurrentStep()
	if step.Status != release.StepPending || step.CompareResult == nil || order.Revision() != 2 {
		t.Fatalf("mismatch step = %+v revision=%d", step, order.Revision())
	}

	result, matched, err = order.ExecuteCompare(2, []catalog.ConfigurationRecord{after}, "operator", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ExecuteCompare match: %v", err)
	}
	if !matched || len(result.DiffKeys) != 0 || result.ExpectedDigest != result.ActualDigest {
		t.Fatalf("match result = %+v matched=%t", result, matched)
	}
	step = order.CurrentStep()
	if step.Status != release.StepExecuted || step.CompareResult == nil || step.ExecutedBy != "operator" || order.Revision() != 3 {
		t.Fatalf("matched step = %+v revision=%d", step, order.Revision())
	}
}

func newCompareOrder(t *testing.T) (*release.Order, catalog.ConfigurationRecord) {
	t.Helper()
	template, err := release.CompileTemplate([]byte(`{"steps":[
		{"code":"compare","type":"COMPARE","params":{"mode":"EFFECTIVE","previewBucket":6}},
		{"code":"apply","type":"BASE_APPLY","params":{"cleanupScopeOverlay":true}},
		{"code":"complete","type":"COMPLETE","params":{}}
	]}`), release.FinalEffectBase)
	if err != nil {
		t.Fatal(err)
	}
	after := catalog.ConfigurationRecord{
		Collection: "payment_routes", Environment: "production", RecordKey: "route-a",
		Data: map[string]string{"route_code": "visa", "priority": "9"},
	}
	order, err := release.NewBaseFinalOrder(release.BaseFinalOrderSpec{
		ID: "compare-order", ReleaseNumber: "REL-COMPARE-1", IdempotencyKey: "compare-create",
		ModelCode: "payment-route-admin", TemplateCode: "compare-final", TemplateVersion: 1,
		ReleaseTypeCode: "compare", RequestDigest: "0000000000000000000000000000000000000000000000000000000000000000",
		Scope: release.Scope{Region: "cn", Environment: "production", Stage: "blue"}, CreatedBy: "operator", CreatedAt: time.Date(2026, 8, 19, 17, 0, 0, 0, time.UTC), Template: template,
		Items: []release.BaseFinalItemSpec{{ID: "compare-item", After: after, ExpectedCollectionRevision: 7}},
	})
	if err != nil {
		t.Fatal(err)
	}
	step := order.CurrentStep()
	if step.CompareMode != release.CompareEffective || step.ComparePreviewBucket == nil || *step.ComparePreviewBucket != 6 {
		t.Fatalf("compiled compare context = %+v", step)
	}
	return order, after
}

func newPercentageOrder(t *testing.T) (*release.Order, catalog.ConfigurationRecord) {
	t.Helper()
	template, err := release.CompileTemplate([]byte(`{"steps":[
		{"code":"percent-10","type":"PERCENT_ROLLOUT","params":{"ranges":[{"start":0,"end":9}]}},
		{"code":"percent-50","type":"PERCENT_ROLLOUT","params":{"ranges":[{"start":10,"end":49}]}},
		{"code":"promote","type":"BASE_APPLY","params":{"cleanupScopeOverlay":true}},
		{"code":"complete","type":"COMPLETE","params":{}}
	]}`), release.FinalEffectBase)
	if err != nil {
		t.Fatal(err)
	}
	after := catalog.ConfigurationRecord{
		Collection: "payment_routes", Environment: "production", RecordKey: "route-a",
		Data: map[string]string{"route_code": "visa", "priority": "9"},
	}
	order, err := release.NewBaseFinalOrder(release.BaseFinalOrderSpec{
		ID: "rollout-order", ReleaseNumber: "REL-ROLL-1", IdempotencyKey: "rollout-create",
		ModelCode: "payment-route-admin", TemplateCode: "percent-final", TemplateVersion: 1,
		ReleaseTypeCode: "percentage", RequestDigest: "0000000000000000000000000000000000000000000000000000000000000000",
		Scope:     release.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		CreatedBy: "operator", CreatedAt: time.Date(2026, 8, 19, 13, 0, 0, 0, time.UTC), Template: template,
		Items: []release.BaseFinalItemSpec{{ID: "rollout-item", After: after, ExpectedCollectionRevision: 7}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return order, after
}

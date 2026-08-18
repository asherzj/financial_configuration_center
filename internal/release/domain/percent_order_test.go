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

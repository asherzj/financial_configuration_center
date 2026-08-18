package domain_test

import (
	"testing"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
)

func TestBaseFinalAddStateMachine(t *testing.T) {
	t.Parallel()

	after := catalog.ConfigurationRecord{
		Collection:  "payment_routes",
		Environment: "production",
		RecordKey:   "WyJ2aXNhLWNuIl0",
		Data:        map[string]string{"route_code": "visa-cn", "priority": "7"},
	}
	order, err := release.NewBaseFinalOrder(release.BaseFinalOrderSpec{
		ID:             "018fb4a7-6c54-7d34-bc21-357b4e943c30",
		ReleaseNumber:  "REL-20260819-0001",
		IdempotencyKey: "62da32b9-bbc4-4db1-9407-301ba7d59311",
		ModelCode:      "payment-route-admin",
		TemplateCode:   "base-final", TemplateVersion: 1, ReleaseTypeCode: "direct", RequestDigest: "0000000000000000000000000000000000000000000000000000000000000000",
		Scope:     release.Scope{Region: "cn", Environment: "production"},
		CreatedBy: "operator@example.com",
		CreatedAt: time.Date(2026, 8, 19, 3, 0, 0, 0, time.UTC),
		Items: []release.BaseFinalItemSpec{{
			ID:                         "018fb4a7-74b6-7a5f-a4d0-11c74002dadd",
			After:                      after,
			ExpectedRecordRevision:     0,
			ExpectedCollectionRevision: 7,
		}},
	})
	if err != nil {
		t.Fatalf("NewBaseFinalOrder: %v", err)
	}
	assertOrder(t, order, release.OrderInProgress, release.StepBaseApply, release.StepPending, 1)
	if order.Items()[0].BaseBefore != nil || order.Items()[0].EffectiveBefore != nil {
		t.Fatal("ADD persisted a before image")
	}

	effect, err := order.ExecuteBase(release.BaseAuthority{
		CollectionRevision: 7,
		Records:            map[string]*catalog.ConfigurationRecord{after.RecordKey: nil},
	}, "operator@example.com", time.Date(2026, 8, 19, 3, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ExecuteBase: %v", err)
	}
	if len(effect.Changes) != 1 || effect.Changes[0].After.RecordKey != after.RecordKey {
		t.Fatalf("unexpected base effect: %+v", effect)
	}
	assertOrder(t, order, release.OrderInProgress, release.StepBaseApply, release.StepExecuted, 2)

	if err := order.Advance(2, "operator@example.com", time.Date(2026, 8, 19, 3, 2, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	assertOrder(t, order, release.OrderInProgress, release.StepComplete, release.StepPending, 3)
	if err := order.Complete(3, "operator@example.com", time.Date(2026, 8, 19, 3, 3, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	assertOrder(t, order, release.OrderSucceeded, release.StepComplete, release.StepExecuted, 4)
	if order.Items()[0].Status != release.ItemApplied || order.Items()[0].ActiveConflictKey != "" {
		t.Fatalf("completed item not finalized: %+v", order.Items()[0])
	}
}

func TestBaseFinalExecutionRejectsStaleAuthorityWithoutMutation(t *testing.T) {
	t.Parallel()

	after := catalog.ConfigurationRecord{Collection: "routes", Environment: "production", RecordKey: "key", Data: map[string]string{"code": "visa"}}
	order, err := release.NewBaseFinalOrder(release.BaseFinalOrderSpec{
		ID: "order", ReleaseNumber: "REL-1", IdempotencyKey: "idem", ModelCode: "model",
		TemplateCode: "base-final", TemplateVersion: 1, ReleaseTypeCode: "direct", RequestDigest: "0000000000000000000000000000000000000000000000000000000000000000",
		Scope: release.Scope{Region: "cn", Environment: "production"}, CreatedBy: "actor", CreatedAt: time.Now().UTC(),
		Items: []release.BaseFinalItemSpec{{ID: "item", After: after, ExpectedCollectionRevision: 7}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := order.ExecuteBase(release.BaseAuthority{CollectionRevision: 8}, "actor", time.Now().UTC()); err == nil {
		t.Fatal("stale collection revision succeeded")
	}
	assertOrder(t, order, release.OrderInProgress, release.StepBaseApply, release.StepPending, 1)

	existing := after
	existing.ConfigRevision = 6
	if _, err := order.ExecuteBase(release.BaseAuthority{
		CollectionRevision: 7,
		Records:            map[string]*catalog.ConfigurationRecord{"key": &existing},
	}, "actor", time.Now().UTC()); err == nil {
		t.Fatal("ADD over existing record succeeded")
	}
	assertOrder(t, order, release.OrderInProgress, release.StepBaseApply, release.StepPending, 1)
}

func assertOrder(t *testing.T, order *release.Order, status release.OrderStatus, stepType release.StepType, stepStatus release.StepStatus, revision release.EntityRevision) {
	t.Helper()
	if order.Status() != status || order.CurrentStep().Type != stepType || order.CurrentStep().Status != stepStatus || order.Revision() != revision {
		t.Fatalf("order = status %s, step %+v, revision %d", order.Status(), order.CurrentStep(), order.Revision())
	}
}

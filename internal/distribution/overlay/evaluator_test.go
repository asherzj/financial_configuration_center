package overlay_test

import (
	"errors"
	"reflect"
	"testing"

	overlay "github.com/asherzj/financial_configuration_center/internal/distribution/overlay"
	readmodel "github.com/asherzj/financial_configuration_center/internal/distribution/readmodel"
)

func TestEvaluateAppliesExactStageAfterEnvironmentOverlay(t *testing.T) {
	base := []readmodel.ConfigurationRecord{{
		Collection:  "payment_routes",
		Environment: "production",
		RecordKey:   "route-a",
		Data:        map[string]string{"endpoint": "base"},
	}}
	rules := []overlay.Rule{
		{
			ID:                "environment-rule",
			Collection:        "payment_routes",
			Scope:             overlay.Scope{Region: "cn", Environment: "production"},
			RecordKey:         "route-a",
			Action:            overlay.ActionModify,
			Content:           map[string]string{"endpoint": "environment"},
			ConfigRevision:    2,
			ActivatedRevision: revision(2),
		},
		{
			ID:                "stage-rule",
			Collection:        "payment_routes",
			Scope:             overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
			RecordKey:         "route-a",
			Action:            overlay.ActionModify,
			Content:           map[string]string{"endpoint": "blue"},
			ConfigRevision:    3,
			ActivatedRevision: revision(3),
		},
	}

	records, err := overlay.Evaluate(
		overlay.Query{Collection: "payment_routes", Scope: overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"}},
		base,
		rules,
	)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	want := []readmodel.ConfigurationRecord{{
		Collection:     "payment_routes",
		Environment:    "production",
		RecordKey:      "route-a",
		Data:           map[string]string{"endpoint": "blue"},
		ConfigRevision: 3,
	}}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("effective records = %#v, want %#v", records, want)
	}

	greenRecords, err := overlay.Evaluate(
		overlay.Query{Collection: "payment_routes", Scope: overlay.Scope{Region: "cn", Environment: "production", Stage: "green"}},
		base,
		rules,
	)
	if err != nil {
		t.Fatalf("Evaluate green scope: %v", err)
	}
	if greenRecords[0].Data["endpoint"] != "environment" {
		t.Fatalf("green scope endpoint = %q, want environment", greenRecords[0].Data["endpoint"])
	}
}

func TestEvaluateAddsMissingEffectiveRecord(t *testing.T) {
	rules := []overlay.Rule{{
		ID:                "add-route",
		Collection:        "payment_routes",
		Scope:             overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey:         "route-b",
		Action:            overlay.ActionAdd,
		Content:           map[string]string{"endpoint": "blue-only"},
		ConfigRevision:    4,
		ActivatedRevision: revision(4),
	}}

	records, err := overlay.Evaluate(
		overlay.Query{Collection: "payment_routes", Scope: overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"}},
		nil,
		rules,
	)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	want := []readmodel.ConfigurationRecord{{
		Collection:     "payment_routes",
		Environment:    "production",
		RecordKey:      "route-b",
		Data:           map[string]string{"endpoint": "blue-only"},
		ConfigRevision: 4,
	}}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("effective records = %#v, want %#v", records, want)
	}
}

func TestEvaluateDeletesExistingEffectiveRecord(t *testing.T) {
	base := []readmodel.ConfigurationRecord{{
		Collection:  "payment_routes",
		Environment: "production",
		RecordKey:   "route-a",
		Data:        map[string]string{"endpoint": "base"},
	}}
	rules := []overlay.Rule{{
		ID:                "delete-route",
		Collection:        "payment_routes",
		Scope:             overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey:         "route-a",
		Action:            overlay.ActionDelete,
		ConfigRevision:    5,
		ActivatedRevision: revision(5),
	}}

	records, err := overlay.Evaluate(
		overlay.Query{Collection: "payment_routes", Scope: overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"}},
		base,
		rules,
	)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("effective records = %#v, want empty", records)
	}
}

func TestEvaluateClassifiesStateBreakingActionsAsInvariantErrors(t *testing.T) {
	base := []readmodel.ConfigurationRecord{{
		Collection:  "payment_routes",
		Environment: "production",
		RecordKey:   "existing",
		Data:        map[string]string{"endpoint": "base"},
	}}
	tests := []struct {
		name string
		rule overlay.Rule
	}{
		{
			name: "add existing",
			rule: activeRule("add-existing", "existing", overlay.ActionAdd, map[string]string{"endpoint": "new"}),
		},
		{
			name: "modify missing",
			rule: activeRule("modify-missing", "missing", overlay.ActionModify, map[string]string{"endpoint": "new"}),
		},
		{
			name: "delete missing",
			rule: activeRule("delete-missing", "missing", overlay.ActionDelete, nil),
		},
		{
			name: "unknown action",
			rule: activeRule("unknown-action", "existing", overlay.Action("UPSERT"), map[string]string{"endpoint": "new"}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := overlay.Evaluate(
				overlay.Query{Collection: "payment_routes", Scope: overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"}},
				base,
				[]overlay.Rule{test.rule},
			)
			if !errors.Is(err, overlay.ErrInvariant) {
				t.Fatalf("Evaluate error = %v, want ErrInvariant", err)
			}
		})
	}
}

func TestEvaluateRejectsActionContentThatBreaksRuleInvariants(t *testing.T) {
	base := []readmodel.ConfigurationRecord{{
		Collection:  "payment_routes",
		Environment: "production",
		RecordKey:   "existing",
		Data:        map[string]string{"endpoint": "base"},
	}}
	tests := []struct {
		name string
		rule overlay.Rule
	}{
		{name: "add without content", rule: activeRule("bad-add", "missing", overlay.ActionAdd, nil)},
		{name: "modify without content", rule: activeRule("bad-modify", "existing", overlay.ActionModify, nil)},
		{name: "delete with content", rule: activeRule("bad-delete", "existing", overlay.ActionDelete, map[string]string{})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := overlay.Evaluate(
				overlay.Query{Collection: "payment_routes", Scope: overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"}},
				base,
				[]overlay.Rule{test.rule},
			)
			if !errors.Is(err, overlay.ErrInvariant) {
				t.Fatalf("Evaluate error = %v, want ErrInvariant", err)
			}
		})
	}
}

func TestEvaluateOrdinaryQueryExcludesPercentageRule(t *testing.T) {
	base := []readmodel.ConfigurationRecord{{
		Collection:  "payment_routes",
		Environment: "production",
		RecordKey:   "route-a",
		Data:        map[string]string{"endpoint": "base"},
	}}
	rule := activeRule("percentage", "route-a", overlay.ActionModify, map[string]string{"endpoint": "rollout"})
	rule.RolloutRanges = []overlay.BucketRange{{Start: 0, End: 49}}

	records, err := overlay.Evaluate(
		overlay.Query{Collection: "payment_routes", Scope: overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"}},
		base,
		[]overlay.Rule{rule},
	)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if records[0].Data["endpoint"] != "base" {
		t.Fatalf("ordinary query endpoint = %q, want base", records[0].Data["endpoint"])
	}
}

func TestEvaluatePreviewAppliesOnlyPercentageRulesContainingBucket(t *testing.T) {
	base := []readmodel.ConfigurationRecord{{
		Collection:  "payment_routes",
		Environment: "production",
		RecordKey:   "route-a",
		Data:        map[string]string{"endpoint": "base"},
	}}
	rule := activeRule("percentage", "route-a", overlay.ActionModify, map[string]string{"endpoint": "rollout"})
	rule.RolloutRanges = []overlay.BucketRange{{Start: 0, End: 49}}
	unmatchedBucket := int32(75)

	records, err := overlay.Evaluate(
		overlay.Query{
			Collection:    "payment_routes",
			Scope:         overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
			PreviewBucket: &unmatchedBucket,
		},
		base,
		[]overlay.Rule{rule},
	)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if records[0].Data["endpoint"] != "base" {
		t.Fatalf("unmatched preview endpoint = %q, want base", records[0].Data["endpoint"])
	}

	matchedBucket := int32(25)
	records, err = overlay.Evaluate(
		overlay.Query{
			Collection:    "payment_routes",
			Scope:         overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
			PreviewBucket: &matchedBucket,
		},
		base,
		[]overlay.Rule{rule},
	)
	if err != nil {
		t.Fatalf("Evaluate matched preview: %v", err)
	}
	if records[0].Data["endpoint"] != "rollout" {
		t.Fatalf("matched preview endpoint = %q, want rollout", records[0].Data["endpoint"])
	}
}

func activeRule(id, recordKey string, action overlay.Action, content map[string]string) overlay.Rule {
	return overlay.Rule{
		ID:                id,
		Collection:        "payment_routes",
		Scope:             overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey:         recordKey,
		Action:            action,
		Content:           content,
		ConfigRevision:    6,
		ActivatedRevision: revision(6),
	}
}

func revision(value readmodel.ConfigRevision) *readmodel.ConfigRevision {
	return &value
}

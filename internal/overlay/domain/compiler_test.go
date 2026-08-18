package domain_test

import (
	"errors"
	"reflect"
	"testing"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
)

func TestCompileRuleUsesAddWhenBaseIsMissing(t *testing.T) {
	activation := catalog.ConfigRevision(8)
	desired := catalog.ConfigurationRecord{
		Collection:  "payment_routes",
		Environment: "production",
		RecordKey:   "route-a",
		Data:        map[string]string{"endpoint": "blue"},
	}

	rule, err := overlay.CompileRule(overlay.RuleSpec{
		ID:                "overlay-a",
		Collection:        "payment_routes",
		Scope:             overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey:         "route-a",
		Desired:           &desired,
		ConfigRevision:    8,
		ReleaseOrderID:    "release-a",
		ActivatedRevision: &activation,
	})
	if err != nil {
		t.Fatalf("CompileRule: %v", err)
	}
	want := overlay.Rule{
		ID:                "overlay-a",
		Collection:        "payment_routes",
		Scope:             overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey:         "route-a",
		Action:            overlay.ActionAdd,
		Content:           map[string]string{"endpoint": "blue"},
		ConfigRevision:    8,
		ReleaseOrderID:    "release-a",
		ActivatedRevision: &activation,
	}
	if !reflect.DeepEqual(rule, want) {
		t.Fatalf("compiled rule = %#v, want %#v", rule, want)
	}
}

func TestCompileRuleUsesModifyWhenBaseAndDesiredExist(t *testing.T) {
	base := catalog.ConfigurationRecord{
		Collection:  "payment_routes",
		Environment: "production",
		RecordKey:   "route-a",
		Data:        map[string]string{"endpoint": "base", "enabled": "true"},
	}
	desired := base
	desired.Data = map[string]string{"endpoint": "blue", "enabled": "true"}

	rule, err := overlay.CompileRule(overlay.RuleSpec{
		ID:         "overlay-a",
		Collection: "payment_routes",
		Scope:      overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey:  "route-a",
		Base:       &base,
		Desired:    &desired,
	})
	if err != nil {
		t.Fatalf("CompileRule: %v", err)
	}
	if rule.Action != overlay.ActionModify || !reflect.DeepEqual(rule.Content, desired.Data) {
		t.Fatalf("compiled rule = %#v, want full MODIFY content", rule)
	}
}

func TestCompileRuleUsesDeleteWhenDesiredIsMissing(t *testing.T) {
	base := catalog.ConfigurationRecord{
		Collection:  "payment_routes",
		Environment: "production",
		RecordKey:   "route-a",
		Data:        map[string]string{"endpoint": "base"},
	}

	rule, err := overlay.CompileRule(overlay.RuleSpec{
		ID:         "overlay-a",
		Collection: "payment_routes",
		Scope:      overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey:  "route-a",
		Base:       &base,
	})
	if err != nil {
		t.Fatalf("CompileRule: %v", err)
	}
	if rule.Action != overlay.ActionDelete || rule.Content != nil {
		t.Fatalf("compiled rule = %#v, want DELETE with nil content", rule)
	}
}

func TestCompileRuleUsesModifyWhenDesiredMatchesBase(t *testing.T) {
	base := catalog.ConfigurationRecord{
		Collection:  "payment_routes",
		Environment: "production",
		RecordKey:   "route-a",
		Data:        map[string]string{"endpoint": "base", "enabled": "true"},
	}
	desired := base
	desired.ConfigRevision = 99

	rule, err := overlay.CompileRule(overlay.RuleSpec{
		ID:         "overlay-a",
		Collection: "payment_routes",
		Scope:      overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey:  "route-a",
		Base:       &base,
		Desired:    &desired,
	})
	if err != nil {
		t.Fatalf("CompileRule: %v", err)
	}
	if rule.Action != overlay.ActionModify || !reflect.DeepEqual(rule.Content, desired.Data) {
		t.Fatalf("compiled rule = %#v, want base-relative MODIFY", rule)
	}
}

func TestCompileRuleRejectsRecordIdentityMismatch(t *testing.T) {
	desired := catalog.ConfigurationRecord{
		Collection:  "other_collection",
		Environment: "production",
		RecordKey:   "route-a",
		Data:        map[string]string{"endpoint": "blue"},
	}

	_, err := overlay.CompileRule(overlay.RuleSpec{
		ID:         "overlay-a",
		Collection: "payment_routes",
		Scope:      overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey:  "route-a",
		Desired:    &desired,
	})
	if !errors.Is(err, overlay.ErrInvalidSpec) {
		t.Fatalf("CompileRule error = %v, want ErrInvalidSpec", err)
	}
}

func TestCompileRuleCanonicalizesRolloutRanges(t *testing.T) {
	desired := catalog.ConfigurationRecord{
		Collection:  "payment_routes",
		Environment: "production",
		RecordKey:   "route-a",
		Data:        map[string]string{"endpoint": "blue"},
	}

	rule, err := overlay.CompileRule(overlay.RuleSpec{
		ID:            "overlay-a",
		Collection:    "payment_routes",
		Scope:         overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey:     "route-a",
		Desired:       &desired,
		RolloutRanges: []overlay.BucketRange{{Start: 50, End: 99}, {Start: 0, End: 49}},
	})
	if err != nil {
		t.Fatalf("CompileRule: %v", err)
	}
	want := []overlay.BucketRange{{Start: 0, End: 49}, {Start: 50, End: 99}}
	if !reflect.DeepEqual(rule.RolloutRanges, want) {
		t.Fatalf("rollout ranges = %#v, want %#v", rule.RolloutRanges, want)
	}
}

func TestCompileRuleOwnsMutableInput(t *testing.T) {
	activation := catalog.ConfigRevision(8)
	desired := catalog.ConfigurationRecord{
		Collection:  "payment_routes",
		Environment: "production",
		RecordKey:   "route-a",
		Data:        map[string]string{"endpoint": "blue"},
	}
	ranges := []overlay.BucketRange{{Start: 0, End: 49}}

	rule, err := overlay.CompileRule(overlay.RuleSpec{
		ID:                "overlay-a",
		Collection:        "payment_routes",
		Scope:             overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey:         "route-a",
		Desired:           &desired,
		RolloutRanges:     ranges,
		ActivatedRevision: &activation,
	})
	if err != nil {
		t.Fatalf("CompileRule: %v", err)
	}
	desired.Data["endpoint"] = "mutated"
	ranges[0].End = 99
	activation = 99

	if rule.Content["endpoint"] != "blue" || rule.RolloutRanges[0].End != 49 || *rule.ActivatedRevision != 8 {
		t.Fatalf("compiled rule retained mutable input aliases: %#v", rule)
	}
}

package domain_test

import (
	"errors"
	"testing"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
)

func TestComputeDigestUsesStableSemanticOverlayContent(t *testing.T) {
	activeAtTwo := catalog.ConfigRevision(2)
	activeAtThree := catalog.ConfigRevision(3)
	rules := []overlay.Rule{
		{
			ID:                "db-id-b",
			Collection:        "payment_routes",
			Scope:             overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
			RecordKey:         "route-b",
			Action:            overlay.ActionAdd,
			Content:           map[string]string{"endpoint": "blue"},
			RolloutRanges:     []overlay.BucketRange{{Start: 25, End: 49}, {Start: 0, End: 24}},
			ConfigRevision:    99,
			ActivatedRevision: &activeAtThree,
		},
		{
			ID:                "db-id-a",
			Collection:        "payment_routes",
			Scope:             overlay.Scope{Region: "cn", Environment: "production"},
			RecordKey:         "route-a",
			Action:            overlay.ActionModify,
			Content:           map[string]string{"endpoint": "environment", "enabled": "true"},
			ConfigRevision:    98,
			ActivatedRevision: &activeAtTwo,
		},
	}

	digest, err := overlay.ComputeDigest(rules)
	if err != nil {
		t.Fatalf("ComputeDigest: %v", err)
	}
	if digest.Algorithm != "SHA-256" || digest.Value != "0f3c3d3cde8fba392075df4f1d841bce87808f3d9432845fd66ddd9719bae96e" {
		t.Fatalf("digest = %+v, want stable SHA-256 literal", digest)
	}
}

func TestComputeDigestRejectsDuplicateScopeRecord(t *testing.T) {
	active := catalog.ConfigRevision(2)
	rule := overlay.Rule{
		ID:                "first",
		Collection:        "payment_routes",
		Scope:             overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey:         "route-a",
		Action:            overlay.ActionModify,
		Content:           map[string]string{"endpoint": "blue"},
		ActivatedRevision: &active,
	}
	duplicate := rule
	duplicate.ID = "second"

	_, err := overlay.ComputeDigest([]overlay.Rule{rule, duplicate})
	if !errors.Is(err, overlay.ErrInvariant) {
		t.Fatalf("ComputeDigest error = %v, want ErrInvariant", err)
	}
}

package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
)

// ComputeDigest hashes only semantic OverlayRule content. Storage identity,
// ownership, timestamps, and revision numbers deliberately do not participate.
func ComputeDigest(rules []Rule) (catalog.Digest, error) {
	ordered := append([]Rule(nil), rules...)
	sort.Slice(ordered, func(left, right int) bool {
		return ruleIdentity(ordered[left]) < ruleIdentity(ordered[right])
	})

	payload := make([]any, len(ordered))
	for index, rule := range ordered {
		if index > 0 && ruleIdentity(ordered[index-1]) == ruleIdentity(rule) {
			return catalog.Digest{}, fmt.Errorf("%w: duplicate rule for %s", ErrInvariant, ruleIdentity(rule))
		}
		ranges, err := canonicalRanges(rule.RolloutRanges)
		if err != nil {
			return catalog.Digest{}, fmt.Errorf("compute overlay digest: rule %q: %w", rule.ID, err)
		}
		payload[index] = []any{
			rule.Collection,
			rule.Scope.Region,
			rule.Scope.Environment,
			rule.Scope.Stage,
			rule.RecordKey,
			rule.Action,
			rule.Content,
			ranges,
			rule.ActivatedRevision != nil,
			rule.ExpiredRevision != nil,
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return catalog.Digest{}, fmt.Errorf("compute overlay digest: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return catalog.Digest{Algorithm: "SHA-256", Value: hex.EncodeToString(sum[:])}, nil
}

func ruleIdentity(rule Rule) string {
	return rule.Collection + "\x00" + rule.Scope.Environment + "\x00" + rule.Scope.Region + "\x00" + rule.Scope.Stage + "\x00" + rule.RecordKey
}

func canonicalRanges(ranges []BucketRange) ([][]int32, error) {
	ordered, err := normalizeRanges(ranges)
	if err != nil {
		return nil, err
	}
	canonical := make([][]int32, len(ordered))
	for index, bucketRange := range ordered {
		canonical[index] = []int32{bucketRange.Start, bucketRange.End}
	}
	return canonical, nil
}

func normalizeRanges(ranges []BucketRange) ([]BucketRange, error) {
	ordered := append([]BucketRange(nil), ranges...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].Start != ordered[right].Start {
			return ordered[left].Start < ordered[right].Start
		}
		return ordered[left].End < ordered[right].End
	})
	for index, bucketRange := range ordered {
		if bucketRange.Start < 0 || bucketRange.End > 99 || bucketRange.Start > bucketRange.End {
			return nil, fmt.Errorf("invalid bucket range [%d,%d]", bucketRange.Start, bucketRange.End)
		}
		if index > 0 && ordered[index-1].End >= bucketRange.Start {
			return nil, fmt.Errorf("overlapping bucket ranges [%d,%d] and [%d,%d]", ordered[index-1].Start, ordered[index-1].End, bucketRange.Start, bucketRange.End)
		}
	}
	return ordered, nil
}

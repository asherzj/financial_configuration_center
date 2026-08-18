package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidRollout = errors.New("invalid percentage rollout")

// ClientBucket is the protocol-stable client assignment used by Config
// Server and exposed by the SDK for diagnostics.
func ClientBucket(consumerID, clientID string) (int32, error) {
	if consumerID == "" || clientID == "" ||
		consumerID != strings.TrimSpace(consumerID) || clientID != strings.TrimSpace(clientID) ||
		strings.ContainsRune(consumerID, '\x00') || strings.ContainsRune(clientID, '\x00') {
		return 0, fmt.Errorf("%w: canonical consumer and client identifiers are required", ErrInvalidRollout)
	}
	payload := make([]byte, 0, len(consumerID)+1+len(clientID))
	payload = append(payload, consumerID...)
	payload = append(payload, 0)
	payload = append(payload, clientID...)
	sum := sha256.Sum256(payload)
	return int32(binary.BigEndian.Uint64(sum[:8]) % 100), nil
}

// ExpandRolloutRanges adds a new, disjoint percentage slice and returns one
// canonical monotonic union. Adjacent ranges are merged; overlap is rejected.
func ExpandRolloutRanges(current, addition []BucketRange) ([]BucketRange, error) {
	if len(addition) == 0 {
		return nil, fmt.Errorf("%w: at least one added range is required", ErrInvalidRollout)
	}
	canonicalCurrent, err := normalizeRanges(current)
	if err != nil {
		return nil, fmt.Errorf("%w: current ranges: %v", ErrInvalidRollout, err)
	}
	canonicalAddition, err := normalizeRanges(addition)
	if err != nil {
		return nil, fmt.Errorf("%w: added ranges: %v", ErrInvalidRollout, err)
	}
	for _, existing := range canonicalCurrent {
		for _, added := range canonicalAddition {
			if existing.Start <= added.End && added.Start <= existing.End {
				return nil, fmt.Errorf("%w: added range [%d,%d] overlaps executed range [%d,%d]", ErrInvalidRollout, added.Start, added.End, existing.Start, existing.End)
			}
		}
	}
	combined := append(append([]BucketRange(nil), canonicalCurrent...), canonicalAddition...)
	combined, err = normalizeRanges(combined)
	if err != nil {
		return nil, fmt.Errorf("%w: combined ranges: %v", ErrInvalidRollout, err)
	}
	merged := make([]BucketRange, 0, len(combined))
	for _, candidate := range combined {
		if len(merged) > 0 && merged[len(merged)-1].End+1 == candidate.Start {
			merged[len(merged)-1].End = candidate.End
			continue
		}
		merged = append(merged, candidate)
	}
	return merged, nil
}

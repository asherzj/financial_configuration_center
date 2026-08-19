package finconfig

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ConfigRevision is the monotonic configuration watermark exposed by the
// public SDK. It is intentionally owned by the SDK rather than a server or
// control-plane domain package.
type ConfigRevision uint64

func clientBucket(consumerID, clientID string) (int32, error) {
	if consumerID == "" || clientID == "" ||
		consumerID != strings.TrimSpace(consumerID) || clientID != strings.TrimSpace(clientID) ||
		strings.ContainsRune(consumerID, '\x00') || strings.ContainsRune(clientID, '\x00') {
		return 0, errors.New("invalid percentage rollout: canonical consumer and client identifiers are required")
	}
	payload := make([]byte, 0, len(consumerID)+1+len(clientID))
	payload = append(payload, consumerID...)
	payload = append(payload, 0)
	payload = append(payload, clientID...)
	sum := sha256.Sum256(payload)
	return int32(binary.BigEndian.Uint64(sum[:8]) % 100), nil
}

func computeBaseDigest(records []Record) (string, error) {
	ordered := append([]Record(nil), records...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].Key < ordered[right].Key })
	payload := make([]any, len(ordered))
	for index, record := range ordered {
		if record.Key == "" {
			return "", errors.New("record key is required")
		}
		if index > 0 && record.Key == ordered[index-1].Key {
			return "", fmt.Errorf("duplicate record key %q", record.Key)
		}
		values := record.Values
		if values == nil {
			values = map[string]string{}
		}
		payload[index] = []any{record.Key, values}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode digest input: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

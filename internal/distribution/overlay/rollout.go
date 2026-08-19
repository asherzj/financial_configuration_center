package overlay

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

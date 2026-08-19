package runtime

import (
	"errors"
	"fmt"
	"strings"

	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	"github.com/google/uuid"
)

type uuidGenerator func() (uuid.UUID, error)

func NewProcessIdentitySeed(serverEpoch string) (snapshot.IdentitySeed, error) {
	return newProcessIdentitySeed(serverEpoch, uuid.NewV7)
}

func newProcessIdentitySeed(serverEpoch string, generate uuidGenerator) (snapshot.IdentitySeed, error) {
	serverEpoch = strings.TrimSpace(serverEpoch)
	epoch, err := uuid.Parse(serverEpoch)
	if err != nil || epoch == uuid.Nil {
		return snapshot.IdentitySeed{}, errors.New("Config Server deployment epoch must be a non-zero UUID")
	}
	if generate == nil {
		return snapshot.IdentitySeed{}, errors.New("Config Server process identity generator is required")
	}
	serverInstance, err := generateUUIDv7(generate, "server instance")
	if err != nil {
		return snapshot.IdentitySeed{}, err
	}
	snapshotInstance, err := generateUUIDv7(generate, "snapshot instance")
	if err != nil {
		return snapshot.IdentitySeed{}, err
	}
	if serverInstance == snapshotInstance {
		return snapshot.IdentitySeed{}, errors.New("Config Server process identities must be independent")
	}
	return snapshot.IdentitySeed{
		ServerEpoch: epoch.String(), ServerInstanceID: serverInstance.String(), SnapshotInstance: snapshotInstance.String(),
	}, nil
}

func generateUUIDv7(generate uuidGenerator, name string) (uuid.UUID, error) {
	value, err := generate()
	if err != nil {
		return uuid.Nil, fmt.Errorf("generate Config Server %s: %w", name, err)
	}
	if value == uuid.Nil || value.Version() != 7 {
		return uuid.Nil, fmt.Errorf("generate Config Server %s: UUIDv7 is required", name)
	}
	return value, nil
}

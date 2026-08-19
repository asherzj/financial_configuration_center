package runtime

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestNewProcessIdentitySeedGeneratesIndependentUUIDv7ValuesPerStart(t *testing.T) {
	t.Parallel()
	epoch := "018f47cb-42f8-7fb2-a4af-0b0bd6dd98c1"
	first, err := NewProcessIdentitySeed(epoch)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewProcessIdentitySeed(epoch)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"first server": first.ServerInstanceID, "first snapshot": first.SnapshotInstance,
		"second server": second.ServerInstanceID, "second snapshot": second.SnapshotInstance,
	} {
		parsed, err := uuid.Parse(value)
		if err != nil || parsed.Version() != 7 || parsed == uuid.Nil {
			t.Fatalf("%s identity = %q, %v", name, value, err)
		}
	}
	if first.ServerEpoch != epoch || second.ServerEpoch != epoch {
		t.Fatalf("deployment epoch changed across normal restart: %+v / %+v", first, second)
	}
	if first.ServerInstanceID == first.SnapshotInstance || first.ServerInstanceID == second.ServerInstanceID || first.SnapshotInstance == second.SnapshotInstance {
		t.Fatalf("per-process identities were reused: %+v / %+v", first, second)
	}
}

func TestProcessIdentitySeedFailsClosedOnInvalidEpochOrGenerator(t *testing.T) {
	t.Parallel()
	v7a := uuid.MustParse("018f47cb-42f8-7fb2-a4af-0b0bd6dd98c2")
	v7b := uuid.MustParse("018f47cb-42f8-7fb2-a4af-0b0bd6dd98c3")
	values := []uuid.UUID{v7a, v7b}
	seed, err := newProcessIdentitySeed("018f47cb-42f8-7fb2-a4af-0b0bd6dd98c1", func() (uuid.UUID, error) {
		value := values[0]
		values = values[1:]
		return value, nil
	})
	if err != nil || seed.ServerInstanceID != v7a.String() || seed.SnapshotInstance != v7b.String() {
		t.Fatalf("deterministic identity seed = %+v, %v", seed, err)
	}

	tests := []struct {
		name      string
		epoch     string
		generator uuidGenerator
	}{
		{name: "blank epoch", epoch: " ", generator: uuid.NewV7},
		{name: "nil epoch", epoch: uuid.Nil.String(), generator: uuid.NewV7},
		{name: "invalid epoch", epoch: "not-a-uuid", generator: uuid.NewV7},
		{name: "nil generator", epoch: v7a.String()},
		{name: "generation error", epoch: v7a.String(), generator: func() (uuid.UUID, error) { return uuid.Nil, errors.New("entropy unavailable") }},
		{name: "nil generated", epoch: v7a.String(), generator: func() (uuid.UUID, error) { return uuid.Nil, nil }},
		{name: "non-v7 generated", epoch: v7a.String(), generator: func() (uuid.UUID, error) { return uuid.MustParse("00000000-0000-4000-8000-000000000001"), nil }},
		{name: "reused generated", epoch: v7a.String(), generator: func() (uuid.UUID, error) { return v7b, nil }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := newProcessIdentitySeed(test.epoch, test.generator); err == nil {
				t.Fatal("invalid process identity inputs were accepted")
			}
		})
	}
}

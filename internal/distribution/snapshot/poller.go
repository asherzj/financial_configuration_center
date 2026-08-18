package snapshot

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
)

type VersionSource interface {
	LoadVersions(context.Context, string) (map[string]catalog.ConfigRevision, error)
}

type SnapshotRefresher interface {
	Current() *Snapshot
	Refresh(context.Context, string) (RefreshResult, error)
}

type VersionPollerOptions struct {
	Environment string
	Interval    time.Duration
}

type PollResult struct {
	Refreshed  bool
	Generation uint64
}

type VersionPoller struct {
	manager     SnapshotRefresher
	source      VersionSource
	environment string
	interval    time.Duration
}

func NewVersionPoller(manager SnapshotRefresher, source VersionSource, options VersionPollerOptions) (*VersionPoller, error) {
	options.Environment = strings.TrimSpace(options.Environment)
	if manager == nil || source == nil || options.Environment == "" || options.Interval <= 0 {
		return nil, errors.New("new version poller: manager, source, environment, and positive interval are required")
	}
	return &VersionPoller{manager: manager, source: source, environment: options.Environment, interval: options.Interval}, nil
}

func (poller *VersionPoller) PollOnce(ctx context.Context) (PollResult, error) {
	authority, err := poller.source.LoadVersions(ctx, poller.environment)
	if err != nil {
		return PollResult{}, fmt.Errorf("poll configuration versions: %w", err)
	}
	for name, revision := range authority {
		if strings.TrimSpace(name) == "" || revision == 0 {
			return PollResult{}, errors.New("poll configuration versions: authority contains an invalid version")
		}
	}
	current := poller.manager.Current()
	if current != nil && current.Environment() == poller.environment && sameVersions(current, authority) {
		return PollResult{Generation: current.Identity().Generation}, nil
	}
	refreshed, err := poller.manager.Refresh(ctx, poller.environment)
	if err != nil {
		return PollResult{}, fmt.Errorf("poll refresh snapshot: %w", err)
	}
	return PollResult{Refreshed: true, Generation: refreshed.Generation}, nil
}

func (poller *VersionPoller) Run(ctx context.Context) error {
	delay := poller.jitteredInterval()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			_, _ = poller.PollOnce(ctx)
			timer.Reset(poller.jitteredInterval())
		}
	}
}

func sameVersions(current *Snapshot, authority map[string]catalog.ConfigRevision) bool {
	names := current.CollectionNames()
	if len(names) != len(authority) {
		return false
	}
	for _, name := range names {
		revision, exists := current.CollectionVersion(name)
		if !exists || authority[name] != revision {
			return false
		}
	}
	return true
}

func (poller *VersionPoller) jitteredInterval() time.Duration {
	identity := poller.manager.Current().Identity()
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(identity.ServerInstanceID + "\x00" + poller.environment + "\x00" + fmt.Sprint(identity.Generation)))
	// 801 positions map to the inclusive range [-20%, +20%]. Including the
	// generation changes the offset after each successful publication.
	offset := int64(hash.Sum32()%801) - 400
	return poller.interval + time.Duration(int64(poller.interval)*offset/2000)
}

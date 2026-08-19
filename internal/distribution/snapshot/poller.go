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
	Submitted  bool
	Generation uint64
}

type RefreshScheduler interface {
	RefreshTargetSubmitter
	RequestRefresh() error
}

type VersionPoller struct {
	manager     SnapshotRefresher
	source      VersionSource
	scheduler   RefreshScheduler
	environment string
	interval    time.Duration
}

func NewVersionPoller(manager SnapshotRefresher, source VersionSource, scheduler RefreshScheduler, options VersionPollerOptions) (*VersionPoller, error) {
	options.Environment = strings.TrimSpace(options.Environment)
	if manager == nil || source == nil || scheduler == nil || options.Environment == "" || options.Interval <= 0 {
		return nil, errors.New("new version poller: manager, source, scheduler, environment, and positive interval are required")
	}
	return &VersionPoller{manager: manager, source: source, scheduler: scheduler, environment: options.Environment, interval: options.Interval}, nil
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
	force := current == nil || current.Environment() != poller.environment || authorityRemovedCollection(current, authority)
	if force {
		if err := poller.scheduler.RequestRefresh(); err != nil {
			return PollResult{}, fmt.Errorf("poll request full refresh: %w", err)
		}
		return PollResult{Submitted: true, Generation: snapshotGeneration(current)}, nil
	}
	targets := make([]RefreshTarget, 0, len(authority))
	for name, revision := range authority {
		targets = append(targets, RefreshTarget{Collection: name, MinRevision: revision})
	}
	if err := poller.scheduler.Submit(targets); err != nil {
		if !errors.Is(err, ErrRefreshQueueFull) {
			return PollResult{}, fmt.Errorf("poll submit version targets: %w", err)
		}
		if forceErr := poller.scheduler.RequestRefresh(); forceErr != nil {
			return PollResult{}, fmt.Errorf("poll request full refresh after target capacity: %w", forceErr)
		}
	}
	return PollResult{Submitted: true, Generation: snapshotGeneration(current)}, nil
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

func authorityRemovedCollection(current *Snapshot, authority map[string]catalog.ConfigRevision) bool {
	for _, name := range current.CollectionNames() {
		if _, exists := authority[name]; !exists {
			return true
		}
	}
	return false
}

func snapshotGeneration(current *Snapshot) uint64 {
	if current == nil {
		return 0
	}
	return current.Identity().Generation
}

func (poller *VersionPoller) jitteredInterval() time.Duration {
	identity := Identity{}
	if current := poller.manager.Current(); current != nil {
		identity = current.Identity()
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(identity.ServerInstanceID + "\x00" + poller.environment + "\x00" + fmt.Sprint(identity.Generation)))
	// 801 positions map to the inclusive range [-20%, +20%]. Including the
	// generation changes the offset after each successful publication.
	offset := int64(hash.Sum32()%801) - 400
	return poller.interval + time.Duration(int64(poller.interval)*offset/2000)
}

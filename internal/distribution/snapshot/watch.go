package snapshot

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	readmodel "github.com/asherzj/financial_configuration_center/internal/distribution/readmodel"
)

type PublicationPublisher interface {
	Publish(*Snapshot)
}

type VersionSignal struct {
	Collection string
	Revision   readmodel.ConfigRevision
	Digest     string
}

type UpdateEvent struct {
	EventID        string
	Identity       Identity
	Versions       []VersionSignal
	ResyncRequired bool
}

type WatchHubOptions struct {
	QueueSize      int
	MaxSubscribers int
}

type WatchHub struct {
	provider    interface{ Current() *Snapshot }
	options     WatchHubOptions
	mu          sync.Mutex
	nextID      uint64
	subscribers map[uint64]chan UpdateEvent
}

type WatchSubscription struct {
	Events <-chan UpdateEvent
	cancel func()
	once   sync.Once
}

func NewWatchHub(provider interface{ Current() *Snapshot }, options WatchHubOptions) (*WatchHub, error) {
	if provider == nil || options.QueueSize <= 0 || options.MaxSubscribers <= 0 {
		return nil, errors.New("new watch hub: provider and positive limits are required")
	}
	return &WatchHub{provider: provider, options: options, subscribers: make(map[uint64]chan UpdateEvent)}, nil
}

func (hub *WatchHub) Subscribe() (*WatchSubscription, error) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if len(hub.subscribers) >= hub.options.MaxSubscribers {
		return nil, errors.New("watch subscriber limit reached")
	}
	hub.nextID++
	id := hub.nextID
	events := make(chan UpdateEvent, hub.options.QueueSize)
	events <- snapshotEvent(hub.provider.Current(), false)
	hub.subscribers[id] = events
	return &WatchSubscription{Events: events, cancel: func() { hub.remove(id) }}, nil
}

func (subscription *WatchSubscription) Cancel() {
	if subscription == nil {
		return
	}
	subscription.once.Do(subscription.cancel)
}

func (hub *WatchHub) Publish(current *Snapshot) {
	if current == nil {
		return
	}
	event := snapshotEvent(current, false)
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for id, events := range hub.subscribers {
		select {
		case events <- event:
		default:
			for len(events) > 0 {
				<-events
			}
			resync := event
			resync.ResyncRequired = true
			events <- resync
			close(events)
			delete(hub.subscribers, id)
		}
	}
}

func (hub *WatchHub) remove(id uint64) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if events, exists := hub.subscribers[id]; exists {
		close(events)
		delete(hub.subscribers, id)
	}
}

func snapshotEvent(current *Snapshot, resync bool) UpdateEvent {
	if current == nil {
		return UpdateEvent{ResyncRequired: true}
	}
	identity := current.Identity()
	versions := make([]VersionSignal, 0, len(current.collections))
	for _, name := range current.CollectionNames() {
		revision, _ := current.CollectionVersion(name)
		digest, _ := current.CollectionDigest(name)
		versions = append(versions, VersionSignal{Collection: name, Revision: revision, Digest: digest.Value})
	}
	sort.Slice(versions, func(left, right int) bool { return versions[left].Collection < versions[right].Collection })
	return UpdateEvent{EventID: fmt.Sprintf("%s:%d", identity.SnapshotInstance, identity.Generation), Identity: identity, Versions: versions, ResyncRequired: resync}
}

var _ PublicationPublisher = (*WatchHub)(nil)

package finconfig

import (
	"context"
	"errors"
	"strings"
	"sync"
)

type UpdateEvent struct {
	Collection string
	Version    VersionView
	Identity   SnapshotIdentity
}

type UpdateHandler func(UpdateEvent)
type CancelFunc func()

type updateDelivery struct {
	handler UpdateHandler
	event   UpdateEvent
}

func (client *Client) Subscribe(collection string, handler UpdateHandler) (CancelFunc, error) {
	collection = strings.TrimSpace(collection)
	if collection == "" || handler == nil {
		return nil, errors.New("FinConfig subscription collection and handler are required")
	}
	client.lifecycleMu.Lock()
	defer client.lifecycleMu.Unlock()
	running := client.lifecycle == lifecycleRunning
	closed := client.lifecycle == lifecycleClosed || client.lifecycle == lifecycleClosing
	if closed {
		return nil, ErrClosed
	}
	if !running {
		return nil, ErrNotStarted
	}
	snapshot := client.current.Load()
	if _, exists := snapshot.collections[collection]; !exists {
		return nil, ErrCollectionNotFound
	}
	client.subscriptionMu.Lock()
	client.nextSubscription++
	id := client.nextSubscription
	if client.subscriptions[collection] == nil {
		client.subscriptions[collection] = make(map[uint64]UpdateHandler)
	}
	client.subscriptions[collection][id] = handler
	client.subscriptionMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			client.subscriptionMu.Lock()
			delete(client.subscriptions[collection], id)
			if len(client.subscriptions[collection]) == 0 {
				delete(client.subscriptions, collection)
			}
			client.subscriptionMu.Unlock()
		})
	}, nil
}

func (client *Client) publishUpdates(changes ChangeSet, snapshot *clientSnapshot) {
	for _, collection := range changes.Collections {
		client.subscriptionMu.RLock()
		handlers := make([]UpdateHandler, 0, len(client.subscriptions[collection]))
		for _, handler := range client.subscriptions[collection] {
			handlers = append(handlers, handler)
		}
		client.subscriptionMu.RUnlock()
		view := snapshot.collections[collection]
		event := UpdateEvent{Collection: collection, Identity: snapshot.identity, Version: VersionView{Collection: collection, Revision: view.revision, Digest: view.digest, Identity: snapshot.identity}}
		for _, handler := range handlers {
			select {
			case client.updates <- updateDelivery{handler: handler, event: event}:
			default:
				// The snapshot is already published. Dropping a saturated callback
				// notification cannot lose configuration state; consumers can query
				// the current version and the poll/watch loops continue to converge.
			}
		}
	}
}

func (client *Client) deliverUpdates(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case delivery := <-client.updates:
			invokeUpdateHandler(delivery.handler, delivery.event)
		}
	}
}

func invokeUpdateHandler(handler UpdateHandler, event UpdateEvent) {
	defer func() { _ = recover() }()
	handler(event)
}

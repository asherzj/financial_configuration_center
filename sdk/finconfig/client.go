// Package finconfig provides an immutable last-known-good configuration client.
package finconfig

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
)

type SnapshotIdentity struct {
	ServerEpoch      string
	ServerInstanceID string
	SnapshotInstance string
	Generation       uint64
}

type Version struct {
	Collection string
	Revision   catalog.ConfigRevision
	Digest     string
}

type SnapshotRequest struct {
	ConsumerID    string
	ClientID      string
	Region        string
	Environment   string
	Stage         string
	KnownVersions []Version
}

type Record struct {
	Key      string
	Revision catalog.ConfigRevision
	Values   map[string]string
}

type CollectionPayload struct {
	Name     string
	Revision catalog.ConfigRevision
	Digest   string
	Records  []Record
}

type SnapshotResponse struct {
	Identity           SnapshotIdentity
	Environment        string
	Collections        []CollectionPayload
	DeletedCollections []string
}

type Transport interface {
	GetSnapshot(context.Context, SnapshotRequest) (SnapshotResponse, error)
}

type WatchRequest struct {
	ConsumerID  string
	ClientID    string
	Region      string
	Environment string
	Stage       string
}

type WatchEvent struct {
	Identity       SnapshotIdentity
	ResyncRequired bool
}

type WatchTransport interface {
	Watch(context.Context, WatchRequest) (<-chan WatchEvent, error)
}

type Config struct {
	ConsumerID       string
	ClientID         string
	Region           string
	Environment      string
	Stage            string
	Transport        Transport
	PollInterval     time.Duration
	WatchEnabled     bool
	ReconnectBackoff time.Duration
}

var (
	ErrAlreadyStarted = errors.New("FinConfig client is already started")
	ErrClosed         = errors.New("FinConfig client is closed")
)

type lifecycleState uint8

const (
	lifecycleNew lifecycleState = iota
	lifecycleStarting
	lifecycleRunning
	lifecycleClosing
	lifecycleClosed
)

type ChangeSet struct {
	Before      SnapshotIdentity
	After       SnapshotIdentity
	Collections []string
}

type collectionSnapshot struct {
	revision catalog.ConfigRevision
	digest   string
	records  map[string]Record
}

type clientSnapshot struct {
	identity    SnapshotIdentity
	environment string
	collections map[string]collectionSnapshot
}

type Client struct {
	consumerID       string
	clientID         string
	region           string
	environment      string
	stage            string
	bucket           int32
	transport        Transport
	refreshMu        sync.Mutex
	callbackMu       sync.RWMutex
	callback         func(ChangeSet) error
	current          atomic.Pointer[clientSnapshot]
	pollInterval     time.Duration
	watchEnabled     bool
	reconnectBackoff time.Duration
	lifecycleMu      sync.Mutex
	lifecycle        lifecycleState
	cancel           context.CancelFunc
	closed           chan struct{}
}

func New(config Config) (*Client, error) {
	config.ConsumerID = strings.TrimSpace(config.ConsumerID)
	config.ClientID = strings.TrimSpace(config.ClientID)
	config.Region = strings.TrimSpace(config.Region)
	config.Environment = strings.TrimSpace(config.Environment)
	config.Stage = strings.TrimSpace(config.Stage)
	if config.ConsumerID == "" || config.ClientID == "" || config.Region == "" || config.Environment == "" || config.Transport == nil {
		return nil, errors.New("new FinConfig client: consumer, client, region, environment, and transport are required")
	}
	bucket, err := overlay.ClientBucket(config.ConsumerID, config.ClientID)
	if err != nil {
		return nil, fmt.Errorf("new FinConfig client: %w", err)
	}
	if config.PollInterval < 0 {
		return nil, errors.New("new FinConfig client: poll interval cannot be negative")
	}
	if config.PollInterval == 0 {
		config.PollInterval = 30 * time.Second
	}
	if config.ReconnectBackoff < 0 {
		return nil, errors.New("new FinConfig client: reconnect backoff cannot be negative")
	}
	if config.ReconnectBackoff == 0 {
		config.ReconnectBackoff = 100 * time.Millisecond
	}
	if config.WatchEnabled {
		if _, supported := config.Transport.(WatchTransport); !supported {
			return nil, errors.New("new FinConfig client: watch-enabled transport does not implement Watch")
		}
	}
	client := &Client{
		consumerID: config.ConsumerID, clientID: config.ClientID, region: config.Region,
		environment: config.Environment, stage: config.Stage, bucket: bucket,
		transport: config.Transport, pollInterval: config.PollInterval,
		watchEnabled: config.WatchEnabled, reconnectBackoff: config.ReconnectBackoff, closed: make(chan struct{}),
	}
	client.current.Store(&clientSnapshot{environment: config.Environment, collections: map[string]collectionSnapshot{}})
	return client, nil
}

func (client *Client) Start(ctx context.Context) error {
	client.lifecycleMu.Lock()
	switch client.lifecycle {
	case lifecycleClosed, lifecycleClosing:
		client.lifecycleMu.Unlock()
		return ErrClosed
	case lifecycleStarting, lifecycleRunning:
		client.lifecycleMu.Unlock()
		return ErrAlreadyStarted
	}
	client.lifecycle = lifecycleStarting
	client.lifecycleMu.Unlock()

	if err := client.Refresh(ctx); err != nil {
		client.lifecycleMu.Lock()
		closing := client.lifecycle == lifecycleClosing
		if !closing {
			client.lifecycle = lifecycleNew
		}
		client.lifecycleMu.Unlock()
		if closing {
			client.finishClosed()
			return ErrClosed
		}
		return err
	}

	client.lifecycleMu.Lock()
	if client.lifecycle == lifecycleClosing {
		client.lifecycleMu.Unlock()
		client.finishClosed()
		return ErrClosed
	}
	runContext, cancel := context.WithCancel(ctx)
	client.cancel = cancel
	client.lifecycle = lifecycleRunning
	client.lifecycleMu.Unlock()
	go client.run(runContext)
	return nil
}

func (client *Client) Close(ctx context.Context) error {
	client.lifecycleMu.Lock()
	switch client.lifecycle {
	case lifecycleClosed:
		client.lifecycleMu.Unlock()
		return nil
	case lifecycleNew:
		client.lifecycle = lifecycleClosed
		close(client.closed)
		client.lifecycleMu.Unlock()
		return nil
	case lifecycleStarting:
		client.lifecycle = lifecycleClosing
	case lifecycleRunning:
		client.lifecycle = lifecycleClosing
		client.cancel()
	}
	closed := client.closed
	client.lifecycleMu.Unlock()
	select {
	case <-closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (client *Client) run(ctx context.Context) {
	var loops sync.WaitGroup
	loops.Add(1)
	go func() {
		defer loops.Done()
		client.poll(ctx)
	}()
	if client.watchEnabled {
		loops.Add(1)
		go func() {
			defer loops.Done()
			client.watch(ctx)
		}()
	}
	loops.Wait()
	client.finishClosed()
}

func (client *Client) poll(ctx context.Context) {
	ticker := time.NewTicker(client.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = client.Refresh(ctx)
		}
	}
}

func (client *Client) watch(ctx context.Context) {
	transport := client.transport.(WatchTransport)
	request := WatchRequest{ConsumerID: client.consumerID, ClientID: client.clientID, Region: client.region, Environment: client.environment, Stage: client.stage}
	for {
		events, err := transport.Watch(ctx, request)
		if err != nil {
			if !waitForReconnect(ctx, client.reconnectBackoff) {
				return
			}
			continue
		}
		for {
			select {
			case <-ctx.Done():
				return
			case event, open := <-events:
				if !open {
					if !waitForReconnect(ctx, client.reconnectBackoff) {
						return
					}
					goto reconnect
				}
				current := client.Identity()
				if event.ResyncRequired || !sameInstance(event.Identity, current) || event.Identity.Generation > current.Generation {
					_ = client.Refresh(ctx)
				}
			}
		}
	reconnect:
	}
}

func waitForReconnect(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (client *Client) finishClosed() {
	client.lifecycleMu.Lock()
	defer client.lifecycleMu.Unlock()
	if client.lifecycle != lifecycleClosed {
		client.lifecycle = lifecycleClosed
		close(client.closed)
	}
}

// Refresh validates a complete candidate and invokes consumer validation before
// one atomic swap. Any failure retains the prior snapshot.
func (client *Client) Refresh(ctx context.Context) error {
	client.refreshMu.Lock()
	defer client.refreshMu.Unlock()

	before := client.current.Load()
	request := SnapshotRequest{
		ConsumerID: client.consumerID, ClientID: client.clientID, Region: client.region, Environment: client.environment, Stage: client.stage,
		KnownVersions: knownVersions(before),
	}
	response, err := client.transport.GetSnapshot(ctx, request)
	if err != nil {
		return fmt.Errorf("FinConfig refresh transport: %w", err)
	}
	if before.identity.Generation != 0 && !sameInstance(response.Identity, before.identity) && len(request.KnownVersions) > 0 {
		request.KnownVersions = nil
		response, err = client.transport.GetSnapshot(ctx, request)
		if err != nil {
			return fmt.Errorf("FinConfig full refresh after snapshot identity change: %w", err)
		}
	}
	candidate, err := buildCandidate(response, before)
	if err != nil {
		return err
	}
	changes := ChangeSet{Before: before.identity, After: candidate.identity, Collections: changedCollections(before, candidate)}
	client.callbackMu.RLock()
	callback := client.callback
	client.callbackMu.RUnlock()
	if callback != nil {
		if err := invokeBeforePublish(callback, changes); err != nil {
			return fmt.Errorf("FinConfig before-publish callback: %w", err)
		}
	}
	client.current.Store(candidate)
	return nil
}

// Bucket returns the stable 0..99 rollout assignment for diagnostics.
func (client *Client) Bucket() int32 { return client.bucket }

func invokeBeforePublish(callback func(ChangeSet) error, changes ChangeSet) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("callback panic: %v", recovered)
		}
	}()
	return callback(changes)
}

func (client *Client) SetBeforePublish(callback func(ChangeSet) error) {
	client.callbackMu.Lock()
	defer client.callbackMu.Unlock()
	client.callback = callback
}

func (client *Client) Identity() SnapshotIdentity { return client.current.Load().identity }

func (client *Client) GetByKey(collection, key string) (Record, bool) {
	current := client.current.Load()
	view, exists := current.collections[collection]
	if !exists {
		return Record{}, false
	}
	record, exists := view.records[key]
	if !exists {
		return Record{}, false
	}
	return cloneRecord(record), true
}

func buildCandidate(response SnapshotResponse, before *clientSnapshot) (*clientSnapshot, error) {
	identity := response.Identity
	if strings.TrimSpace(identity.ServerEpoch) == "" || strings.TrimSpace(identity.ServerInstanceID) == "" || strings.TrimSpace(identity.SnapshotInstance) == "" || identity.Generation == 0 {
		return nil, errors.New("FinConfig candidate: complete nonzero snapshot identity is required")
	}
	if response.Environment != before.environment {
		return nil, fmt.Errorf("FinConfig candidate: environment %q does not match %q", response.Environment, before.environment)
	}
	if sameInstance(identity, before.identity) && identity.Generation < before.identity.Generation {
		return nil, fmt.Errorf("FinConfig candidate: generation regressed from %d to %d", before.identity.Generation, identity.Generation)
	}
	reset := before.identity.Generation == 0 || !sameInstance(identity, before.identity)
	candidate := &clientSnapshot{identity: identity, environment: response.Environment, collections: make(map[string]collectionSnapshot, len(before.collections)+len(response.Collections))}
	if !reset {
		for name, collection := range before.collections {
			candidate.collections[name] = collection
		}
	}
	deleted := make(map[string]struct{}, len(response.DeletedCollections))
	for _, name := range response.DeletedCollections {
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("FinConfig candidate: deleted collection name is required")
		}
		if _, duplicate := deleted[name]; duplicate {
			return nil, fmt.Errorf("FinConfig candidate: duplicate deleted collection %q", name)
		}
		deleted[name] = struct{}{}
		delete(candidate.collections, name)
	}
	payloadSeen := make(map[string]struct{}, len(response.Collections))
	for _, payload := range response.Collections {
		if strings.TrimSpace(payload.Name) == "" || payload.Revision == 0 {
			return nil, errors.New("FinConfig candidate: collection name and revision are required")
		}
		if _, duplicate := deleted[payload.Name]; duplicate {
			return nil, fmt.Errorf("FinConfig candidate: collection %q is both changed and deleted", payload.Name)
		}
		if _, duplicate := payloadSeen[payload.Name]; duplicate {
			return nil, fmt.Errorf("FinConfig candidate: duplicate collection %q", payload.Name)
		}
		payloadSeen[payload.Name] = struct{}{}
		if !validSHA256(payload.Digest) {
			return nil, fmt.Errorf("FinConfig candidate: collection %q has invalid digest", payload.Name)
		}
		view := collectionSnapshot{revision: payload.Revision, digest: payload.Digest, records: make(map[string]Record, len(payload.Records))}
		domainRecords := make([]catalog.ConfigurationRecord, len(payload.Records))
		for index, source := range payload.Records {
			if source.Key == "" || source.Revision == 0 || source.Revision > payload.Revision {
				return nil, fmt.Errorf("FinConfig candidate: collection %q has invalid record identity or revision", payload.Name)
			}
			if _, duplicate := view.records[source.Key]; duplicate {
				return nil, fmt.Errorf("FinConfig candidate: collection %q repeats record %q", payload.Name, source.Key)
			}
			record := cloneRecord(source)
			view.records[record.Key] = record
			domainRecords[index] = catalog.ConfigurationRecord{RecordKey: record.Key, Data: record.Values}
		}
		digest, err := catalog.ComputeBaseDigest(domainRecords)
		if err != nil {
			return nil, fmt.Errorf("FinConfig candidate: collection %q digest input: %w", payload.Name, err)
		}
		if digest.Value != payload.Digest {
			return nil, fmt.Errorf("FinConfig candidate: collection %q digest mismatch", payload.Name)
		}
		candidate.collections[payload.Name] = view
	}
	return candidate, nil
}

func knownVersions(snapshot *clientSnapshot) []Version {
	versions := make([]Version, 0, len(snapshot.collections))
	for name, collection := range snapshot.collections {
		versions = append(versions, Version{Collection: name, Revision: collection.revision, Digest: collection.digest})
	}
	sort.Slice(versions, func(left, right int) bool { return versions[left].Collection < versions[right].Collection })
	return versions
}

func changedCollections(before, after *clientSnapshot) []string {
	changed := make([]string, 0, len(before.collections)+len(after.collections))
	seen := make(map[string]struct{}, len(before.collections)+len(after.collections))
	for name, current := range after.collections {
		previous, existed := before.collections[name]
		if !existed || previous.revision != current.revision || previous.digest != current.digest {
			changed = append(changed, name)
		}
		seen[name] = struct{}{}
	}
	for name := range before.collections {
		if _, exists := seen[name]; !exists {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed
}

func sameInstance(left, right SnapshotIdentity) bool {
	return left.ServerEpoch == right.ServerEpoch && left.ServerInstanceID == right.ServerInstanceID && left.SnapshotInstance == right.SnapshotInstance
}

func validSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func cloneRecord(record Record) Record {
	source := record.Values
	record.Values = make(map[string]string, len(source))
	for key, value := range source {
		record.Values[key] = value
	}
	return record
}

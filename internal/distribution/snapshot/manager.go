package snapshot

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
)

type Source interface {
	LoadEnvironment(context.Context, string) ([]CollectionInput, error)
}

type Clock interface {
	Now() time.Time
}

type IdentitySeed struct {
	ServerEpoch      string
	ServerInstanceID string
	SnapshotInstance string
}

type Identity struct {
	ServerEpoch      string
	ServerInstanceID string
	SnapshotInstance string
	Generation       uint64
	PublishedAt      time.Time
}

type CollectionInput struct {
	Definition catalog.CollectionDefinition
	Models     []catalog.CompiledModel
	Version    catalog.ConfigRevision
	Records    []catalog.ConfigurationRecord
}

type RefreshResult struct {
	Generation      uint64
	CollectionCount int
}

type collectionView struct {
	definition catalog.CollectionDefinition
	version    catalog.ConfigRevision
	digest     catalog.Digest
	records    map[string]catalog.ConfigurationRecord
	ordered    []string
}

// Snapshot is immutable after construction. Every method returning nested
// mutable data returns a deep copy.
type Snapshot struct {
	identity    Identity
	environment string
	collections map[string]collectionView
	models      map[string]catalog.CompiledModel
}

type Manager struct {
	source    Source
	seed      IdentitySeed
	clock     Clock
	mu        sync.Mutex
	value     atomic.Pointer[Snapshot]
	publisher PublicationPublisher
}

func NewManager(source Source, seed IdentitySeed, clock Clock) (*Manager, error) {
	if source == nil || clock == nil {
		return nil, errors.New("new snapshot manager: source and clock are required")
	}
	seed.ServerEpoch = strings.TrimSpace(seed.ServerEpoch)
	seed.ServerInstanceID = strings.TrimSpace(seed.ServerInstanceID)
	seed.SnapshotInstance = strings.TrimSpace(seed.SnapshotInstance)
	if seed.ServerEpoch == "" || seed.ServerInstanceID == "" || seed.SnapshotInstance == "" {
		return nil, errors.New("new snapshot manager: complete identity seed is required")
	}
	manager := &Manager{source: source, seed: seed, clock: clock}
	manager.value.Store(&Snapshot{
		identity: Identity{
			ServerEpoch: seed.ServerEpoch, ServerInstanceID: seed.ServerInstanceID,
			SnapshotInstance: seed.SnapshotInstance, PublishedAt: clock.Now().UTC(),
		},
		collections: map[string]collectionView{},
		models:      map[string]catalog.CompiledModel{},
	})
	return manager, nil
}

func (manager *Manager) Current() *Snapshot { return manager.value.Load() }

// Refresh builds a complete candidate before one atomic pointer swap. Any
// source, validation, or digest error leaves the previous snapshot untouched.
func (manager *Manager) Refresh(ctx context.Context, environment string) (RefreshResult, error) {
	environment = strings.TrimSpace(environment)
	if environment == "" {
		return RefreshResult{}, errors.New("refresh snapshot: environment is required")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()

	inputs, err := manager.source.LoadEnvironment(ctx, environment)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("refresh snapshot source: %w", err)
	}
	previous := manager.Current()
	candidate, err := buildSnapshot(manager.seed, previous.identity.Generation+1, manager.clock.Now().UTC(), environment, inputs)
	if err != nil {
		return RefreshResult{}, err
	}
	manager.value.Store(candidate)
	if manager.publisher != nil {
		manager.publisher.Publish(candidate)
	}
	return RefreshResult{Generation: candidate.identity.Generation, CollectionCount: len(candidate.collections)}, nil
}

func (manager *Manager) SetPublisher(publisher PublicationPublisher) error {
	if publisher == nil {
		return errors.New("snapshot publisher is required")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.publisher != nil {
		return errors.New("snapshot publisher is already configured")
	}
	manager.publisher = publisher
	return nil
}

func (snapshot *Snapshot) Identity() Identity { return snapshot.identity }

func (snapshot *Snapshot) Environment() string { return snapshot.environment }

func (snapshot *Snapshot) CollectionNames() []string {
	names := make([]string, 0, len(snapshot.collections))
	for name := range snapshot.collections {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (snapshot *Snapshot) Model(code string) (catalog.CompiledModel, bool) {
	model, exists := snapshot.models[code]
	return model, exists
}

func (snapshot *Snapshot) CollectionVersion(collection string) (catalog.ConfigRevision, bool) {
	view, exists := snapshot.collections[collection]
	return view.version, exists
}

func (snapshot *Snapshot) Definition(collection string) (catalog.CollectionDefinition, bool) {
	view, exists := snapshot.collections[collection]
	if !exists {
		return catalog.CollectionDefinition{}, false
	}
	return view.definition, true
}

func (snapshot *Snapshot) CollectionDigest(collection string) (catalog.Digest, bool) {
	view, exists := snapshot.collections[collection]
	return view.digest, exists
}

func (snapshot *Snapshot) Record(collection, recordKey string) (catalog.ConfigurationRecord, bool) {
	view, exists := snapshot.collections[collection]
	if !exists {
		return catalog.ConfigurationRecord{}, false
	}
	record, exists := view.records[recordKey]
	if !exists {
		return catalog.ConfigurationRecord{}, false
	}
	return cloneRecord(record), true
}

func (snapshot *Snapshot) Records(collection string) []catalog.ConfigurationRecord {
	view, exists := snapshot.collections[collection]
	if !exists {
		return nil
	}
	records := make([]catalog.ConfigurationRecord, len(view.ordered))
	for index, key := range view.ordered {
		records[index] = cloneRecord(view.records[key])
	}
	return records
}

func buildSnapshot(seed IdentitySeed, generation uint64, publishedAt time.Time, environment string, inputs []CollectionInput) (*Snapshot, error) {
	candidate := &Snapshot{
		identity: Identity{
			ServerEpoch: seed.ServerEpoch, ServerInstanceID: seed.ServerInstanceID,
			SnapshotInstance: seed.SnapshotInstance, Generation: generation, PublishedAt: publishedAt,
		},
		environment: environment,
		collections: make(map[string]collectionView, len(inputs)),
		models:      make(map[string]catalog.CompiledModel),
	}
	for _, input := range inputs {
		name := input.Definition.Name()
		if name == "" || input.Version == 0 {
			return nil, errors.New("build snapshot: collection identity and positive version are required")
		}
		if _, duplicate := candidate.collections[name]; duplicate {
			return nil, fmt.Errorf("build snapshot: duplicate collection %q", name)
		}
		view := collectionView{
			definition: input.Definition,
			version:    input.Version,
			records:    make(map[string]catalog.ConfigurationRecord, len(input.Records)),
			ordered:    make([]string, 0, len(input.Records)),
		}
		for _, sourceRecord := range input.Records {
			if sourceRecord.Collection != name || sourceRecord.Environment != environment || sourceRecord.ConfigRevision == 0 || sourceRecord.ConfigRevision > input.Version {
				return nil, fmt.Errorf("build snapshot: record %q identity or revision is invalid", sourceRecord.RecordKey)
			}
			canonical, err := input.Definition.NewRecord(environment, sourceRecord.Data)
			if err != nil {
				return nil, fmt.Errorf("build snapshot: record %q: %w", sourceRecord.RecordKey, err)
			}
			if canonical.RecordKey != sourceRecord.RecordKey {
				return nil, fmt.Errorf("build snapshot: record key %q does not match canonical key %q", sourceRecord.RecordKey, canonical.RecordKey)
			}
			if _, duplicate := view.records[sourceRecord.RecordKey]; duplicate {
				return nil, fmt.Errorf("build snapshot: duplicate record key %q", sourceRecord.RecordKey)
			}
			canonical.ConfigRevision = sourceRecord.ConfigRevision
			view.records[canonical.RecordKey] = canonical
			view.ordered = append(view.ordered, canonical.RecordKey)
		}
		sort.Strings(view.ordered)
		records := make([]catalog.ConfigurationRecord, len(view.ordered))
		for index, key := range view.ordered {
			records[index] = view.records[key]
		}
		digest, err := catalog.ComputeBaseDigest(records)
		if err != nil {
			return nil, fmt.Errorf("build snapshot: collection %q digest: %w", name, err)
		}
		view.digest = digest
		candidate.collections[name] = view

		for _, model := range input.Models {
			if model.Collection() != name {
				return nil, fmt.Errorf("build snapshot: model %q points to another collection", model.Code())
			}
			if _, duplicate := candidate.models[model.Code()]; duplicate {
				return nil, fmt.Errorf("build snapshot: duplicate model %q", model.Code())
			}
			candidate.models[model.Code()] = model
		}
	}
	return candidate, nil
}

func cloneRecord(record catalog.ConfigurationRecord) catalog.ConfigurationRecord {
	source := record.Data
	record.Data = make(map[string]string, len(source))
	for key, value := range source {
		record.Data[key] = value
	}
	return record
}

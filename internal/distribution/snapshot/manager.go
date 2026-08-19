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
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
)

type Source interface {
	LoadEnvironment(context.Context, string) ([]CollectionInput, error)
}

// PartialSource returns one repeatable-read environment view while preserving
// collection-local load failures for dependency-group fallback.
type PartialSource interface {
	LoadEnvironmentPartial(context.Context, string) (EnvironmentLoad, error)
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
	Definition          catalog.CollectionDefinition
	Models              []catalog.CompiledModel
	SubscribedConsumers []string
	Version             catalog.ConfigRevision
	Cursor              uint64
	Records             []catalog.ConfigurationRecord
	OverlayRules        []overlay.Rule
}

type EnvironmentLoad struct {
	Inputs   []CollectionInput
	Failures map[string]error
}

type DependencyGroupFailure struct {
	Collections []string
	Reason      string
}

type RefreshResult struct {
	Generation      uint64
	CollectionCount int
	FailedGroups    []DependencyGroupFailure
}

type collectionView struct {
	definition          catalog.CollectionDefinition
	version             catalog.ConfigRevision
	cursor              uint64
	digest              catalog.Digest
	records             map[string]catalog.ConfigurationRecord
	ordered             []string
	overlayRules        []overlay.Rule
	subscribedConsumers []string
}

// Snapshot is immutable after construction. Every method returning nested
// mutable data returns a deep copy.
type Snapshot struct {
	identity              Identity
	environment           string
	collections           map[string]collectionView
	models                map[string]catalog.CompiledModel
	authorizedCollections map[string][]string
}

type Manager struct {
	source           Source
	seed             IdentitySeed
	clock            Clock
	mu               sync.Mutex
	value            atomic.Pointer[Snapshot]
	publisher        PublicationPublisher
	lastFailedGroups [][]string
	lastErrorCode    string
}

type CollectionDiagnostic struct {
	Name     string
	Revision catalog.ConfigRevision
	Cursor   uint64
	Digest   catalog.Digest
}

type Diagnostics struct {
	Identity               Identity
	Environment            string
	Collections            []CollectionDiagnostic
	FailedDependencyGroups [][]string
	LastErrorCode          string
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
		collections:           map[string]collectionView{},
		models:                map[string]catalog.CompiledModel{},
		authorizedCollections: map[string][]string{},
	})
	return manager, nil
}

func (manager *Manager) Current() *Snapshot { return manager.value.Load() }

// Refresh builds a complete candidate before one atomic pointer swap. Any
// source, validation, or digest error leaves the previous snapshot untouched.
func (manager *Manager) Refresh(ctx context.Context, environment string) (result RefreshResult, resultErr error) {
	environment = strings.TrimSpace(environment)
	if environment == "" {
		return RefreshResult{}, errors.New("refresh snapshot: environment is required")
	}
	manager.mu.Lock()
	defer func() {
		if resultErr != nil {
			manager.lastFailedGroups = nil
			manager.lastErrorCode = "SNAPSHOT_REFRESH_FAILED"
		} else {
			manager.lastFailedGroups = make([][]string, len(result.FailedGroups))
			for index, group := range result.FailedGroups {
				manager.lastFailedGroups[index] = append([]string(nil), group.Collections...)
			}
			manager.lastErrorCode = ""
		}
		manager.mu.Unlock()
	}()

	loaded, err := loadEnvironment(ctx, manager.source, environment)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("refresh snapshot source: %w", err)
	}
	previous := manager.Current()
	initial := previous.identity.Generation == 0 || previous.environment != environment
	if initial && len(loaded.Failures) > 0 {
		return RefreshResult{}, fmt.Errorf("refresh initial snapshot: %s", formatCollectionFailures(loaded.Failures))
	}
	var candidate *Snapshot
	var failedGroups []DependencyGroupFailure
	if initial {
		candidate, err = buildSnapshot(manager.seed, previous.identity.Generation+1, manager.clock.Now().UTC(), environment, loaded.Inputs)
	} else {
		candidate, failedGroups, err = buildPartialSnapshot(manager.seed, previous, manager.clock.Now().UTC(), environment, loaded)
	}
	if err != nil {
		return RefreshResult{}, err
	}
	manager.value.Store(candidate)
	if manager.publisher != nil {
		manager.publisher.Publish(candidate)
	}
	return RefreshResult{Generation: candidate.identity.Generation, CollectionCount: len(candidate.collections), FailedGroups: failedGroups}, nil
}

func (manager *Manager) Diagnostics() Diagnostics {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current := manager.Current()
	result := Diagnostics{
		Identity: current.Identity(), Environment: current.Environment(), LastErrorCode: manager.lastErrorCode,
		FailedDependencyGroups: make([][]string, len(manager.lastFailedGroups)),
	}
	for index, group := range manager.lastFailedGroups {
		result.FailedDependencyGroups[index] = append([]string(nil), group...)
	}
	names := current.CollectionNames()
	result.Collections = make([]CollectionDiagnostic, len(names))
	for index, name := range names {
		revision, _ := current.CollectionVersion(name)
		digest, _ := current.CollectionDigest(name)
		cursor, _ := current.CollectionCursor(name)
		result.Collections[index] = CollectionDiagnostic{Name: name, Revision: revision, Cursor: cursor, Digest: digest}
	}
	return result
}

func loadEnvironment(ctx context.Context, source Source, environment string) (EnvironmentLoad, error) {
	if partial, ok := source.(PartialSource); ok {
		loaded, err := partial.LoadEnvironmentPartial(ctx, environment)
		if loaded.Failures == nil {
			loaded.Failures = map[string]error{}
		}
		return loaded, err
	}
	inputs, err := source.LoadEnvironment(ctx, environment)
	return EnvironmentLoad{Inputs: inputs, Failures: map[string]error{}}, err
}

func buildPartialSnapshot(seed IdentitySeed, previous *Snapshot, publishedAt time.Time, environment string, loaded EnvironmentLoad) (*Snapshot, []DependencyGroupFailure, error) {
	incoming := make(map[string]CollectionInput, len(loaded.Inputs))
	for _, input := range loaded.Inputs {
		name := input.Definition.Name()
		if name == "" {
			return nil, nil, errors.New("refresh partial snapshot: collection identity is required")
		}
		if _, duplicate := incoming[name]; duplicate {
			return nil, nil, fmt.Errorf("refresh partial snapshot: duplicate collection %q", name)
		}
		incoming[name] = input
	}
	failures := make(map[string]error, len(loaded.Failures))
	for name, failure := range loaded.Failures {
		name = strings.TrimSpace(name)
		if name == "" || failure == nil {
			return nil, nil, errors.New("refresh partial snapshot: failures require collection and error")
		}
		failures[name] = failure
	}

	groups := dependencyGroups(previous, incoming, failures)
	accepted := make([]CollectionInput, 0, len(incoming)+len(previous.collections))
	failedGroups := make([]DependencyGroupFailure, 0)
	successfulGroups := 0
	for _, group := range groups {
		groupFailure := firstGroupFailure(group, failures)
		candidateInputs := inputsForGroup(group, incoming)
		if groupFailure == nil && len(candidateInputs) > 0 {
			_, groupFailure = buildSnapshot(seed, previous.identity.Generation+1, publishedAt, environment, candidateInputs)
		}
		if groupFailure != nil {
			accepted = append(accepted, previousInputs(previous, group)...)
			failedGroups = append(failedGroups, DependencyGroupFailure{Collections: append([]string(nil), group...), Reason: groupFailure.Error()})
			continue
		}
		accepted = append(accepted, candidateInputs...)
		successfulGroups++
	}
	if successfulGroups == 0 {
		return nil, nil, fmt.Errorf("refresh partial snapshot: all dependency groups failed: %s", formatGroupFailures(failedGroups))
	}
	candidate, err := buildSnapshot(seed, previous.identity.Generation+1, publishedAt, environment, accepted)
	if err != nil {
		return nil, nil, fmt.Errorf("refresh partial snapshot: compose accepted groups: %w", err)
	}
	return candidate, failedGroups, nil
}

func dependencyGroups(previous *Snapshot, incoming map[string]CollectionInput, failures map[string]error) [][]string {
	edges := make(map[string]map[string]struct{})
	addNode := func(name string) {
		if name != "" && edges[name] == nil {
			edges[name] = make(map[string]struct{})
		}
	}
	addEdge := func(left, right string) {
		addNode(left)
		addNode(right)
		if left != "" && right != "" && left != right {
			edges[left][right] = struct{}{}
			edges[right][left] = struct{}{}
		}
	}
	for name := range previous.collections {
		addNode(name)
	}
	for name := range incoming {
		addNode(name)
	}
	for name := range failures {
		addNode(name)
	}
	modelOwners := make(map[string]string)
	addModels := func(models []catalog.CompiledModel) {
		for _, model := range models {
			owner := model.Collection()
			addNode(owner)
			if existing := modelOwners[model.Code()]; existing != "" && existing != owner {
				addEdge(existing, owner)
			} else {
				modelOwners[model.Code()] = owner
			}
			for _, field := range model.Fields() {
				if field.OptionSource != nil && field.OptionSource.Kind == catalog.OptionSourceCollection {
					addEdge(owner, field.OptionSource.Collection)
				}
			}
		}
	}
	previousModels := make([]catalog.CompiledModel, 0, len(previous.models))
	for _, model := range previous.models {
		previousModels = append(previousModels, model)
	}
	addModels(previousModels)
	for _, input := range incoming {
		addModels(input.Models)
	}

	names := make([]string, 0, len(edges))
	for name := range edges {
		names = append(names, name)
	}
	sort.Strings(names)
	visited := make(map[string]bool, len(names))
	groups := make([][]string, 0, len(names))
	for _, name := range names {
		if visited[name] {
			continue
		}
		visited[name] = true
		queue := []string{name}
		group := make([]string, 0, 1)
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			group = append(group, current)
			neighbors := make([]string, 0, len(edges[current]))
			for neighbor := range edges[current] {
				neighbors = append(neighbors, neighbor)
			}
			sort.Strings(neighbors)
			for _, neighbor := range neighbors {
				if !visited[neighbor] {
					visited[neighbor] = true
					queue = append(queue, neighbor)
				}
			}
		}
		sort.Strings(group)
		groups = append(groups, group)
	}
	return groups
}

func firstGroupFailure(group []string, failures map[string]error) error {
	for _, name := range group {
		if failure := failures[name]; failure != nil {
			return fmt.Errorf("collection %s: %w", name, failure)
		}
	}
	return nil
}

func inputsForGroup(group []string, inputs map[string]CollectionInput) []CollectionInput {
	selected := make([]CollectionInput, 0, len(group))
	for _, name := range group {
		if input, exists := inputs[name]; exists {
			selected = append(selected, input)
		}
	}
	return selected
}

func previousInputs(previous *Snapshot, group []string) []CollectionInput {
	inputs := make([]CollectionInput, 0, len(group))
	for _, name := range group {
		view, exists := previous.collections[name]
		if !exists {
			continue
		}
		models := make([]catalog.CompiledModel, 0)
		for _, model := range previous.models {
			if model.Collection() == name {
				models = append(models, model)
			}
		}
		sort.Slice(models, func(left, right int) bool { return models[left].Code() < models[right].Code() })
		records := make([]catalog.ConfigurationRecord, len(view.ordered))
		for index, key := range view.ordered {
			records[index] = cloneRecord(view.records[key])
		}
		rules := make([]overlay.Rule, len(view.overlayRules))
		for index, rule := range view.overlayRules {
			rules[index] = cloneOverlayRule(rule)
		}
		inputs = append(inputs, CollectionInput{
			Definition: view.definition, Models: models, SubscribedConsumers: append([]string(nil), view.subscribedConsumers...),
			Version: view.version, Cursor: view.cursor, Records: records, OverlayRules: rules,
		})
	}
	return inputs
}

func formatCollectionFailures(failures map[string]error) string {
	names := make([]string, 0, len(failures))
	for name := range failures {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for index, name := range names {
		parts[index] = fmt.Sprintf("%s: %v", name, failures[name])
	}
	return strings.Join(parts, "; ")
}

func formatGroupFailures(failures []DependencyGroupFailure) string {
	parts := make([]string, len(failures))
	for index, failure := range failures {
		parts[index] = fmt.Sprintf("[%s]: %s", strings.Join(failure.Collections, ","), failure.Reason)
	}
	return strings.Join(parts, "; ")
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

func (snapshot *Snapshot) CollectionCursor(collection string) (uint64, bool) {
	view, exists := snapshot.collections[collection]
	return view.cursor, exists
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

func (snapshot *Snapshot) OverlayRules(collection string) []overlay.Rule {
	view, exists := snapshot.collections[collection]
	if !exists {
		return nil
	}
	rules := make([]overlay.Rule, len(view.overlayRules))
	for index, rule := range view.overlayRules {
		rules[index] = cloneOverlayRule(rule)
	}
	return rules
}

// AuthorizedCollections returns the SDK-deliverable subscriptions published
// in this immutable snapshot generation.
func (snapshot *Snapshot) AuthorizedCollections(_ context.Context, consumerID string) ([]string, error) {
	if snapshot == nil {
		return nil, errors.New("snapshot authorization is unavailable")
	}
	consumerID = strings.TrimSpace(consumerID)
	if consumerID == "" {
		return nil, errors.New("snapshot authorization requires a consumer")
	}
	return append([]string(nil), snapshot.authorizedCollections[consumerID]...), nil
}

func (manager *Manager) AuthorizedCollections(ctx context.Context, consumerID string) ([]string, error) {
	if manager == nil {
		return nil, errors.New("snapshot authorization is unavailable")
	}
	current := manager.Current()
	if current == nil {
		return nil, errors.New("snapshot authorization is unavailable")
	}
	return current.AuthorizedCollections(ctx, consumerID)
}

func buildSnapshot(seed IdentitySeed, generation uint64, publishedAt time.Time, environment string, inputs []CollectionInput) (*Snapshot, error) {
	candidate := &Snapshot{
		identity: Identity{
			ServerEpoch: seed.ServerEpoch, ServerInstanceID: seed.ServerInstanceID,
			SnapshotInstance: seed.SnapshotInstance, Generation: generation, PublishedAt: publishedAt,
		},
		environment:           environment,
		collections:           make(map[string]collectionView, len(inputs)),
		models:                make(map[string]catalog.CompiledModel),
		authorizedCollections: make(map[string][]string),
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
			definition:   input.Definition,
			version:      input.Version,
			cursor:       input.Cursor,
			records:      make(map[string]catalog.ConfigurationRecord, len(input.Records)),
			ordered:      make([]string, 0, len(input.Records)),
			overlayRules: make([]overlay.Rule, len(input.OverlayRules)),
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
		for index, rule := range input.OverlayRules {
			if rule.Collection != name || rule.Scope.Environment != environment || rule.ConfigRevision == 0 || rule.ConfigRevision > input.Version {
				return nil, fmt.Errorf("build snapshot: overlay rule %q identity or revision is invalid", rule.ID)
			}
			view.overlayRules[index] = cloneOverlayRule(rule)
		}
		seenConsumers := make(map[string]struct{}, len(input.SubscribedConsumers))
		for _, consumerID := range input.SubscribedConsumers {
			consumerID = strings.TrimSpace(consumerID)
			if consumerID == "" {
				return nil, fmt.Errorf("build snapshot: collection %q has an empty subscribed consumer", name)
			}
			if _, duplicate := seenConsumers[consumerID]; duplicate {
				return nil, fmt.Errorf("build snapshot: collection %q has duplicate subscribed consumer %q", name, consumerID)
			}
			seenConsumers[consumerID] = struct{}{}
			view.subscribedConsumers = append(view.subscribedConsumers, consumerID)
			if input.Definition.SDKDeliveryEnabled() {
				candidate.authorizedCollections[consumerID] = append(candidate.authorizedCollections[consumerID], name)
			}
		}
		sort.Strings(view.subscribedConsumers)
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
	for consumerID := range candidate.authorizedCollections {
		sort.Strings(candidate.authorizedCollections[consumerID])
	}
	if err := validateOptionDependencies(candidate); err != nil {
		return nil, err
	}
	return candidate, nil
}

func validateOptionDependencies(candidate *Snapshot) error {
	for _, model := range candidate.models {
		for _, field := range model.Fields() {
			source := field.OptionSource
			if source == nil || source.Kind != catalog.OptionSourceCollection {
				continue
			}
			view, exists := candidate.collections[source.Collection]
			if !exists {
				return fmt.Errorf("build snapshot: model %q option collection %q is missing", model.Code(), source.Collection)
			}
			for _, name := range []string{source.ValueField, source.LabelField} {
				definition, exists := view.definition.Field(name)
				if !exists || definition.Sensitive {
					return fmt.Errorf("build snapshot: model %q option field %q is missing or sensitive", model.Code(), name)
				}
			}
			for _, filter := range source.FixedFilters {
				definition, exists := view.definition.Field(filter.Field)
				if !exists || definition.Sensitive {
					return fmt.Errorf("build snapshot: model %q option filter %q is missing or sensitive", model.Code(), filter.Field)
				}
				if _, err := catalog.CanonicalizeScalar(definition.Type, filter.Value); err != nil {
					return fmt.Errorf("build snapshot: model %q option filter %q: %w", model.Code(), filter.Field, err)
				}
			}
		}
	}
	return nil
}

func cloneRecord(record catalog.ConfigurationRecord) catalog.ConfigurationRecord {
	source := record.Data
	record.Data = make(map[string]string, len(source))
	for key, value := range source {
		record.Data[key] = value
	}
	return record
}

func cloneOverlayRule(rule overlay.Rule) overlay.Rule {
	content := rule.Content
	rule.Content = make(map[string]string, len(content))
	for key, value := range content {
		rule.Content[key] = value
	}
	rule.RolloutRanges = append([]overlay.BucketRange(nil), rule.RolloutRanges...)
	if rule.EffectiveFrom != nil {
		value := *rule.EffectiveFrom
		rule.EffectiveFrom = &value
	}
	if rule.EffectiveUntil != nil {
		value := *rule.EffectiveUntil
		rule.EffectiveUntil = &value
	}
	if rule.ActivatedRevision != nil {
		value := *rule.ActivatedRevision
		rule.ActivatedRevision = &value
	}
	if rule.ActivatedAt != nil {
		value := *rule.ActivatedAt
		rule.ActivatedAt = &value
	}
	if rule.ExpiredRevision != nil {
		value := *rule.ExpiredRevision
		rule.ExpiredRevision = &value
	}
	if rule.ExpiredAt != nil {
		value := *rule.ExpiredAt
		rule.ExpiredAt = &value
	}
	return rule
}

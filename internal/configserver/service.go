package configserver

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
)

var (
	ErrManagedEnvironmentMismatch = errors.New("Config Server environment does not match the managed environment")
	ErrSnapshotUnavailable        = errors.New("Config Server snapshot is unavailable")
	ErrInvalidArgument            = errors.New("Config Server request is invalid")
	ErrCollectionForbidden        = errors.New("Config Server collection is not available to the consumer")
)

const maxCollectionWait = 2 * time.Second

type SnapshotProvider interface {
	Current() *snapshot.Snapshot
}

type Version struct {
	Collection string
	Revision   catalog.ConfigRevision
	Digest     string
}

type GetSnapshotRequest struct {
	ConsumerID    string
	ClientID      string
	Region        string
	Environment   string
	Stage         string
	KnownVersions []Version
}

type Record struct {
	RecordKey      string
	RecordRevision catalog.ConfigRevision
	Data           map[string]string
}

type CollectionPayload struct {
	Name         string
	Revision     catalog.ConfigRevision
	ChangeCursor uint64
	Digest       string
	Records      []Record
}

type GetSnapshotResponse struct {
	Identity           snapshot.Identity
	Region             string
	Environment        string
	Stage              string
	Bucket             int32
	Collections        []CollectionPayload
	DeletedCollections []string
}

type DiffVersionsRequest = GetSnapshotRequest

type DiffVersionsResponse struct {
	Identity snapshot.Identity
	Added    []string
	Modified []string
	Deleted  []string
}

type GetCollectionsRequest struct {
	ConsumerID  string
	ClientID    string
	Region      string
	Environment string
	Stage       string
	Collections []string
	MinRevision catalog.ConfigRevision
}

type GetCollectionsResponse struct {
	Identity    snapshot.Identity
	Collections []CollectionPayload
}

type Service struct {
	snapshots          SnapshotProvider
	managedEnvironment string
	refreshSubmitter   snapshot.RefreshTargetSubmitter
	waitTimeout        time.Duration
}

func New(snapshots SnapshotProvider, managedEnvironment string) *Service {
	return &Service{snapshots: snapshots, managedEnvironment: strings.TrimSpace(managedEnvironment)}
}

func NewWithRefresh(snapshots SnapshotProvider, managedEnvironment string, submitter snapshot.RefreshTargetSubmitter, waitTimeout time.Duration) (*Service, error) {
	if submitter == nil || waitTimeout <= 0 || waitTimeout > maxCollectionWait {
		return nil, errors.New("new Config Server refresh reader: submitter and wait timeout up to two seconds are required")
	}
	service := New(snapshots, managedEnvironment)
	service.refreshSubmitter = submitter
	service.waitTimeout = waitTimeout
	return service, nil
}

func (service *Service) GetSnapshot(ctx context.Context, request GetSnapshotRequest) (GetSnapshotResponse, error) {
	if service == nil || service.snapshots == nil || service.managedEnvironment == "" {
		return GetSnapshotResponse{}, errors.New("get snapshot: service dependencies are incomplete")
	}
	request.ConsumerID = strings.TrimSpace(request.ConsumerID)
	request.ClientID = strings.TrimSpace(request.ClientID)
	request.Region = strings.TrimSpace(request.Region)
	request.Environment = strings.TrimSpace(request.Environment)
	request.Stage = strings.TrimSpace(request.Stage)
	if request.ConsumerID == "" || request.ClientID == "" || request.Region == "" || request.Environment == "" {
		return GetSnapshotResponse{}, errors.New("get snapshot: consumer, client, region, and environment are required")
	}
	if request.Environment != service.managedEnvironment {
		return GetSnapshotResponse{}, fmt.Errorf("get snapshot: %w: got %q, want %q", ErrManagedEnvironmentMismatch, request.Environment, service.managedEnvironment)
	}
	bucket, err := overlay.ClientBucket(request.ConsumerID, request.ClientID)
	if err != nil {
		return GetSnapshotResponse{}, fmt.Errorf("get snapshot: %w", err)
	}
	current := service.snapshots.Current()
	if current == nil {
		return GetSnapshotResponse{}, fmt.Errorf("get snapshot: %w", ErrSnapshotUnavailable)
	}
	if current.Environment() != service.managedEnvironment {
		return GetSnapshotResponse{}, fmt.Errorf("get snapshot: %w: snapshot for %q is not loaded", ErrManagedEnvironmentMismatch, service.managedEnvironment)
	}
	authorized, err := current.AuthorizedCollections(ctx, request.ConsumerID)
	if err != nil {
		return GetSnapshotResponse{}, fmt.Errorf("get snapshot authorization: %w", err)
	}
	authorizedSet := make(map[string]struct{}, len(authorized))
	for _, name := range authorized {
		name = strings.TrimSpace(name)
		if name != "" {
			authorizedSet[name] = struct{}{}
		}
	}
	known := make(map[string]Version, len(request.KnownVersions))
	for _, version := range request.KnownVersions {
		version.Collection = strings.TrimSpace(version.Collection)
		if version.Collection == "" {
			return GetSnapshotResponse{}, errors.New("get snapshot: known version collection is required")
		}
		if _, duplicate := known[version.Collection]; duplicate {
			return GetSnapshotResponse{}, fmt.Errorf("get snapshot: duplicate known version %q", version.Collection)
		}
		known[version.Collection] = version
	}

	response := GetSnapshotResponse{
		Identity: current.Identity(), Region: request.Region, Environment: current.Environment(), Stage: request.Stage, Bucket: bucket,
	}
	for _, name := range current.CollectionNames() {
		if _, allowed := authorizedSet[name]; !allowed {
			continue
		}
		definition, _ := current.Definition(name)
		if !definition.SDKDeliveryEnabled() {
			continue
		}
		revision, _ := current.CollectionVersion(name)
		changeCursor, _ := current.CollectionCursor(name)
		records, err := overlay.Evaluate(overlay.Query{
			Collection: name, Scope: overlay.Scope{Region: request.Region, Environment: request.Environment, Stage: request.Stage}, PreviewBucket: &bucket,
		}, current.Records(name), current.OverlayRules(name))
		if err != nil {
			return GetSnapshotResponse{}, fmt.Errorf("get snapshot: evaluate %q: %w", name, err)
		}
		digest, err := catalog.ComputeBaseDigest(records)
		if err != nil {
			return GetSnapshotResponse{}, fmt.Errorf("get snapshot: digest %q: %w", name, err)
		}
		if previous, exists := known[name]; exists && previous.Revision == revision && previous.Digest == digest.Value {
			delete(known, name)
			continue
		}
		payload := CollectionPayload{Name: name, Revision: revision, ChangeCursor: changeCursor, Digest: digest.Value, Records: make([]Record, len(records))}
		for index, record := range records {
			payload.Records[index] = Record{RecordKey: record.RecordKey, RecordRevision: record.ConfigRevision, Data: cloneMap(record.Data)}
		}
		response.Collections = append(response.Collections, payload)
		delete(known, name)
	}
	for name := range known {
		response.DeletedCollections = append(response.DeletedCollections, name)
	}
	sort.Strings(response.DeletedCollections)
	return response, nil
}

func (service *Service) DiffVersions(ctx context.Context, request DiffVersionsRequest) (DiffVersionsResponse, error) {
	known := make(map[string]struct{}, len(request.KnownVersions))
	normalized := request
	normalized.KnownVersions = append([]Version(nil), request.KnownVersions...)
	for index, version := range normalized.KnownVersions {
		name := strings.TrimSpace(version.Collection)
		if name == "" {
			return DiffVersionsResponse{}, fmt.Errorf("diff versions: known version collection is required: %w", ErrInvalidArgument)
		}
		if _, duplicate := known[name]; duplicate {
			return DiffVersionsResponse{}, fmt.Errorf("diff versions: duplicate known version %q: %w", name, ErrInvalidArgument)
		}
		known[name] = struct{}{}
		normalized.KnownVersions[index].Collection = name
	}
	response, err := service.GetSnapshot(ctx, GetSnapshotRequest(normalized))
	if err != nil {
		return DiffVersionsResponse{}, err
	}
	diff := DiffVersionsResponse{Identity: response.Identity, Deleted: append([]string(nil), response.DeletedCollections...)}
	for _, collection := range response.Collections {
		if _, existed := known[collection.Name]; existed {
			diff.Modified = append(diff.Modified, collection.Name)
		} else {
			diff.Added = append(diff.Added, collection.Name)
		}
	}
	sort.Strings(diff.Added)
	sort.Strings(diff.Modified)
	sort.Strings(diff.Deleted)
	return diff, nil
}

func (service *Service) GetCollections(ctx context.Context, request GetCollectionsRequest) (GetCollectionsResponse, error) {
	request.Collections = append([]string(nil), request.Collections...)
	requested := make(map[string]struct{}, len(request.Collections))
	for index, collection := range request.Collections {
		collection = strings.TrimSpace(collection)
		if collection == "" {
			return GetCollectionsResponse{}, fmt.Errorf("get collections: collection is required: %w", ErrInvalidArgument)
		}
		if _, duplicate := requested[collection]; duplicate {
			return GetCollectionsResponse{}, fmt.Errorf("get collections: duplicate collection %q: %w", collection, ErrInvalidArgument)
		}
		requested[collection] = struct{}{}
		request.Collections[index] = collection
	}
	if len(requested) == 0 {
		return GetCollectionsResponse{}, fmt.Errorf("get collections: collections are required: %w", ErrInvalidArgument)
	}

	load := func(loadCtx context.Context) (GetCollectionsResponse, []snapshot.RefreshTarget, error) {
		response, err := service.GetSnapshot(loadCtx, GetSnapshotRequest{
			ConsumerID: request.ConsumerID, ClientID: request.ClientID, Region: request.Region,
			Environment: request.Environment, Stage: request.Stage,
		})
		if err != nil {
			return GetCollectionsResponse{}, nil, err
		}
		available := make(map[string]CollectionPayload, len(response.Collections))
		for _, collection := range response.Collections {
			available[collection.Name] = collection
		}
		selected := make([]CollectionPayload, 0, len(request.Collections))
		unmet := make([]snapshot.RefreshTarget, 0)
		for _, name := range request.Collections {
			collection, exists := available[name]
			if !exists {
				return GetCollectionsResponse{}, nil, fmt.Errorf("get collections: requested collection is unavailable: %w", ErrCollectionForbidden)
			}
			selected = append(selected, collection)
			if collection.Revision < request.MinRevision {
				unmet = append(unmet, snapshot.RefreshTarget{Collection: name, MinRevision: request.MinRevision})
			}
		}
		sort.Slice(selected, func(left, right int) bool { return selected[left].Name < selected[right].Name })
		return GetCollectionsResponse{Identity: response.Identity, Collections: selected}, unmet, nil
	}

	response, unmet, err := load(ctx)
	if err != nil || len(unmet) == 0 {
		return response, err
	}
	if service.refreshSubmitter == nil || service.waitTimeout <= 0 {
		return GetCollectionsResponse{}, fmt.Errorf("get collections: minimum revision is unavailable: %w", ErrSnapshotUnavailable)
	}
	if err := service.refreshSubmitter.Submit(unmet); err != nil {
		return GetCollectionsResponse{}, fmt.Errorf("get collections: submit refresh target: %w", ErrSnapshotUnavailable)
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, service.waitTimeout)
	defer cancelWait()
	interval := service.waitTimeout / 10
	if interval <= 0 {
		interval = time.Millisecond
	}
	if interval > 50*time.Millisecond {
		interval = 50 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if collectionTargetsReached(service.snapshots.Current(), service.managedEnvironment, unmet) {
			response, unmet, err = load(waitCtx)
			if waitCtx.Err() != nil {
				return GetCollectionsResponse{}, collectionWaitError(ctx)
			}
			if err != nil {
				return GetCollectionsResponse{}, err
			}
			if len(unmet) == 0 {
				select {
				case <-waitCtx.Done():
					return GetCollectionsResponse{}, collectionWaitError(ctx)
				default:
					return response, nil
				}
			}
		}
		select {
		case <-waitCtx.Done():
			return GetCollectionsResponse{}, collectionWaitError(ctx)
		case <-ticker.C:
		}
	}
}

func collectionTargetsReached(current *snapshot.Snapshot, environment string, targets []snapshot.RefreshTarget) bool {
	if current == nil || current.Environment() != environment {
		return false
	}
	for _, target := range targets {
		revision, exists := current.CollectionVersion(target.Collection)
		if !exists || revision < target.MinRevision {
			return false
		}
	}
	return true
}

func collectionWaitError(parent context.Context) error {
	if err := parent.Err(); err != nil {
		return err
	}
	return fmt.Errorf("get collections: minimum revision was not reached before the wait limit: %w", ErrSnapshotUnavailable)
}

func cloneMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

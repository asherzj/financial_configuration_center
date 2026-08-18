package configserver

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
)

type SnapshotProvider interface {
	Current() *snapshot.Snapshot
}

type Authorizer interface {
	AuthorizedCollections(context.Context, string) ([]string, error)
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
	Name     string
	Revision catalog.ConfigRevision
	Digest   string
	Records  []Record
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

type Service struct {
	snapshots  SnapshotProvider
	authorizer Authorizer
}

func New(snapshots SnapshotProvider, authorizer Authorizer) *Service {
	return &Service{snapshots: snapshots, authorizer: authorizer}
}

func (service *Service) GetSnapshot(ctx context.Context, request GetSnapshotRequest) (GetSnapshotResponse, error) {
	if service == nil || service.snapshots == nil || service.authorizer == nil {
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
	bucket, err := overlay.ClientBucket(request.ConsumerID, request.ClientID)
	if err != nil {
		return GetSnapshotResponse{}, fmt.Errorf("get snapshot: %w", err)
	}
	current := service.snapshots.Current()
	if current == nil || current.Environment() != request.Environment {
		return GetSnapshotResponse{}, fmt.Errorf("get snapshot: environment %q is not loaded", request.Environment)
	}
	authorized, err := service.authorizer.AuthorizedCollections(ctx, request.ConsumerID)
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
		if strings.TrimSpace(version.Collection) == "" {
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
		payload := CollectionPayload{Name: name, Revision: revision, Digest: digest.Value, Records: make([]Record, len(records))}
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

func cloneMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

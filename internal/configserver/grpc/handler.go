package grpc

import (
	"context"
	"errors"
	"math"
	"strings"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/configserver"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/config/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Application interface {
	GetSnapshot(context.Context, configserver.GetSnapshotRequest) (configserver.GetSnapshotResponse, error)
}

type Handler struct{ application Application }

func New(application Application) (*Handler, error) {
	if application == nil {
		return nil, errors.New("new ConfigService handler: application is required")
	}
	return &Handler{application: application}, nil
}

func (handler *Handler) GetSnapshot(ctx context.Context, request *configv1.GetSnapshotRequest) (*configv1.GetSnapshotResponse, error) {
	if request == nil || request.Scope == nil || strings.TrimSpace(request.ConsumerId) == "" || strings.TrimSpace(request.ClientId) == "" || strings.TrimSpace(request.Scope.Environment) == "" {
		return nil, status.Error(codes.InvalidArgument, "consumer_id, client_id, and scope.environment are required")
	}
	known := make([]configserver.Version, len(request.KnownVersions))
	for index, version := range request.KnownVersions {
		if version == nil || version.ConfigRevision < 0 || version.BaseDigest == nil {
			return nil, status.Error(codes.InvalidArgument, "known_versions entries require a non-negative revision and base digest")
		}
		known[index] = configserver.Version{Collection: version.Collection, Revision: catalog.ConfigRevision(version.ConfigRevision), Digest: version.BaseDigest.Value}
	}
	response, err := handler.application.GetSnapshot(ctx, configserver.GetSnapshotRequest{
		ConsumerID: request.ConsumerId, ClientID: request.ClientId,
		Environment: request.Scope.Environment, KnownVersions: known,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "get snapshot failed")
	}
	converted := &configv1.GetSnapshotResponse{
		Snapshot:           mapIdentity(response.Identity),
		Scope:              &commonv1.Scope{Region: request.Scope.Region, Environment: response.Environment, Stage: request.Scope.Stage},
		DeletedCollections: append([]string(nil), response.DeletedCollections...),
		Collections:        make([]*configv1.CollectionPayload, len(response.Collections)),
	}
	for index, collection := range response.Collections {
		revision, err := revisionInt64(collection.Revision)
		if err != nil {
			return nil, status.Error(codes.Internal, "collection revision exceeds RPC range")
		}
		body := &configv1.CollectionData{Records: make([]*configv1.SnapshotRecord, len(collection.Records))}
		for recordIndex, record := range collection.Records {
			recordRevision, err := revisionInt64(record.RecordRevision)
			if err != nil {
				return nil, status.Error(codes.Internal, "record revision exceeds RPC range")
			}
			body.Records[recordIndex] = &configv1.SnapshotRecord{
				RecordKey: record.RecordKey, RecordRevision: recordRevision, Values: cloneMap(record.Data),
			}
		}
		data, err := proto.MarshalOptions{Deterministic: true}.Marshal(body)
		if err != nil {
			return nil, status.Error(codes.Internal, "encode collection payload failed")
		}
		converted.Collections[index] = &configv1.CollectionPayload{
			Collection: collection.Name, Codec: "PROTOBUF", FormatVersion: 1, Data: data,
			Version: &configv1.VersionView{
				Collection: collection.Name, ConfigRevision: revision,
				BaseDigest: &commonv1.Digest{Algorithm: "SHA-256", Value: collection.Digest},
			},
		}
	}
	return converted, nil
}

func (handler *Handler) DiffVersions(context.Context, *configv1.DiffVersionsRequest) (*configv1.DiffVersionsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "DiffVersions is not implemented in the base-only slice")
}

func (handler *Handler) GetCollections(context.Context, *configv1.GetCollectionsRequest) (*configv1.GetCollectionsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "GetCollections is not implemented in the base-only slice")
}

func (handler *Handler) Watch(*configv1.WatchRequest, configv1.ConfigService_WatchServer) error {
	return status.Error(codes.Unimplemented, "Watch is not implemented in the base-only slice")
}

func mapIdentity(identity snapshot.Identity) *commonv1.SnapshotIdentity {
	return &commonv1.SnapshotIdentity{
		ServerEpoch: identity.ServerEpoch, ServerInstanceId: identity.ServerInstanceID,
		SnapshotInstance: identity.SnapshotInstance, SnapshotGeneration: identity.Generation,
		PublishedAt: timestamppb.New(identity.PublishedAt),
	}
}

func revisionInt64(revision catalog.ConfigRevision) (int64, error) {
	if uint64(revision) > math.MaxInt64 {
		return 0, errors.New("revision exceeds int64")
	}
	return int64(revision), nil
}

func cloneMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

var _ configv1.ConfigService = (*Handler)(nil)

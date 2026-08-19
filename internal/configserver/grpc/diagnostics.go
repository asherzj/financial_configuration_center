package grpc

import (
	"context"
	"errors"
	"strings"

	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/config/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type DiagnosticsProvider interface {
	Diagnostics() snapshot.Diagnostics
}

type DiagnosticsHandler struct{ provider DiagnosticsProvider }

func NewDiagnostics(provider DiagnosticsProvider) (*DiagnosticsHandler, error) {
	if provider == nil {
		return nil, errors.New("new diagnostics handler: provider is required")
	}
	return &DiagnosticsHandler{provider: provider}, nil
}

func (handler *DiagnosticsHandler) GetSnapshotStatus(context.Context, *configv1.GetSnapshotStatusRequest) (*configv1.GetSnapshotStatusResponse, error) {
	diagnostics := handler.provider.Diagnostics()
	failed := make([]string, len(diagnostics.FailedDependencyGroups))
	for index, group := range diagnostics.FailedDependencyGroups {
		failed[index] = strings.Join(group, ",")
	}
	if diagnostics.LastErrorCode != "" {
		failed = append(failed, diagnostics.LastErrorCode)
	}
	return &configv1.GetSnapshotStatusResponse{
		Snapshot: mapIdentity(diagnostics.Identity), CollectionCount: int64(len(diagnostics.Collections)), FailedDependencyGroups: failed,
	}, nil
}

func (handler *DiagnosticsHandler) GetCollectionStatus(_ context.Context, request *configv1.GetCollectionStatusRequest) (*configv1.GetCollectionStatusResponse, error) {
	if request == nil || strings.TrimSpace(request.Collection) == "" || strings.TrimSpace(request.Environment) == "" {
		return nil, status.Error(codes.InvalidArgument, "collection and environment are required")
	}
	diagnostics := handler.provider.Diagnostics()
	if diagnostics.Environment != request.Environment {
		return nil, status.Error(codes.FailedPrecondition, "requested environment is not loaded")
	}
	for _, collection := range diagnostics.Collections {
		if collection.Name != request.Collection {
			continue
		}
		revision, err := revisionInt64(collection.Revision)
		if err != nil {
			return nil, status.Error(codes.Internal, "collection revision exceeds RPC range")
		}
		response := &configv1.GetCollectionStatusResponse{
			Collection: collection.Name, Environment: diagnostics.Environment,
			Version: &configv1.VersionView{
				Collection: collection.Name, ConfigRevision: revision,
				EffectiveDigest: &commonv1.Digest{Algorithm: "SHA-256", Value: collection.Digest.Value},
			},
		}
		if diagnostics.LastErrorCode != "" {
			response.LastErrorCode = &diagnostics.LastErrorCode
		}
		return response, nil
	}
	return nil, status.Error(codes.NotFound, "collection is not loaded")
}

var _ configv1.DiagnosticsService = (*DiagnosticsHandler)(nil)

package grpc

import (
	"context"
	"errors"
	"strings"

	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/config/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type DiagnosticsProvider interface {
	Diagnostics() snapshot.Diagnostics
}

type DiagnosticsRequestAuthorizer interface {
	AuthorizeDiagnostics(context.Context, string) error
}

type DiagnosticsHandler struct {
	provider           DiagnosticsProvider
	authorizer         DiagnosticsRequestAuthorizer
	managedEnvironment string
}

func NewDiagnostics(provider DiagnosticsProvider, authorizer DiagnosticsRequestAuthorizer, managedEnvironment string) (*DiagnosticsHandler, error) {
	compiledEnvironment, environmentErr := platformauth.CompileEnvironment(managedEnvironment)
	if provider == nil || authorizer == nil || environmentErr != nil {
		return nil, errors.New("new diagnostics handler: provider, request authorizer, and managed environment are required")
	}
	return &DiagnosticsHandler{provider: provider, authorizer: authorizer, managedEnvironment: compiledEnvironment}, nil
}

func (handler *DiagnosticsHandler) GetSnapshotStatus(ctx context.Context, _ *configv1.GetSnapshotStatusRequest) (*configv1.GetSnapshotStatusResponse, error) {
	if err := handler.authorizer.AuthorizeDiagnostics(ctx, handler.managedEnvironment); err != nil {
		return nil, err
	}
	diagnostics := handler.provider.Diagnostics()
	if diagnostics.Environment != handler.managedEnvironment {
		return nil, status.Error(codes.FailedPrecondition, "managed environment snapshot is not loaded")
	}
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

func (handler *DiagnosticsHandler) GetCollectionStatus(ctx context.Context, request *configv1.GetCollectionStatusRequest) (*configv1.GetCollectionStatusResponse, error) {
	if request == nil || strings.TrimSpace(request.Collection) == "" || strings.TrimSpace(request.Environment) == "" {
		return nil, status.Error(codes.InvalidArgument, "collection and environment are required")
	}
	environment, err := platformauth.CompileEnvironment(request.Environment)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "environment must be a concrete segment")
	}
	if environment != handler.managedEnvironment {
		return nil, status.Error(codes.FailedPrecondition, "requested environment is not managed by this server")
	}
	if err := handler.authorizer.AuthorizeDiagnostics(ctx, environment); err != nil {
		return nil, err
	}
	diagnostics := handler.provider.Diagnostics()
	if diagnostics.Environment != handler.managedEnvironment {
		return nil, status.Error(codes.FailedPrecondition, "managed environment snapshot is not loaded")
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

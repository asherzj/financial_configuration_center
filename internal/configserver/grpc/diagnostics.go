package grpc

import (
	"context"
	"errors"
	"reflect"
	"strings"

	commonv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
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
	if provider == nil || isNilDiagnosticsDependency(provider) || authorizer == nil || isNilDiagnosticsDependency(authorizer) || environmentErr != nil {
		return nil, errors.New("new diagnostics handler: provider, request authorizer, and managed environment are required")
	}
	return &DiagnosticsHandler{provider: provider, authorizer: authorizer, managedEnvironment: compiledEnvironment}, nil
}

func isNilDiagnosticsDependency(value any) bool {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func (handler *DiagnosticsHandler) GetSnapshotStatus(ctx context.Context, _ *configv1.GetSnapshotStatusRequest) (*configv1.GetSnapshotStatusResponse, error) {
	if err := handler.authorizer.AuthorizeDiagnostics(ctx, handler.managedEnvironment); err != nil {
		return nil, err
	}
	diagnostics := handler.provider.Diagnostics()
	if diagnostics.Environment != handler.managedEnvironment {
		return nil, kitexstatus.Err(kitexcodes.FailedPrecondition, "managed environment snapshot is not loaded")
	}
	failed := make([]string, len(diagnostics.FailedDependencyGroups))
	failedDetails := make([]*configv1.FailedDependencyGroup, len(diagnostics.FailedDependencyGroups))
	for index, group := range diagnostics.FailedDependencyGroups {
		failed[index] = strings.Join(group, ",")
		failedDetails[index] = &configv1.FailedDependencyGroup{Collections: append([]string(nil), group...)}
	}
	collections := make([]*configv1.SnapshotCollectionStatus, len(diagnostics.Collections))
	for index, collection := range diagnostics.Collections {
		revision, err := revisionInt64(collection.Revision)
		if err != nil {
			return nil, kitexstatus.Err(kitexcodes.Internal, "collection revision exceeds RPC range")
		}
		cursor, err := uint64Int64(collection.Cursor)
		if err != nil {
			return nil, kitexstatus.Err(kitexcodes.Internal, "collection cursor exceeds RPC range")
		}
		collections[index] = &configv1.SnapshotCollectionStatus{
			Collection: collection.Name, ConfigRevision: revision, ChangeCursor: cursor,
			EffectiveDigest: &commonv1.Digest{Algorithm: collection.Digest.Algorithm, Value: collection.Digest.Value},
		}
	}
	response := &configv1.GetSnapshotStatusResponse{
		Snapshot: mapIdentity(diagnostics.Identity), CollectionCount: int64(len(collections)), FailedDependencyGroups: failed,
		Environment: diagnostics.Environment, Collections: collections, FailedDependencyGroupDetails: failedDetails,
	}
	if diagnostics.LastErrorCode != "" {
		response.LastErrorCode = &diagnostics.LastErrorCode
	}
	return response, nil
}

func (handler *DiagnosticsHandler) GetCollectionStatus(ctx context.Context, request *configv1.GetCollectionStatusRequest) (*configv1.GetCollectionStatusResponse, error) {
	if request == nil || strings.TrimSpace(request.Collection) == "" || strings.TrimSpace(request.Environment) == "" {
		return nil, kitexstatus.Err(kitexcodes.InvalidArgument, "collection and environment are required")
	}
	environment, err := platformauth.CompileEnvironment(request.Environment)
	if err != nil {
		return nil, kitexstatus.Err(kitexcodes.InvalidArgument, "environment must be a concrete segment")
	}
	if environment != handler.managedEnvironment {
		return nil, kitexstatus.Err(kitexcodes.FailedPrecondition, "requested environment is not managed by this server")
	}
	if err := handler.authorizer.AuthorizeDiagnostics(ctx, environment); err != nil {
		return nil, err
	}
	diagnostics := handler.provider.Diagnostics()
	if diagnostics.Environment != handler.managedEnvironment {
		return nil, kitexstatus.Err(kitexcodes.FailedPrecondition, "managed environment snapshot is not loaded")
	}
	for _, collection := range diagnostics.Collections {
		if collection.Name != request.Collection {
			continue
		}
		revision, err := revisionInt64(collection.Revision)
		if err != nil {
			return nil, kitexstatus.Err(kitexcodes.Internal, "collection revision exceeds RPC range")
		}
		response := &configv1.GetCollectionStatusResponse{
			Collection: collection.Name, Environment: diagnostics.Environment,
			Version: &configv1.VersionView{
				Collection: collection.Name, ConfigRevision: revision,
				EffectiveDigest: &commonv1.Digest{Algorithm: collection.Digest.Algorithm, Value: collection.Digest.Value},
			},
		}
		changeCursor, err := uint64Int64(collection.Cursor)
		if err != nil {
			return nil, kitexstatus.Err(kitexcodes.Internal, "collection cursor exceeds RPC range")
		}
		response.ChangeCursor = changeCursor
		if diagnostics.LastErrorCode != "" {
			response.LastErrorCode = &diagnostics.LastErrorCode
		}
		return response, nil
	}
	return nil, kitexstatus.Err(kitexcodes.NotFound, "collection is not loaded")
}

var _ configv1.DiagnosticsService = (*DiagnosticsHandler)(nil)

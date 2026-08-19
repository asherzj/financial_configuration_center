package grpc

import (
	"context"
	"errors"

	configv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1"
	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type HintNotifier interface {
	Notify(snapshot.RefreshHint) error
}

type RefreshRequestAuthorizer interface {
	AuthorizeRefresh(context.Context, string) error
}

type RefreshHandler struct {
	notifier           HintNotifier
	snapshot           interface{ Current() *snapshot.Snapshot }
	authorizer         RefreshRequestAuthorizer
	managedEnvironment string
}

func NewRefreshHandler(notifier HintNotifier, provider interface{ Current() *snapshot.Snapshot }, authorizer RefreshRequestAuthorizer, managedEnvironment string) (*RefreshHandler, error) {
	compiledEnvironment, environmentErr := platformauth.CompileEnvironment(managedEnvironment)
	if notifier == nil || provider == nil || authorizer == nil || environmentErr != nil {
		return nil, errors.New("new RefreshService handler: notifier, snapshot provider, request authorizer, and managed environment are required")
	}
	return &RefreshHandler{notifier: notifier, snapshot: provider, authorizer: authorizer, managedEnvironment: compiledEnvironment}, nil
}

func (handler *RefreshHandler) Notify(ctx context.Context, request *configv1.NotifyRequest) (*configv1.NotifyResponse, error) {
	if request == nil || request.Scope == nil || request.EventId == "" || request.Scope.Environment == "" || len(request.Targets) == 0 {
		return nil, status.Error(codes.InvalidArgument, "event_id, scope.environment, and targets are required")
	}
	environment, err := platformauth.CompileEnvironment(request.Scope.Environment)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "scope.environment must be a concrete segment")
	}
	if environment != handler.managedEnvironment {
		return nil, status.Error(codes.InvalidArgument, "refresh hint targets another managed environment")
	}
	if err := handler.authorizer.AuthorizeRefresh(ctx, environment); err != nil {
		return nil, err
	}
	targets := make([]snapshot.HintTarget, len(request.Targets))
	for index, target := range request.Targets {
		if target == nil || target.Collection == "" || target.MinConfigRevision <= 0 || target.TargetCursor < 0 {
			return nil, status.Error(codes.InvalidArgument, "refresh targets require collection and positive revision")
		}
		targets[index] = snapshot.HintTarget{Collection: target.Collection, MinRevision: catalog.ConfigRevision(target.MinConfigRevision), TargetCursor: uint64(target.TargetCursor)}
	}
	err = handler.notifier.Notify(snapshot.RefreshHint{
		EventID: request.EventId, Environment: environment, Targets: targets,
		ReleaseOrderID: request.ReleaseOrderId, TraceID: request.TraceId,
	})
	if errors.Is(err, snapshot.ErrHintQueueFull) {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &configv1.NotifyResponse{Accepted: true, Snapshot: mapIdentity(handler.snapshot.Current().Identity())}, nil
}

var _ configv1.RefreshService = (*RefreshHandler)(nil)

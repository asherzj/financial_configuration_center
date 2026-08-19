package grpc

import (
	"context"
	"errors"
	"strings"

	controlv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/control/v1"
	access "github.com/asherzj/financial_configuration_center/internal/access/application"
	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Application interface {
	Reveal(context.Context, access.RevealCommand) (access.RevealResult, error)
}

type IdentityResolver interface {
	Subject(context.Context) (string, error)
	Roles(context.Context) ([]string, error)
	RequestID(context.Context) string
}

type DisplayNameResolver interface {
	DisplayName(context.Context) (string, error)
}

type Handler struct {
	application Application
	identity    IdentityResolver
}

func New(application Application, identity IdentityResolver) (*Handler, error) {
	if application == nil || identity == nil {
		return nil, errors.New("new SensitiveAccessService handler: application and identity are required")
	}
	return &Handler{application: application, identity: identity}, nil
}

func (handler *Handler) RevealField(ctx context.Context, request *controlv1.RevealFieldRequest) (*controlv1.RevealFieldResponse, error) {
	if request == nil || request.Scope == nil || request.ExpectedRecordRevision <= 0 || request.ExpectedCollectionRevision <= 0 || request.ExpectedModelRevision <= 0 {
		return nil, kitexstatus.Err(kitexcodes.InvalidArgument, "reveal request and positive revisions are required")
	}
	subject, err := handler.identity.Subject(ctx)
	if err != nil || strings.TrimSpace(subject) == "" {
		return nil, kitexstatus.Err(kitexcodes.Unauthenticated, "authenticated subject is required")
	}
	roles, err := handler.identity.Roles(ctx)
	if err != nil {
		return nil, kitexstatus.Err(kitexcodes.Unauthenticated, "authenticated roles are required")
	}
	displayName := ""
	if resolver, ok := handler.identity.(DisplayNameResolver); ok {
		displayName, _ = resolver.DisplayName(ctx)
	}
	requestID := handler.identity.RequestID(ctx)
	result, err := handler.application.Reveal(ctx, access.RevealCommand{
		ModelCode: request.ModelCode,
		Scope:     access.Scope{Region: request.Scope.Region, Environment: request.Scope.Environment, Stage: request.Scope.Stage},
		RecordKey: request.RecordKey, FieldName: request.FieldName,
		ExpectedRecordRevision:     catalog.ConfigRevision(request.ExpectedRecordRevision),
		ExpectedCollectionRevision: catalog.ConfigRevision(request.ExpectedCollectionRevision),
		ExpectedModelRevision:      catalog.ConfigRevision(request.ExpectedModelRevision),
		ExpectedServerEpoch:        request.ExpectedServerEpoch, ExpectedSnapshotInstance: request.ExpectedSnapshotInstance,
		ExpectedSnapshotGeneration: request.ExpectedSnapshotGeneration, Reason: request.Reason, PreviewBucket: request.PreviewBucket,
		RequestID: requestID, Principal: access.Principal{Subject: subject, DisplayName: displayName, Roles: append([]string(nil), roles...)},
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &controlv1.RevealFieldResponse{Value: result.Value, ExpiresAt: timestamppb.New(result.ExpiresAt)}, nil
}

func mapError(err error) error {
	switch {
	case errors.Is(err, access.ErrInvalid):
		return kitexstatus.Err(kitexcodes.InvalidArgument, err.Error())
	case errors.Is(err, access.ErrForbidden):
		return kitexstatus.Err(kitexcodes.PermissionDenied, err.Error())
	case errors.Is(err, access.ErrAborted):
		return kitexstatus.Err(kitexcodes.Aborted, err.Error())
	case errors.Is(err, access.ErrNotFound):
		return kitexstatus.Err(kitexcodes.NotFound, err.Error())
	case errors.Is(err, access.ErrFailedPrecondition):
		return kitexstatus.Err(kitexcodes.FailedPrecondition, err.Error())
	default:
		return kitexstatus.Err(kitexcodes.Internal, "sensitive reveal failed")
	}
}

var _ controlv1.SensitiveAccessService = (*Handler)(nil)

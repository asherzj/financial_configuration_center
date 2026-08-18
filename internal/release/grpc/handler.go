package grpc

import (
	"context"
	"errors"
	"strings"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/release/application"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	controlv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/control/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Commands interface {
	CreateRelease(context.Context, application.CreateReleaseCommand) (application.OrderView, error)
	Act(context.Context, application.ActCommand) (application.OrderView, error)
}

type ActorResolver interface {
	Subject(context.Context) (string, error)
}

type RoleResolver interface {
	Roles(context.Context) ([]string, error)
}

type Handler struct {
	commands Commands
	actors   ActorResolver
}

func New(commands Commands, actors ActorResolver) (*Handler, error) {
	if commands == nil || actors == nil {
		return nil, errors.New("new ReleaseService handler: commands and actor resolver are required")
	}
	return &Handler{commands: commands, actors: actors}, nil
}

func (handler *Handler) CreateReleaseOrder(ctx context.Context, request *controlv1.CreateReleaseOrderRequest) (*controlv1.CreateReleaseOrderResponse, error) {
	if request == nil || request.Scope == nil || strings.TrimSpace(request.ReleaseTypeCode) == "" || len(request.Items) == 0 || request.EffectiveFrom != nil || request.EffectiveUntil != nil {
		return nil, status.Error(codes.InvalidArgument, "create requires scope, release type, items, and no schedule")
	}
	actor, err := handler.actors.Subject(ctx)
	if err != nil || strings.TrimSpace(actor) == "" {
		return nil, status.Error(codes.Unauthenticated, "authenticated actor is required")
	}
	items := make([]application.ReleaseDraft, len(request.Items))
	for index, item := range request.Items {
		if item == nil || item.ExpectedRecordRevision < 0 || item.ExpectedCollectionRevision <= 0 {
			return nil, status.Error(codes.InvalidArgument, "release items require valid expected revisions")
		}
		var action release.ChangeAction
		switch item.Action {
		case commonv1.ChangeAction_CHANGE_ACTION_ADD:
			action = release.ChangeAdd
		case commonv1.ChangeAction_CHANGE_ACTION_MODIFY:
			action = release.ChangeModify
		case commonv1.ChangeAction_CHANGE_ACTION_DELETE:
			action = release.ChangeDelete
		default:
			return nil, status.Error(codes.InvalidArgument, "release item action is invalid")
		}
		items[index] = application.ReleaseDraft{
			Action: action, BaseBefore: cloneMap(item.BaseBefore), EffectiveBefore: cloneMap(item.EffectiveBefore), After: cloneMap(item.After),
			ExpectedRecordRevision:     catalog.ConfigRevision(item.ExpectedRecordRevision),
			ExpectedCollectionRevision: catalog.ConfigRevision(item.ExpectedCollectionRevision),
		}
	}
	view, err := handler.commands.CreateRelease(ctx, application.CreateReleaseCommand{
		IdempotencyKey: request.IdempotencyKey, ModelCode: request.ModelCode, ReleaseTypeCode: request.ReleaseTypeCode,
		Scope: release.Scope{Region: request.Scope.Region, Environment: request.Scope.Environment, Stage: request.Scope.Stage},
		Actor: actor, Items: items,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &controlv1.CreateReleaseOrderResponse{Detail: project(view)}, nil
}

func (handler *Handler) ActOnReleaseOrder(ctx context.Context, request *controlv1.ActOnReleaseOrderRequest) (*controlv1.ActOnReleaseOrderResponse, error) {
	if request == nil || request.ExpectedOrderRevision <= 0 || strings.TrimSpace(request.ExpectedCurrentStep) == "" {
		return nil, status.Error(codes.InvalidArgument, "order, action request, expected revision, current step, and action are required")
	}
	actor, err := handler.actors.Subject(ctx)
	if err != nil || strings.TrimSpace(actor) == "" {
		return nil, status.Error(codes.Unauthenticated, "authenticated actor is required")
	}
	var action application.Action
	switch request.Action {
	case commonv1.ReleaseAction_RELEASE_ACTION_EXECUTE:
		action = application.ActionExecute
	case commonv1.ReleaseAction_RELEASE_ACTION_ADVANCE:
		action = application.ActionAdvance
	case commonv1.ReleaseAction_RELEASE_ACTION_APPROVE:
		action = application.ActionApprove
	case commonv1.ReleaseAction_RELEASE_ACTION_REJECT:
		action = application.ActionReject
	case commonv1.ReleaseAction_RELEASE_ACTION_ROLLBACK:
		action = application.ActionRollback
	default:
		return nil, status.Error(codes.Unimplemented, "release action is not implemented")
	}
	var roles []string
	if resolver, ok := handler.actors.(RoleResolver); ok {
		roles, err = resolver.Roles(ctx)
		if err != nil {
			return nil, status.Error(codes.PermissionDenied, "actor roles could not be resolved")
		}
	}
	view, err := handler.commands.Act(ctx, application.ActCommand{
		OrderID: request.OrderId, ActionRequestID: request.ActionRequestId,
		ExpectedRevision:    release.EntityRevision(request.ExpectedOrderRevision),
		ExpectedCurrentStep: request.ExpectedCurrentStep, Action: action, Actor: actor, Roles: roles, Comment: request.Comment,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &controlv1.ActOnReleaseOrderResponse{Detail: project(view)}, nil
}

func (handler *Handler) GetReleaseOrder(context.Context, *controlv1.GetReleaseOrderRequest) (*controlv1.GetReleaseOrderResponse, error) {
	return nil, status.Error(codes.Unimplemented, "GetReleaseOrder is not implemented in the base-only slice")
}

func (handler *Handler) ListReleaseOrders(context.Context, *controlv1.ListReleaseOrdersRequest) (*controlv1.ListReleaseOrdersResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ListReleaseOrders is not implemented in the base-only slice")
}

func (handler *Handler) CreateCompensatingRelease(context.Context, *controlv1.CreateCompensatingReleaseRequest) (*controlv1.CreateCompensatingReleaseResponse, error) {
	return nil, status.Error(codes.Unimplemented, "CreateCompensatingRelease is not implemented in the base-only slice")
}

func project(view application.OrderView) *controlv1.ReleaseOrderDetail {
	allowed := make([]commonv1.ReleaseAction, 0, 4)
	if view.CanExecute {
		allowed = append(allowed, commonv1.ReleaseAction_RELEASE_ACTION_EXECUTE)
	}
	if view.CanApprove {
		allowed = append(allowed, commonv1.ReleaseAction_RELEASE_ACTION_APPROVE)
	}
	if view.CanReject {
		allowed = append(allowed, commonv1.ReleaseAction_RELEASE_ACTION_REJECT)
	}
	if view.CanAdvance {
		allowed = append(allowed, commonv1.ReleaseAction_RELEASE_ACTION_ADVANCE)
	}
	if view.CanRollback {
		allowed = append(allowed, commonv1.ReleaseAction_RELEASE_ACTION_ROLLBACK)
	}
	steps := make([]*controlv1.ReleaseStepState, len(view.Steps))
	for index, step := range view.Steps {
		ranges := make([]*controlv1.ReleaseBucketRange, len(step.RolloutRanges))
		for rangeIndex, bucketRange := range step.RolloutRanges {
			ranges[rangeIndex] = &controlv1.ReleaseBucketRange{Start: bucketRange.Start, End: bucketRange.End}
		}
		var compareResult *controlv1.ReleaseCompareResult
		if step.CompareResult != nil {
			compareResult = &controlv1.ReleaseCompareResult{
				ExpectedDigest: step.CompareResult.ExpectedDigest.Value, ActualDigest: step.CompareResult.ActualDigest.Value,
				DiffKeys: append([]string(nil), step.CompareResult.DiffKeys...), CheckedAt: timestamppb.New(step.CompareResult.CheckedAt),
			}
		}
		steps[index] = &controlv1.ReleaseStepState{
			StepCode: step.Code, StepType: string(step.Type), Sequence: int32(index), Status: string(step.Status), EntityRevision: int64(view.Revision),
			RolloutRanges: ranges, CompareResult: compareResult,
		}
	}
	return &controlv1.ReleaseOrderDetail{
		Order: &controlv1.ReleaseOrder{
			Id: view.ID, Status: toReleaseStatus(view.Status), CurrentStepCode: view.CurrentStepCode, EntityRevision: int64(view.Revision),
		},
		Items: []*controlv1.ReleaseItem{}, Steps: steps,
		AllowedActions: allowed,
	}
}

func toReleaseStatus(value release.OrderStatus) commonv1.ReleaseStatus {
	if value == release.OrderSucceeded {
		return commonv1.ReleaseStatus_RELEASE_STATUS_SUCCEEDED
	}
	if value == release.OrderRejected {
		return commonv1.ReleaseStatus_RELEASE_STATUS_REJECTED
	}
	if value == release.OrderRolledBack {
		return commonv1.ReleaseStatus_RELEASE_STATUS_ROLLED_BACK
	}
	return commonv1.ReleaseStatus_RELEASE_STATUS_IN_PROGRESS
}

func mapError(err error) error {
	switch {
	case errors.Is(err, release.ErrIdempotencyKeyReused), errors.Is(err, release.ErrActiveConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, release.ErrAborted):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, release.ErrForbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, release.ErrFailedPrecondition):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, release.ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, "release command failed")
	}
}

func cloneMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

var _ controlv1.ReleaseService = (*Handler)(nil)

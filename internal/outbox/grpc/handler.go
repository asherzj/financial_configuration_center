package grpc

import (
	"context"
	"errors"
	"strings"

	"github.com/asherzj/financial_configuration_center/internal/outbox"
	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	controlv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/control/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Operations interface {
	List(context.Context, outbox.Principal, outbox.ListRequest) (outbox.EventPage, error)
	Replay(context.Context, outbox.ReplayCommand) (outbox.Event, error)
}

type PrincipalResolver interface {
	Subject(context.Context) (string, error)
	Roles(context.Context) ([]string, error)
}

type Handler struct {
	operations Operations
	principals PrincipalResolver
}

func New(operations Operations, principals PrincipalResolver) (*Handler, error) {
	if operations == nil || principals == nil {
		return nil, errors.New("new outbox operations handler: operations and principal resolver are required")
	}
	return &Handler{operations: operations, principals: principals}, nil
}

func (handler *Handler) ListOutboxEvents(ctx context.Context, request *controlv1.ListOutboxEventsRequest) (*controlv1.ListOutboxEventsResponse, error) {
	principal, err := handler.principal(ctx)
	if err != nil {
		return nil, err
	}
	list := outbox.ListRequest{}
	if request != nil {
		if request.Status != nil {
			value := outbox.Status(strings.TrimSpace(request.GetStatus()))
			list.Status = &value
		}
		if request.Page != nil {
			list.PageNumber = int(request.Page.GetNumber())
			list.PageSize = int(request.Page.GetSize())
		}
	}
	page, err := handler.operations.List(ctx, principal, list)
	if err != nil {
		return nil, mapError(err)
	}
	events := make([]*controlv1.OutboxEvent, len(page.Events))
	for index, event := range page.Events {
		events[index] = projectEvent(event)
	}
	return &controlv1.ListOutboxEventsResponse{
		Events: events,
		Page:   &commonv1.PageResponse{Number: int32(page.PageNumber), Size: int32(page.PageSize), TotalNumber: page.TotalNumber, TotalPages: int64(page.TotalPages)},
	}, nil
}

func (handler *Handler) ReplayOutboxEvent(ctx context.Context, request *controlv1.ReplayOutboxEventRequest) (*controlv1.ReplayOutboxEventResponse, error) {
	if request == nil || request.ExpectedEventRevision <= 0 {
		return nil, status.Error(codes.InvalidArgument, "event and positive expected revision are required")
	}
	principal, err := handler.principal(ctx)
	if err != nil {
		return nil, err
	}
	event, err := handler.operations.Replay(ctx, outbox.ReplayCommand{
		EventID: request.EventId, ExpectedRevision: outbox.LeaseRevision(request.ExpectedEventRevision),
		Reason: request.Reason, Confirmation: request.Confirmation, Principal: principal,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &controlv1.ReplayOutboxEventResponse{Event: projectEvent(event)}, nil
}

func (handler *Handler) principal(ctx context.Context) (outbox.Principal, error) {
	subject, err := handler.principals.Subject(ctx)
	if err != nil || strings.TrimSpace(subject) == "" {
		return outbox.Principal{}, status.Error(codes.Unauthenticated, "authenticated actor is required")
	}
	roles, err := handler.principals.Roles(ctx)
	if err != nil {
		return outbox.Principal{}, status.Error(codes.PermissionDenied, "actor roles could not be resolved")
	}
	return outbox.Principal{Subject: subject, Roles: roles}, nil
}

func projectEvent(event outbox.Event) *controlv1.OutboxEvent {
	projected := &controlv1.OutboxEvent{
		Id: event.ID, SequenceNo: int64(event.Sequence), EventType: event.Type, Status: string(event.Status),
		LeaseRevision: int64(event.LeaseRevision), Attempts: int32(event.Attempts), NextAttemptAt: timestamppb.New(event.NextAttemptAt),
	}
	if event.LastError != "" {
		projected.LastError = &event.LastError
	}
	return projected
}

func mapError(err error) error {
	switch {
	case errors.Is(err, outbox.ErrOperationsInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, outbox.ErrOperationsForbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, outbox.ErrLeaseLost):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, outbox.ErrNotDeadLetter):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, "outbox operation failed")
	}
}

var _ controlv1.OperationsService = (*Handler)(nil)

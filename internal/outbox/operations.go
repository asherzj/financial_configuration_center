package outbox

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

const PlatformOperatorRole = "PLATFORM_OPERATOR"

var (
	ErrOperationsInvalid   = errors.New("invalid outbox operation")
	ErrOperationsForbidden = errors.New("outbox operation is forbidden")
)

type Principal struct {
	Subject string
	Roles   []string
}

type ListRequest struct {
	Status     *Status
	PageNumber int
	PageSize   int
}

type EventPage struct {
	Events      []Event
	PageNumber  int
	PageSize    int
	TotalNumber int64
	TotalPages  int
}

type OperationsRepository interface {
	List(context.Context, ListRequest) (EventPage, error)
	Replay(context.Context, ReplayRequest) (Event, error)
}

type OperationsService struct {
	repository OperationsRepository
	clock      Clock
}

func NewOperationsService(repository OperationsRepository, clock Clock) (*OperationsService, error) {
	if repository == nil || clock == nil {
		return nil, errors.New("outbox operations dependencies are required")
	}
	return &OperationsService{repository: repository, clock: clock}, nil
}

func (service *OperationsService) List(ctx context.Context, principal Principal, request ListRequest) (EventPage, error) {
	if err := authorizePlatformOperator(principal); err != nil {
		return EventPage{}, err
	}
	if request.PageNumber == 0 {
		request.PageNumber = 1
	}
	if request.PageSize == 0 {
		request.PageSize = 20
	}
	if request.PageNumber < 1 || request.PageSize < 1 || request.PageSize > 100 || request.Status != nil && !validStatus(*request.Status) {
		return EventPage{}, fmt.Errorf("%w: page or status is invalid", ErrOperationsInvalid)
	}
	return service.repository.List(ctx, request)
}

type ReplayCommand struct {
	EventID          string
	ExpectedRevision LeaseRevision
	Reason           string
	Confirmation     string
	Principal        Principal
}

func ReplayConfirmation(eventID string) string { return "REPLAY " + strings.TrimSpace(eventID) }

func (service *OperationsService) Replay(ctx context.Context, command ReplayCommand) (Event, error) {
	if err := authorizePlatformOperator(command.Principal); err != nil {
		return Event{}, err
	}
	command.EventID = strings.TrimSpace(command.EventID)
	command.Reason = strings.TrimSpace(command.Reason)
	if command.EventID == "" || command.ExpectedRevision == 0 || command.Reason == "" || command.Confirmation != ReplayConfirmation(command.EventID) {
		return Event{}, fmt.Errorf("%w: event, revision, reason, and exact confirmation are required", ErrOperationsInvalid)
	}
	return service.repository.Replay(ctx, ReplayRequest{
		EventID: command.EventID, ExpectedRevision: command.ExpectedRevision, Reason: command.Reason,
		Actor: strings.TrimSpace(command.Principal.Subject), Now: service.clock.Now().UTC(),
	})
}

func authorizePlatformOperator(principal Principal) error {
	if strings.TrimSpace(principal.Subject) == "" || !slices.Contains(principal.Roles, PlatformOperatorRole) {
		return ErrOperationsForbidden
	}
	return nil
}

func validStatus(status Status) bool {
	return status == StatusPending || status == StatusProcessing || status == StatusSent || status == StatusDeadLetter
}

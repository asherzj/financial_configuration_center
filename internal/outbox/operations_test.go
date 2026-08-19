package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/outbox"
)

func TestOutboxOperationsRequireRoleBoundPagesAndExactReplayConfirmation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 20, 7, 0, 0, 0, time.UTC)
	repository := &operationsRepositoryStub{page: outbox.EventPage{Events: []outbox.Event{{ID: "event", Status: outbox.StatusDeadLetter, LeaseRevision: 6}}, TotalNumber: 1}}
	service, err := outbox.NewOperationsService(repository, operationsClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(context.Background(), outbox.Principal{Subject: "viewer"}, outbox.ListRequest{}); !errors.Is(err, outbox.ErrOperationsForbidden) {
		t.Fatalf("unauthorized list = %v", err)
	}
	principal := outbox.Principal{Subject: "operator", Roles: []string{outbox.PlatformOperatorRole}}
	page, err := service.List(context.Background(), principal, outbox.ListRequest{})
	if err != nil || repository.lastList.PageNumber != 1 || repository.lastList.PageSize != 20 || page.TotalNumber != 1 {
		t.Fatalf("list = %+v request=%+v err=%v", page, repository.lastList, err)
	}
	if _, err := service.List(context.Background(), principal, outbox.ListRequest{PageSize: 101}); !errors.Is(err, outbox.ErrOperationsInvalid) {
		t.Fatalf("unbounded list = %v", err)
	}
	command := outbox.ReplayCommand{EventID: "event", ExpectedRevision: 6, Reason: "downstream recovered", Confirmation: "event", Principal: principal}
	if _, err := service.Replay(context.Background(), command); !errors.Is(err, outbox.ErrOperationsInvalid) {
		t.Fatalf("weak confirmation = %v", err)
	}
	command.Confirmation = outbox.ReplayConfirmation(command.EventID)
	replayed, err := service.Replay(context.Background(), command)
	if err != nil || replayed.ID != "event" || repository.lastReplay.Actor != "operator" || !repository.lastReplay.Now.Equal(now) {
		t.Fatalf("replay = %+v request=%+v err=%v", replayed, repository.lastReplay, err)
	}
}

type operationsRepositoryStub struct {
	page       outbox.EventPage
	lastList   outbox.ListRequest
	lastReplay outbox.ReplayRequest
}

func (stub *operationsRepositoryStub) List(_ context.Context, request outbox.ListRequest) (outbox.EventPage, error) {
	stub.lastList = request
	return stub.page, nil
}

func (stub *operationsRepositoryStub) Replay(_ context.Context, request outbox.ReplayRequest) (outbox.Event, error) {
	stub.lastReplay = request
	return outbox.Event{ID: request.EventID, Status: outbox.StatusPending, LeaseRevision: request.ExpectedRevision + 1}, nil
}

type operationsClock struct{ now time.Time }

func (clock operationsClock) Now() time.Time { return clock.now }

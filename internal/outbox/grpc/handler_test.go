package grpc_test

import (
	"context"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/outbox"
	outboxgrpc "github.com/asherzj/financial_configuration_center/internal/outbox/grpc"
	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	controlv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/control/v1"
)

func TestOutboxOperationsHandlerMapsBoundedMetadataAndReplayPrincipal(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 20, 7, 30, 0, 0, time.UTC)
	operations := &operationsStub{page: outbox.EventPage{
		Events:     []outbox.Event{{ID: "event", Sequence: 9, Type: "CONFIGURATION_CHANGED", Status: outbox.StatusDeadLetter, LeaseRevision: 6, Attempts: 20, NextAttemptAt: now, LastError: "delivery failed"}},
		PageNumber: 1, PageSize: 20, TotalNumber: 1, TotalPages: 1,
	}, replayed: outbox.Event{ID: "event", Status: outbox.StatusPending, LeaseRevision: 7, NextAttemptAt: now}}
	handler, err := outboxgrpc.New(operations, principalResolver{})
	if err != nil {
		t.Fatal(err)
	}
	statusValue := "DEAD_LETTER"
	listed, err := handler.ListOutboxEvents(context.Background(), &controlv1.ListOutboxEventsRequest{Status: &statusValue, Page: &commonv1.PageRequest{Number: int32Pointer(1), Size: int32Pointer(20)}})
	if err != nil || len(listed.Events) != 1 || listed.Events[0].LeaseRevision != 6 || listed.Events[0].LastError == nil || operations.lastPrincipal.Subject != "operator" || operations.lastList.Status == nil || *operations.lastList.Status != outbox.StatusDeadLetter {
		t.Fatalf("list = %+v principal=%+v request=%+v err=%v", listed, operations.lastPrincipal, operations.lastList, err)
	}
	replayed, err := handler.ReplayOutboxEvent(context.Background(), &controlv1.ReplayOutboxEventRequest{
		EventId: "event", ExpectedEventRevision: 6, Reason: "recovered", Confirmation: "REPLAY event",
	})
	if err != nil || replayed.Event.LeaseRevision != 7 || operations.lastReplay.Principal.Subject != "operator" || len(operations.lastReplay.Principal.Roles) != 1 {
		t.Fatalf("replay = %+v command=%+v err=%v", replayed, operations.lastReplay, err)
	}
}

type operationsStub struct {
	page          outbox.EventPage
	replayed      outbox.Event
	lastPrincipal outbox.Principal
	lastList      outbox.ListRequest
	lastReplay    outbox.ReplayCommand
}

func (stub *operationsStub) List(_ context.Context, principal outbox.Principal, request outbox.ListRequest) (outbox.EventPage, error) {
	stub.lastPrincipal, stub.lastList = principal, request
	return stub.page, nil
}

func (stub *operationsStub) Replay(_ context.Context, command outbox.ReplayCommand) (outbox.Event, error) {
	stub.lastReplay = command
	return stub.replayed, nil
}

type principalResolver struct{}

func (principalResolver) Subject(context.Context) (string, error) { return "operator", nil }
func (principalResolver) Roles(context.Context) ([]string, error) {
	return []string{outbox.PlatformOperatorRole}, nil
}

func int32Pointer(value int32) *int32 { return &value }

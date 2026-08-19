package rpc_test

import (
	"context"
	"errors"
	"math"
	"testing"

	configv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1"
	"github.com/asherzj/financial_configuration_center/internal/outbox"
	outboxrpc "github.com/asherzj/financial_configuration_center/internal/outbox/infrastructure/rpc"
	"github.com/cloudwego/kitex/client/callopt"
)

func TestRefreshNotifierMapsApplicationRequestToContractsRPC(t *testing.T) {
	t.Parallel()
	client := &refreshClientStub{response: &configv1.NotifyResponse{Accepted: true}}
	notifier, err := outboxrpc.NewRefreshNotifier(client)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), contextKey{}, "request")
	err = notifier.Notify(ctx, outbox.RefreshNotification{
		EventID: "event-1", Environment: "production", ReleaseOrderID: "order-1", TraceID: "trace-1",
		Targets: []outbox.RefreshTarget{{Collection: "routes", MinConfigRevision: 8, TargetCursor: 13}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.ctx != ctx {
		t.Fatal("adapter did not preserve application context")
	}
	request := client.request
	if request == nil || request.EventId != "event-1" || request.Scope == nil || request.Scope.Environment != "production" || request.ReleaseOrderId != "order-1" || request.TraceId != "trace-1" {
		t.Fatalf("request = %+v", request)
	}
	if len(request.Targets) != 1 || request.Targets[0].Collection != "routes" || request.Targets[0].MinConfigRevision != 8 || request.Targets[0].TargetCursor != 13 {
		t.Fatalf("targets = %+v", request.Targets)
	}
}

func TestRefreshNotifierFailsClosedForInvalidOrRejectedRequests(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		notification outbox.RefreshNotification
		response     *configv1.NotifyResponse
		want         error
	}{
		{name: "missing event", notification: outbox.RefreshNotification{Environment: "production", Targets: []outbox.RefreshTarget{{Collection: "routes", MinConfigRevision: 1}}}, want: outboxrpc.ErrInvalidRefreshNotification},
		{name: "revision overflow", notification: validNotification(math.MaxInt64 + 1), want: outboxrpc.ErrInvalidRefreshNotification},
		{name: "cursor overflow", notification: notificationWithCursor(math.MaxInt64 + 1), want: outboxrpc.ErrInvalidRefreshNotification},
		{name: "nil response", notification: validNotification(1), want: outboxrpc.ErrRefreshNotAccepted},
		{name: "rejected", notification: validNotification(1), response: &configv1.NotifyResponse{}, want: outboxrpc.ErrRefreshNotAccepted},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			notifier, err := outboxrpc.NewRefreshNotifier(&refreshClientStub{response: test.response})
			if err != nil {
				t.Fatal(err)
			}
			if err := notifier.Notify(context.Background(), test.notification); !errors.Is(err, test.want) {
				t.Fatalf("Notify() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestRefreshNotifierAcceptsWireIntegerMaximums(t *testing.T) {
	t.Parallel()
	client := &refreshClientStub{response: &configv1.NotifyResponse{Accepted: true}}
	notifier, err := outboxrpc.NewRefreshNotifier(client)
	if err != nil {
		t.Fatal(err)
	}
	notification := validNotification(math.MaxInt64)
	notification.Targets[0].TargetCursor = math.MaxInt64
	if err := notifier.Notify(context.Background(), notification); err != nil {
		t.Fatal(err)
	}
	if client.request.Targets[0].MinConfigRevision != math.MaxInt64 || client.request.Targets[0].TargetCursor != math.MaxInt64 {
		t.Fatalf("target = %+v", client.request.Targets[0])
	}
}

func TestRefreshNotifierPreservesRPCFailure(t *testing.T) {
	t.Parallel()
	want := errors.New("transport failed")
	notifier, err := outboxrpc.NewRefreshNotifier(&refreshClientStub{err: want})
	if err != nil {
		t.Fatal(err)
	}
	if err := notifier.Notify(context.Background(), validNotification(1)); !errors.Is(err, want) {
		t.Fatalf("Notify() error = %v, want wrapped %v", err, want)
	}
}

func TestNewRefreshNotifierRejectsNilClients(t *testing.T) {
	t.Parallel()
	if _, err := outboxrpc.NewRefreshNotifier(nil); err == nil {
		t.Fatal("NewRefreshNotifier(nil) succeeded")
	}
	var typedNil *refreshClientStub
	if _, err := outboxrpc.NewRefreshNotifier(typedNil); err == nil {
		t.Fatal("NewRefreshNotifier(typed nil) succeeded")
	}
}

func validNotification(revision uint64) outbox.RefreshNotification {
	return outbox.RefreshNotification{
		EventID: "event-1", Environment: "production",
		Targets: []outbox.RefreshTarget{{Collection: "routes", MinConfigRevision: revision}},
	}
}

func notificationWithCursor(cursor uint64) outbox.RefreshNotification {
	notification := validNotification(1)
	notification.Targets[0].TargetCursor = cursor
	return notification
}

type contextKey struct{}

type refreshClientStub struct {
	ctx      context.Context
	request  *configv1.NotifyRequest
	response *configv1.NotifyResponse
	err      error
}

func (client *refreshClientStub) Notify(ctx context.Context, request *configv1.NotifyRequest, _ ...callopt.Option) (*configv1.NotifyResponse, error) {
	client.ctx = ctx
	client.request = request
	return client.response, client.err
}

package outbox_test

import (
	"context"
	"testing"

	"github.com/asherzj/financial_configuration_center/internal/outbox"
)

func TestRefreshSenderMapsStableOutboxEnvelope(t *testing.T) {
	t.Parallel()
	notifier := &notifierStub{}
	sender, err := outbox.NewRefreshSender(notifier)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), contextKey{}, "request")
	err = sender.Send(ctx, outbox.Event{ID: "event-1", Type: "CONFIGURATION_CHANGED", PayloadVersion: 1, Payload: []byte(`{"schemaVersion":1,"collection":"routes","environment":"production","configRevision":8,"releaseOrderId":"order-1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if notifier.notification.EventID != "event-1" || notifier.notification.Targets[0].MinConfigRevision != 8 || notifier.notification.Environment != "production" {
		t.Fatalf("notification = %+v", notifier.notification)
	}
	if notifier.ctx != ctx {
		t.Fatal("sender did not preserve relay context")
	}
}

func TestNewRefreshSenderRejectsTypedNilNotifier(t *testing.T) {
	t.Parallel()
	var notifier *notifierStub
	if _, err := outbox.NewRefreshSender(notifier); err == nil {
		t.Fatal("NewRefreshSender(typed nil) succeeded")
	}
}

type contextKey struct{}

type notifierStub struct {
	ctx          context.Context
	notification outbox.RefreshNotification
}

func (notifier *notifierStub) Notify(ctx context.Context, notification outbox.RefreshNotification) error {
	notifier.ctx = ctx
	notifier.notification = notification
	return nil
}

package outbox_test

import (
	"context"
	"testing"

	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	"github.com/asherzj/financial_configuration_center/internal/outbox"
)

func TestRefreshSenderMapsStableOutboxEnvelope(t *testing.T) {
	t.Parallel()
	notifier := &notifierStub{}
	sender, err := outbox.NewRefreshSender(notifier)
	if err != nil {
		t.Fatal(err)
	}
	err = sender.Send(context.Background(), outbox.Event{ID: "event-1", Type: "CONFIGURATION_CHANGED", PayloadVersion: 1, Payload: []byte(`{"schemaVersion":1,"collection":"routes","environment":"production","configRevision":8,"releaseOrderId":"order-1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if notifier.hint.EventID != "event-1" || notifier.hint.Targets[0].MinRevision != 8 || notifier.hint.Environment != "production" {
		t.Fatalf("hint = %+v", notifier.hint)
	}
}

type notifierStub struct{ hint snapshot.RefreshHint }

func (notifier *notifierStub) Notify(hint snapshot.RefreshHint) error {
	notifier.hint = hint
	return nil
}

package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	readmodel "github.com/asherzj/financial_configuration_center/internal/distribution/readmodel"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
)

type HintNotifier interface {
	Notify(snapshot.RefreshHint) error
}

type RefreshSender struct{ notifier HintNotifier }

func NewRefreshSender(notifier HintNotifier) (*RefreshSender, error) {
	if notifier == nil {
		return nil, errors.New("new refresh sender: notifier is required")
	}
	return &RefreshSender{notifier: notifier}, nil
}

func (sender *RefreshSender) Send(_ context.Context, event Event) error {
	if event.Type != "CONFIGURATION_CHANGED" || event.PayloadVersion != 1 {
		return fmt.Errorf("unsupported outbox event %q version %d", event.Type, event.PayloadVersion)
	}
	var payload struct {
		SchemaVersion  uint32 `json:"schemaVersion"`
		Collection     string `json:"collection"`
		Environment    string `json:"environment"`
		ConfigRevision uint64 `json:"configRevision"`
		ReleaseOrderID string `json:"releaseOrderId"`
		TraceID        string `json:"traceId"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("decode configuration changed event: %w", err)
	}
	if payload.SchemaVersion != 1 || payload.Collection == "" || payload.Environment == "" || payload.ConfigRevision == 0 {
		return errors.New("configuration changed event is incomplete")
	}
	return sender.notifier.Notify(snapshot.RefreshHint{
		EventID: event.ID, Environment: payload.Environment, ReleaseOrderID: payload.ReleaseOrderID, TraceID: payload.TraceID,
		Targets: []snapshot.HintTarget{{Collection: payload.Collection, MinRevision: readmodel.ConfigRevision(payload.ConfigRevision)}},
	})
}

var _ Sender = (*RefreshSender)(nil)

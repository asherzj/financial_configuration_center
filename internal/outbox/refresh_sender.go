package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

type RefreshTarget struct {
	Collection        string
	MinConfigRevision uint64
	TargetCursor      uint64
}

type RefreshNotification struct {
	EventID        string
	Environment    string
	Targets        []RefreshTarget
	ReleaseOrderID string
	TraceID        string
}

// RefreshNotifier is the Outbox application port for asking Config Server to
// converge on an authoritative revision. Infrastructure owns the wire client.
type RefreshNotifier interface {
	Notify(context.Context, RefreshNotification) error
}

type RefreshSender struct{ notifier RefreshNotifier }

func NewRefreshSender(notifier RefreshNotifier) (*RefreshSender, error) {
	if notifier == nil || isNilRefreshNotifier(notifier) {
		return nil, errors.New("new refresh sender: notifier is required")
	}
	return &RefreshSender{notifier: notifier}, nil
}

func (sender *RefreshSender) Send(ctx context.Context, event Event) error {
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
	return sender.notifier.Notify(ctx, RefreshNotification{
		EventID: event.ID, Environment: payload.Environment, ReleaseOrderID: payload.ReleaseOrderID, TraceID: payload.TraceID,
		Targets: []RefreshTarget{{Collection: payload.Collection, MinConfigRevision: payload.ConfigRevision}},
	})
}

func isNilRefreshNotifier(notifier RefreshNotifier) bool {
	value := reflect.ValueOf(notifier)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

var _ Sender = (*RefreshSender)(nil)

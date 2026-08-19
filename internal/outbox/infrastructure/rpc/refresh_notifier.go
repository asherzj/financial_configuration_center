package rpc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"

	commonv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1/refreshservice"
	"github.com/asherzj/financial_configuration_center/internal/outbox"
)

var (
	ErrInvalidRefreshNotification = errors.New("invalid refresh notification")
	ErrRefreshNotAccepted         = errors.New("refresh notification was not accepted")
)

// RefreshNotifier adapts the Outbox application port to the versioned
// Config Server wire contract. Client construction and authentication remain
// responsibilities of the Admin runtime composition root.
type RefreshNotifier struct {
	client refreshservice.Client
}

func NewRefreshNotifier(client refreshservice.Client) (*RefreshNotifier, error) {
	if client == nil || isNil(client) {
		return nil, errors.New("new refresh notifier: client is required")
	}
	return &RefreshNotifier{client: client}, nil
}

func (notifier *RefreshNotifier) Notify(ctx context.Context, notification outbox.RefreshNotification) error {
	if ctx == nil || notification.EventID == "" || notification.Environment == "" || len(notification.Targets) == 0 {
		return ErrInvalidRefreshNotification
	}
	targets := make([]*configv1.RefreshTarget, len(notification.Targets))
	for index, target := range notification.Targets {
		if target.Collection == "" || target.MinConfigRevision == 0 || target.MinConfigRevision > math.MaxInt64 || target.TargetCursor > math.MaxInt64 {
			return fmt.Errorf("%w: target %d is incomplete or exceeds the wire range", ErrInvalidRefreshNotification, index)
		}
		targets[index] = &configv1.RefreshTarget{
			Collection:        target.Collection,
			MinConfigRevision: int64(target.MinConfigRevision),
			TargetCursor:      int64(target.TargetCursor),
		}
	}
	response, err := notifier.client.Notify(ctx, &configv1.NotifyRequest{
		EventId: notification.EventID,
		Targets: targets,
		Scope: &commonv1.Scope{
			Environment: notification.Environment,
		},
		ReleaseOrderId: notification.ReleaseOrderID,
		TraceId:        notification.TraceID,
	})
	if err != nil {
		return fmt.Errorf("notify Config Server refresh: %w", err)
	}
	if response == nil || !response.Accepted {
		return ErrRefreshNotAccepted
	}
	return nil
}

func isNil(value any) bool {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

var _ outbox.RefreshNotifier = (*RefreshNotifier)(nil)

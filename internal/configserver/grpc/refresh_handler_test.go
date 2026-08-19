package grpc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	commonv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1"
	configgrpc "github.com/asherzj/financial_configuration_center/internal/configserver/grpc"
	readmodel "github.com/asherzj/financial_configuration_center/internal/distribution/readmodel"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRefreshHandlerAcceptsHintIntoReceiver(t *testing.T) {
	t.Parallel()
	source := hintHandlerSource{}
	manager, err := snapshot.NewManager(source, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance"}, handlerClock{})
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := snapshot.NewRefreshCoordinator(manager, snapshot.RefreshCoordinatorOptions{
		ManagedEnvironment: "production", MaxPendingCollections: 1,
		InitialBackoff: time.Millisecond, MaxBackoff: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := snapshot.NewHintReceiver(coordinator, snapshot.HintReceiverOptions{ManagedEnvironment: "production", CacheSize: 10, DedupTTL: time.Minute}, handlerClock{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := configgrpc.NewRefreshHandler(receiver, manager, allowRequestAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	response, err := handler.Notify(context.Background(), &configv1.NotifyRequest{
		EventId: "event", Scope: &commonv1.Scope{Environment: " production "},
		Targets: []*configv1.RefreshTarget{{Collection: "routes", MinConfigRevision: 8}},
	})
	if err != nil || !response.Accepted || response.Snapshot.ServerEpoch != "epoch" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func TestRefreshHandlerRejectsCrossEnvironmentBeforeAuthorization(t *testing.T) {
	t.Parallel()
	notifier := &recordingHintNotifier{}
	authorizer := &recordingRefreshAuthorizer{}
	handler, err := configgrpc.NewRefreshHandler(notifier, nilSnapshotProvider{}, authorizer, "production")
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.Notify(context.Background(), &configv1.NotifyRequest{
		EventId: "event", Scope: &commonv1.Scope{Environment: "staging"},
		Targets: []*configv1.RefreshTarget{{Collection: "routes", MinConfigRevision: 8}},
	})
	if status.Code(err) != codes.InvalidArgument || notifier.called || authorizer.environment != "" {
		t.Fatalf("code=%v notified=%v authorized=%q err=%v", status.Code(err), notifier.called, authorizer.environment, err)
	}
}

func TestRefreshHandlerAuthorizesRelayBeforeQueueingHint(t *testing.T) {
	t.Parallel()
	denied := errors.New("relay denied")
	notifier := &recordingHintNotifier{}
	authorizer := &recordingRefreshAuthorizer{err: denied}
	handler, err := configgrpc.NewRefreshHandler(notifier, nilSnapshotProvider{}, authorizer, "production")
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.Notify(context.Background(), &configv1.NotifyRequest{
		EventId: "event", Scope: &commonv1.Scope{Environment: "production"},
		Targets: []*configv1.RefreshTarget{{Collection: "routes", MinConfigRevision: 8}},
	})
	if !errors.Is(err, denied) || notifier.called || authorizer.environment != "production" {
		t.Fatalf("error=%v notified=%v environment=%q", err, notifier.called, authorizer.environment)
	}
}

type hintHandlerSource struct{}

func (hintHandlerSource) LoadEnvironment(context.Context, string) ([]snapshot.CollectionInput, error) {
	return nil, nil
}

func (hintHandlerSource) LoadVersions(context.Context, string) (map[string]readmodel.ConfigRevision, error) {
	return nil, nil
}

type handlerClock struct{}

func (handlerClock) Now() time.Time { return time.Date(2026, 8, 19, 11, 0, 0, 0, time.UTC) }

type recordingHintNotifier struct{ called bool }

func (notifier *recordingHintNotifier) Notify(snapshot.RefreshHint) error {
	notifier.called = true
	return nil
}

type nilSnapshotProvider struct{}

func (nilSnapshotProvider) Current() *snapshot.Snapshot { return nil }

type recordingRefreshAuthorizer struct {
	err         error
	environment string
}

func (authorizer *recordingRefreshAuthorizer) AuthorizeRefresh(_ context.Context, environment string) error {
	authorizer.environment = environment
	return authorizer.err
}

package grpc_test

import (
	"context"
	"testing"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	configgrpc "github.com/asherzj/financial_configuration_center/internal/configserver/grpc"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/config/v1"
)

func TestRefreshHandlerAcceptsHintIntoReceiver(t *testing.T) {
	t.Parallel()
	source := hintHandlerSource{}
	manager, err := snapshot.NewManager(source, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance"}, handlerClock{})
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := snapshot.NewHintReceiver(manager, snapshot.HintReceiverOptions{ManagedEnvironment: "production", QueueSize: 1, CacheSize: 10, DedupTTL: time.Minute}, handlerClock{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := configgrpc.NewRefreshHandler(receiver, manager)
	if err != nil {
		t.Fatal(err)
	}
	response, err := handler.Notify(context.Background(), &configv1.NotifyRequest{
		EventId: "event", Scope: &commonv1.Scope{Environment: "production"},
		Targets: []*configv1.RefreshTarget{{Collection: "routes", MinConfigRevision: 8}},
	})
	if err != nil || !response.Accepted || response.Snapshot.ServerEpoch != "epoch" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

type hintHandlerSource struct{}

func (hintHandlerSource) LoadEnvironment(context.Context, string) ([]snapshot.CollectionInput, error) {
	return nil, nil
}

func (hintHandlerSource) LoadVersions(context.Context, string) (map[string]catalog.ConfigRevision, error) {
	return nil, nil
}

type handlerClock struct{}

func (handlerClock) Now() time.Time { return time.Date(2026, 8, 19, 11, 0, 0, 0, time.UTC) }

package sdk_test

import (
	"context"
	"fmt"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/sdk/finconfig"
)

type paymentRoute struct {
	Code     string `finconfig:"code"`
	Channel  string `finconfig:"channel"`
	Priority int64  `finconfig:"priority"`
	Enabled  bool   `finconfig:"enabled"`
}

// useRegion contains application code only. Production composition supplies a
// Kitex gRPC Transport configured with Consumer JWT, TLS and the target Envoy.
func useRegion(ctx context.Context, transport finconfig.Transport, region string) (*finconfig.Client, error) {
	client, err := finconfig.New(finconfig.Config{
		ConsumerID: "payment_service",
		ClientID:   "payment-service-stable-instance-01",
		Region:     region, Environment: "production", Stage: "blue",
		Transport: transport, WatchEnabled: false, PollInterval: 30 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	client.SetBeforePublish(func(change finconfig.ChangeSet) error {
		// Validate cross-collection invariants here. Returning an error retains
		// the previous immutable snapshot.
		return nil
	})
	if err := client.Start(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

func ExampleClient_queryDecodeSubscribeAndMultipleRegions() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cn, _ := useRegion(ctx, fixtureTransport("cn"), "cn")
	us, _ := useRegion(ctx, fixtureTransport("us"), "us")
	defer cn.Close(context.Background())
	defer us.Close(context.Background())

	row, found, _ := cn.QueryOne(finconfig.Query{Collection: "payment_routes", RecordKey: "route-a"})
	if found {
		route, _ := finconfig.Decode[paymentRoute](row)
		fmt.Printf("cn %s %s %d %t\n", route.Code, route.Channel, route.Priority, route.Enabled)
	}
	rows, _ := us.QueryAll("payment_routes")
	version, _, _ := us.Version("payment_routes")
	fmt.Printf("us rows=%d revision=%d\n", len(rows), version.Revision)

	stop, _ := cn.Subscribe("payment_routes", func(event finconfig.UpdateEvent) {
		// Re-query the immutable local snapshot. Event payloads never contain
		// configuration values.
		_, _, _ = cn.QueryOne(finconfig.Query{Collection: event.Collection, RecordKey: "route-a"})
	})
	stop()

	// Output:
	// cn route-a VISA 1 true
	// us rows=1 revision=1
}

type staticTransport struct{ response finconfig.SnapshotResponse }

func (transport staticTransport) GetSnapshot(context.Context, finconfig.SnapshotRequest) (finconfig.SnapshotResponse, error) {
	return transport.response, nil
}

func fixtureTransport(region string) finconfig.Transport {
	record := finconfig.Record{Key: "route-a", Revision: 1, Values: map[string]string{"code": "route-a", "channel": "VISA", "priority": "1", "enabled": "true"}}
	digest, _ := catalog.ComputeBaseDigest([]catalog.ConfigurationRecord{{RecordKey: record.Key, Data: record.Values}})
	return staticTransport{response: finconfig.SnapshotResponse{
		Identity:    finconfig.SnapshotIdentity{ServerEpoch: "demo-epoch", ServerInstanceID: "demo-server-" + region, SnapshotInstance: "demo-snapshot-" + region, Generation: 1},
		Environment: "production",
		Collections: []finconfig.CollectionPayload{{Name: "payment_routes", Revision: 1, Digest: digest.Value, Records: []finconfig.Record{record}}},
	}}
}

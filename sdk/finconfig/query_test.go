package finconfig_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/sdk/finconfig"
)

func TestLocalQueryVersionAndExplicitDecode(t *testing.T) {
	t.Parallel()
	records := []finconfig.Record{
		{Key: "b", Revision: 3, Values: map[string]string{"code": "b", "priority": "2", "enabled": "false"}},
		{Key: "a", Revision: 2, Values: map[string]string{"code": "a", "priority": "1", "enabled": "true"}},
	}
	client := refreshedClient(t, records)
	row, found, err := client.QueryOne(finconfig.Query{Collection: "routes", RecordKey: "a"})
	if err != nil || !found {
		t.Fatalf("row=%+v found=%t err=%v", row, found, err)
	}
	if value, ok := row.Get("priority"); !ok || value != "1" {
		t.Fatalf("priority=%q found=%t", value, ok)
	}
	all, err := client.QueryAll("routes")
	if err != nil || len(all) != 2 || all[0].RecordKey() != "a" || all[1].RecordKey() != "b" {
		t.Fatalf("all=%+v err=%v", all, err)
	}
	version, found, err := client.Version("routes")
	if err != nil || !found || version.Revision != 3 {
		t.Fatalf("version=%+v found=%t err=%v", version, found, err)
	}
	type route struct {
		Code     string `finconfig:"code"`
		Priority int64  `finconfig:"priority"`
		Enabled  bool   `finconfig:"enabled"`
	}
	decoded, err := finconfig.Decode[route](row)
	if err != nil || decoded.Code != "a" || decoded.Priority != 1 || !decoded.Enabled {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	clone := row.CloneMap()
	clone["priority"] = "999"
	value, _ := row.Get("priority")
	if value != "1" {
		t.Fatal("readonly row was mutable through CloneMap")
	}
}

func TestSubscribeRunsAfterAtomicPublishAndRecoversPanics(t *testing.T) {
	t.Parallel()
	record := finconfig.Record{Key: "a", Revision: 1, Values: map[string]string{"code": "a"}}
	transport := &pollTransport{response: snapshotForRecords(t, 1, record)}
	client, err := finconfig.New(finconfig.Config{ConsumerID: "consumer", ClientID: "client", Region: "cn", Environment: "production", Transport: transport, PollInterval: time.Hour, CallbackWorkers: 1, CallbackQueueSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	events := make(chan finconfig.UpdateEvent, 1)
	cancel, err := client.Subscribe("routes", func(event finconfig.UpdateEvent) {
		if event.Version.Revision == 2 {
			events <- event
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	_, err = client.Subscribe("routes", func(finconfig.UpdateEvent) { panic("consumer panic") })
	if err != nil {
		t.Fatal(err)
	}
	record.Revision = 2
	record.Values["code"] = "updated"
	transport.set(snapshotForRecords(t, 2, record))
	if err := client.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if got, _ := client.GetByKey("routes", "a"); got.Values["code"] != "updated" || event.Identity.Generation != 2 {
			t.Fatalf("event=%+v current=%+v", event, got)
		}
	case <-time.After(time.Second):
		t.Fatal("subscription event was not delivered")
	}
}

func TestLocalQueryReturnsTypedErrors(t *testing.T) {
	t.Parallel()
	client, _ := finconfig.New(finconfig.Config{ConsumerID: "consumer", ClientID: "client", Region: "cn", Environment: "production", Transport: &stubTransport{}})
	if _, _, err := client.QueryOne(finconfig.Query{Collection: "routes", RecordKey: "a"}); !errors.Is(err, finconfig.ErrNotStarted) {
		t.Fatalf("query before snapshot=%v", err)
	}
	client = refreshedClient(t, nil)
	if _, err := client.QueryAll("missing"); !errors.Is(err, finconfig.ErrCollectionNotFound) {
		t.Fatalf("missing collection=%v", err)
	}
}

func refreshedClient(t *testing.T, records []finconfig.Record) *finconfig.Client {
	t.Helper()
	client, err := finconfig.New(finconfig.Config{ConsumerID: "consumer", ClientID: "client", Region: "cn", Environment: "production", Transport: &stubTransport{response: snapshotForRecords(t, 1, records...)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	return client
}

func snapshotForRecords(t *testing.T, generation uint64, records ...finconfig.Record) finconfig.SnapshotResponse {
	t.Helper()
	revision := finconfig.ConfigRevision(generation)
	for _, record := range records {
		if record.Revision > revision {
			revision = record.Revision
		}
	}
	return finconfig.SnapshotResponse{Identity: finconfig.SnapshotIdentity{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance", Generation: generation}, Environment: "production", Collections: []finconfig.CollectionPayload{{Name: "routes", Revision: revision, Digest: digestFor(t, records...), Records: records}}}
}

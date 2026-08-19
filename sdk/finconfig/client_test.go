package finconfig_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/sdk/finconfig"
)

func TestVersionPollConvergesWithoutHintOrWatch(t *testing.T) {
	t.Parallel()
	record := finconfig.Record{Key: "route", Revision: 7, Values: map[string]string{"code": "visa", "priority": "1"}}
	transport := &pollTransport{response: finconfig.SnapshotResponse{
		Identity: finconfig.SnapshotIdentity{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance", Generation: 1}, Environment: "production",
		Collections: []finconfig.CollectionPayload{{Name: "routes", Revision: 7, Digest: digestFor(t, record), Records: []finconfig.Record{record}}},
	}}
	client, err := finconfig.New(finconfig.Config{ConsumerID: "consumer", ClientID: "client", Region: "cn", Environment: "production", Transport: transport, PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	record.Revision = 8
	record.Values["priority"] = "2"
	transport.set(finconfig.SnapshotResponse{
		Identity: finconfig.SnapshotIdentity{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance", Generation: 2}, Environment: "production",
		Collections: []finconfig.CollectionPayload{{Name: "routes", Revision: 8, Digest: digestFor(t, record), Records: []finconfig.Record{record}}},
	})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got, ok := client.GetByKey("routes", "route"); ok && got.Values["priority"] == "2" {
			if err := client.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("SDK did not converge through version poll")
}

func TestWatchAcceleratesRefreshWithoutReplacingPoll(t *testing.T) {
	t.Parallel()
	record := finconfig.Record{Key: "route", Revision: 7, Values: map[string]string{"code": "visa", "priority": "1"}}
	transport := &watchTransport{pollTransport: pollTransport{response: finconfig.SnapshotResponse{
		Identity: finconfig.SnapshotIdentity{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance", Generation: 1}, Environment: "production",
		Collections: []finconfig.CollectionPayload{{Name: "routes", Revision: 7, Digest: digestFor(t, record), Records: []finconfig.Record{record}}},
	}}, events: make(chan finconfig.WatchEvent, 1)}
	client, err := finconfig.New(finconfig.Config{
		ConsumerID: "consumer", ClientID: "client", Region: "cn", Environment: "production", Transport: transport,
		PollInterval: time.Hour, WatchEnabled: true, ReconnectBackoff: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	record.Revision = 8
	record.Values["priority"] = "2"
	identity := finconfig.SnapshotIdentity{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance", Generation: 2}
	transport.set(finconfig.SnapshotResponse{Identity: identity, Environment: "production", Collections: []finconfig.CollectionPayload{{Name: "routes", Revision: 8, Digest: digestFor(t, record), Records: []finconfig.Record{record}}}})
	transport.events <- finconfig.WatchEvent{Identity: identity}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got, _ := client.GetByKey("routes", "route"); got.Values["priority"] == "2" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("watch did not accelerate refresh")
}

func TestClientRefreshPublishesImmutableSnapshotAndRetainsLastKnownGood(t *testing.T) {
	t.Parallel()

	record := finconfig.Record{Key: "WyJ2aXNhLWNuIl0", Revision: 8, Values: map[string]string{"route_code": "visa-cn", "priority": "7"}}
	digest := digestFor(t, record)
	transport := &stubTransport{response: finconfig.SnapshotResponse{
		Identity:    finconfig.SnapshotIdentity{ServerEpoch: "epoch-1", ServerInstanceID: "server-1", SnapshotInstance: "instance-1", Generation: 1},
		Environment: "production",
		Collections: []finconfig.CollectionPayload{{Name: "payment_routes", Revision: 8, Digest: digest, Records: []finconfig.Record{record}}},
	}}
	client, err := finconfig.New(finconfig.Config{ConsumerID: "payment-service", ClientID: "pod-1", Region: "cn", Environment: "production", Transport: transport})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := client.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	record.Values["priority"] = "mutated-transport-alias"
	got, ok := client.GetByKey("payment_routes", record.Key)
	if !ok || got.Values["priority"] != "7" {
		t.Fatalf("GetByKey = %+v, %t", got, ok)
	}
	got.Values["priority"] = "mutated"
	again, _ := client.GetByKey("payment_routes", record.Key)
	if again.Values["priority"] != "7" {
		t.Fatal("SDK snapshot was mutable through a query")
	}
	if len(transport.lastRequest.KnownVersions) != 0 {
		t.Fatalf("first refresh sent known versions: %+v", transport.lastRequest)
	}
	if transport.lastRequest.Region != "cn" || client.Bucket() < 0 || client.Bucket() > 99 {
		t.Fatalf("scope or diagnostic bucket = request %+v bucket %d", transport.lastRequest, client.Bucket())
	}

	transport.err = errors.New("config server unavailable")
	if err := client.Refresh(context.Background()); err == nil {
		t.Fatal("transport failure succeeded")
	}
	again, ok = client.GetByKey("payment_routes", record.Key)
	if !ok || again.Values["priority"] != "7" || client.Identity().Generation != 1 {
		t.Fatalf("transport failure discarded last-known-good: %+v %+v", again, client.Identity())
	}
}

func TestClientExposesStableProtocolBucket(t *testing.T) {
	t.Parallel()
	client, err := finconfig.New(finconfig.Config{
		ConsumerID: "consumer", ClientID: "client", Region: "cn", Environment: "production", Transport: &stubTransport{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.Bucket() != 71 {
		t.Fatalf("Bucket = %d, want fixed protocol vector 71", client.Bucket())
	}
	if _, err := finconfig.New(finconfig.Config{ConsumerID: "consumer", ClientID: "client", Environment: "production", Transport: &stubTransport{}}); err == nil {
		t.Fatal("client without region succeeded")
	}
}

func TestClientRejectsInvalidCandidateAndCallbackFailureBeforeSwap(t *testing.T) {
	t.Parallel()

	record := finconfig.Record{Key: "key", Revision: 1, Values: map[string]string{"code": "a"}}
	transport := &stubTransport{response: finconfig.SnapshotResponse{
		Identity:    finconfig.SnapshotIdentity{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance", Generation: 1},
		Environment: "production",
		Collections: []finconfig.CollectionPayload{{Name: "routes", Revision: 1, Digest: digestFor(t, record), Records: []finconfig.Record{record}}},
	}}
	client, err := finconfig.New(finconfig.Config{ConsumerID: "consumer", ClientID: "client", Region: "cn", Environment: "production", Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	client.SetBeforePublish(func(finconfig.ChangeSet) error { return errors.New("consumer validation rejected") })
	transport.response.Identity.Generation = 2
	transport.response.Collections[0].Revision = 2
	transport.response.Collections[0].Records[0].Revision = 2
	transport.response.Collections[0].Records[0].Values["code"] = "b"
	transport.response.Collections[0].Digest = digestFor(t, transport.response.Collections[0].Records[0])
	if err := client.Refresh(context.Background()); err == nil {
		t.Fatal("callback failure succeeded")
	}
	got, _ := client.GetByKey("routes", "key")
	if got.Values["code"] != "a" || client.Identity().Generation != 1 {
		t.Fatalf("callback failure replaced last-known-good: %+v %+v", got, client.Identity())
	}

	client.SetBeforePublish(nil)
	transport.response.Collections[0].Digest = "bad"
	if err := client.Refresh(context.Background()); err == nil {
		t.Fatal("invalid digest succeeded")
	}
	got, _ = client.GetByKey("routes", "key")
	if got.Values["code"] != "a" {
		t.Fatal("invalid candidate replaced last-known-good")
	}
	client.SetBeforePublish(func(finconfig.ChangeSet) error { panic("consumer panic") })
	transport.response.Collections[0].Digest = digestFor(t, transport.response.Collections[0].Records[0])
	if err := client.Refresh(context.Background()); err == nil {
		t.Fatal("callback panic escaped as success")
	}
	got, _ = client.GetByKey("routes", "key")
	if got.Values["code"] != "a" {
		t.Fatal("callback panic replaced last-known-good")
	}
}

func TestClientMergesIncrementalCollectionsAndDeletesRevokedOnes(t *testing.T) {
	t.Parallel()

	route := finconfig.Record{Key: "route", Revision: 1, Values: map[string]string{"code": "a"}}
	bank := finconfig.Record{Key: "bank", Revision: 1, Values: map[string]string{"code": "b"}}
	transport := &stubTransport{response: finconfig.SnapshotResponse{
		Identity: finconfig.SnapshotIdentity{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance", Generation: 1}, Environment: "production",
		Collections: []finconfig.CollectionPayload{
			{Name: "routes", Revision: 1, Digest: digestFor(t, route), Records: []finconfig.Record{route}},
			{Name: "banks", Revision: 1, Digest: digestFor(t, bank), Records: []finconfig.Record{bank}},
		},
	}}
	client, err := finconfig.New(finconfig.Config{ConsumerID: "consumer", ClientID: "client", Region: "cn", Environment: "production", Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	route.Revision = 2
	route.Values["code"] = "updated"
	transport.response = finconfig.SnapshotResponse{
		Identity: finconfig.SnapshotIdentity{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance", Generation: 2}, Environment: "production",
		Collections:        []finconfig.CollectionPayload{{Name: "routes", Revision: 2, Digest: digestFor(t, route), Records: []finconfig.Record{route}}},
		DeletedCollections: []string{"banks"},
	}
	if err := client.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := client.GetByKey("routes", "route"); got.Values["code"] != "updated" {
		t.Fatalf("modified collection was not replaced: %+v", got)
	}
	if _, ok := client.GetByKey("banks", "bank"); ok {
		t.Fatal("deleted collection remains visible")
	}
}

type stubTransport struct {
	response    finconfig.SnapshotResponse
	err         error
	lastRequest finconfig.SnapshotRequest
}

type pollTransport struct {
	mu       sync.RWMutex
	response finconfig.SnapshotResponse
}

type watchTransport struct {
	pollTransport
	events chan finconfig.WatchEvent
}

func (transport *watchTransport) Watch(context.Context, finconfig.WatchRequest) (<-chan finconfig.WatchEvent, error) {
	return transport.events, nil
}

func (transport *pollTransport) GetSnapshot(context.Context, finconfig.SnapshotRequest) (finconfig.SnapshotResponse, error) {
	transport.mu.RLock()
	defer transport.mu.RUnlock()
	return cloneResponse(transport.response), nil
}

func (transport *pollTransport) set(response finconfig.SnapshotResponse) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.response = cloneResponse(response)
}

func cloneResponse(response finconfig.SnapshotResponse) finconfig.SnapshotResponse {
	cloned := response
	cloned.Collections = make([]finconfig.CollectionPayload, len(response.Collections))
	for index, collection := range response.Collections {
		cloned.Collections[index] = collection
		cloned.Collections[index].Records = make([]finconfig.Record, len(collection.Records))
		for recordIndex, record := range collection.Records {
			cloned.Collections[index].Records[recordIndex] = record
			cloned.Collections[index].Records[recordIndex].Values = cloneValues(record.Values)
		}
	}
	cloned.DeletedCollections = append([]string(nil), response.DeletedCollections...)
	return cloned
}

func cloneValues(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func (transport *stubTransport) GetSnapshot(_ context.Context, request finconfig.SnapshotRequest) (finconfig.SnapshotResponse, error) {
	transport.lastRequest = request
	if transport.err != nil {
		return finconfig.SnapshotResponse{}, transport.err
	}
	return transport.response, nil
}

func digestFor(t *testing.T, records ...finconfig.Record) string {
	t.Helper()
	ordered := append([]finconfig.Record(nil), records...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].Key < ordered[right].Key })
	payload := make([]any, len(ordered))
	for index, record := range ordered {
		payload[index] = []any{record.Key, record.Values}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

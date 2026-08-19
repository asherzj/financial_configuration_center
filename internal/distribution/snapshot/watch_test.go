package snapshot_test

import (
	"context"
	"testing"
	"time"

	readmodel "github.com/asherzj/financial_configuration_center/internal/distribution/readmodel"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
)

func TestWatchHubPublishesFirstWatermarkAndIsolatesSlowSubscriber(t *testing.T) {
	t.Parallel()
	definition, model := snapshotCatalog(t)
	source := &pollSource{versions: map[string]readmodel.ConfigRevision{"payment_routes": 7}, inputs: []snapshot.CollectionInput{{Definition: definition, Models: []readmodel.CompiledModel{model}, Version: 7}}}
	manager, err := snapshot.NewManager(source, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance"}, pollClock{})
	if err != nil {
		t.Fatal(err)
	}
	hub, err := snapshot.NewWatchHub(manager, snapshot.WatchHubOptions{QueueSize: 1, MaxSubscribers: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetPublisher(hub); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), "production"); err != nil {
		t.Fatal(err)
	}
	fast, _ := hub.Subscribe()
	defer fast.Cancel()
	first := <-fast.Events
	if first.Identity.Generation != 1 || first.Versions[0].Revision != 7 {
		t.Fatalf("first event = %+v", first)
	}
	slow, _ := hub.Subscribe()
	defer slow.Cancel()

	source.set(map[string]readmodel.ConfigRevision{"payment_routes": 8}, []snapshot.CollectionInput{{Definition: definition, Models: []readmodel.CompiledModel{model}, Version: 8}})
	started := time.Now()
	if _, err := manager.Refresh(context.Background(), "production"); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("slow watch blocked snapshot publication")
	}
	if update := <-fast.Events; update.Identity.Generation != 2 || update.ResyncRequired {
		t.Fatalf("fast update = %+v", update)
	}
	if resync := <-slow.Events; !resync.ResyncRequired || resync.Identity.Generation != 2 {
		t.Fatalf("slow update = %+v", resync)
	}
	if _, open := <-slow.Events; open {
		t.Fatal("overflowed watch remained open")
	}
}

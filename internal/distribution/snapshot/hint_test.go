package snapshot_test

import (
	"context"
	"errors"
	"testing"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
)

func TestHintReceiverDeduplicatesQueuesAndSkipsOldWatermarks(t *testing.T) {
	t.Parallel()
	definition, model := snapshotCatalog(t)
	source := &pollSource{versions: map[string]catalog.ConfigRevision{"payment_routes": 7}, inputs: []snapshot.CollectionInput{{Definition: definition, Models: []catalog.CompiledModel{model}, Version: 7}}}
	manager, err := snapshot.NewManager(source, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance"}, pollClock{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), "production"); err != nil {
		t.Fatal(err)
	}
	receiver, err := snapshot.NewHintReceiver(manager, snapshot.HintReceiverOptions{ManagedEnvironment: "production", QueueSize: 1, CacheSize: 2, DedupTTL: time.Minute}, pollClock{})
	if err != nil {
		t.Fatal(err)
	}
	hint := snapshot.RefreshHint{EventID: "event-1", Environment: "production", Targets: []snapshot.HintTarget{{Collection: "payment_routes", MinRevision: 8}}}
	if err := receiver.Notify(hint); err != nil {
		t.Fatal(err)
	}
	if err := receiver.Notify(hint); err != nil {
		t.Fatalf("duplicate hint: %v", err)
	}
	if err := receiver.Notify(snapshot.RefreshHint{EventID: "event-2", Environment: "production", Targets: []snapshot.HintTarget{{Collection: "payment_routes", MinRevision: 9}}}); !errors.Is(err, snapshot.ErrHintQueueFull) {
		t.Fatalf("full queue = %v", err)
	}
	source.set(map[string]catalog.ConfigRevision{"payment_routes": 8}, []snapshot.CollectionInput{{Definition: definition, Models: []catalog.CompiledModel{model}, Version: 8}})
	if err := receiver.ProcessNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.Current().Identity().Generation != 2 {
		t.Fatalf("generation = %d", manager.Current().Identity().Generation)
	}
	if err := receiver.Notify(snapshot.RefreshHint{EventID: "old-event", Environment: "production", Targets: []snapshot.HintTarget{{Collection: "payment_routes", MinRevision: 7}}}); err != nil {
		t.Fatalf("old hint: %v", err)
	}
}

func TestHintReceiverRejectsAnotherManagedEnvironment(t *testing.T) {
	t.Parallel()
	definition, model := snapshotCatalog(t)
	source := &pollSource{versions: map[string]catalog.ConfigRevision{"payment_routes": 7}, inputs: []snapshot.CollectionInput{{Definition: definition, Models: []catalog.CompiledModel{model}, Version: 7}}}
	manager, err := snapshot.NewManager(source, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance"}, pollClock{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), "production"); err != nil {
		t.Fatal(err)
	}
	receiver, err := snapshot.NewHintReceiver(manager, snapshot.HintReceiverOptions{
		ManagedEnvironment: "production", QueueSize: 1, CacheSize: 2, DedupTTL: time.Minute,
	}, pollClock{})
	if err != nil {
		t.Fatal(err)
	}
	err = receiver.Notify(snapshot.RefreshHint{
		EventID: "wrong-environment", Environment: "staging",
		Targets: []snapshot.HintTarget{{Collection: "payment_routes", MinRevision: 8}},
	})
	if !errors.Is(err, snapshot.ErrManagedEnvironmentMismatch) {
		t.Fatalf("cross-environment hint error = %v", err)
	}
	if manager.Current().Environment() != "production" || manager.Current().Identity().Generation != 1 {
		t.Fatalf("cross-environment hint changed snapshot environment=%q identity=%+v", manager.Current().Environment(), manager.Current().Identity())
	}
}

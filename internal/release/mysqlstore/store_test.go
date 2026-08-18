package mysqlstore_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/adminbff"
	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/configserver"
	"github.com/asherzj/financial_configuration_center/internal/distribution/mysqlsource"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	"github.com/asherzj/financial_configuration_center/internal/outbox"
	outboxmysqlstore "github.com/asherzj/financial_configuration_center/internal/outbox/mysqlstore"
	"github.com/asherzj/financial_configuration_center/internal/pagequery"
	platformmysql "github.com/asherzj/financial_configuration_center/internal/platform/mysql"
	"github.com/asherzj/financial_configuration_center/internal/platform/mysql/migrations"
	"github.com/asherzj/financial_configuration_center/internal/release/application"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
	"github.com/asherzj/financial_configuration_center/internal/release/mysqlstore"
	"github.com/asherzj/financial_configuration_center/sdk/finconfig"
	drivermysql "github.com/go-sql-driver/mysql"
)

func TestRealMySQLPollConvergesWithoutHintOrWatch(t *testing.T) {
	dsn := isolatedDatabase(t)
	ctx := context.Background()
	database, err := platformmysql.Open(ctx, platformmysql.Config{DSN: dsn, MaxOpenConns: 8, MaxIdleConns: 8, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	clock := fixedClock{now: time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)}
	source, err := mysqlsource.New(database)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := snapshot.NewManager(source, snapshot.IdentitySeed{ServerEpoch: "epoch-poll", ServerInstanceID: "server-poll", SnapshotInstance: "snapshot-poll"}, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(ctx, "production"); err != nil {
		t.Fatal(err)
	}
	poller, err := snapshot.NewVersionPoller(manager, source, snapshot.VersionPollerOptions{Environment: "production", Interval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	pollContext, stopPoller := context.WithCancel(ctx)
	defer stopPoller()
	go func() { _ = poller.Run(pollContext) }()

	configService := configserver.New(manager, source)
	sdkClient, err := finconfig.New(finconfig.Config{ConsumerID: "payment-service", ClientID: "pod-poll", Region: "cn", Environment: "production", Transport: configTransport{service: configService}, PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := sdkClient.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sdkClient.Close(context.Background()) })

	releaseStore, err := mysqlstore.New(database)
	if err != nil {
		t.Fatal(err)
	}
	releaseService := application.NewService(releaseStore, &numberedIDs{next: 800, releaseNumber: "REL-20260819-0800"}, clock)
	created, err := releaseService.CreateBaseFinal(ctx, baseCreate("create-poll", "actor-poll", "production", "poll-route", 7))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := releaseService.Act(ctx, application.ActCommand{OrderID: created.ID, ActionRequestID: "30000000-0000-4000-8000-000000000001", ExpectedRevision: 1, ExpectedCurrentStep: "base-apply", Action: application.ActionExecute, Actor: "actor-poll"}); err != nil {
		t.Fatal(err)
	}
	definition, _ := manager.Current().Definition("payment_routes")
	record, _ := definition.NewRecord("production", map[string]string{"route_code": "poll-route", "priority": "1"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, found := sdkClient.GetByKey("payment_routes", record.RecordKey); found && got.Values["route_code"] == "poll-route" {
			raw, _ := sql.Open("mysql", dsn)
			defer raw.Close()
			assertCount(t, raw, `SELECT COUNT(*) FROM outbox_events WHERE status = 'PENDING'`, 1)
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("configuration did not converge through server and SDK version polls")
}

func TestRealMySQLOutboxMultiWorkerLeaseCAS(t *testing.T) {
	dsn := isolatedDatabase(t)
	ctx := context.Background()
	database, err := platformmysql.Open(ctx, platformmysql.Config{DSN: dsn, MaxOpenConns: 8, MaxIdleConns: 8, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	storeA, err := outboxmysqlstore.New(database)
	if err != nil {
		t.Fatal(err)
	}
	storeB, _ := outboxmysqlstore.New(database)
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	raw, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for index := 1; index <= 3; index++ {
		if _, err := raw.Exec(`
			INSERT INTO outbox_events (
				id, aggregate_type, aggregate_id, event_type, payload_version, payload,
				idempotency_key, status, lease_revision, attempts, next_attempt_at, created_at, updated_at
			) VALUES (?, 'RELEASE_ORDER', 'order', 'CONFIGURATION_CHANGED', 1, JSON_OBJECT('schemaVersion', 1), ?, 'PENDING', 1, 0, ?, ?, ?)
		`, fmt.Sprintf("10000000-0000-4000-8000-%012d", index), fmt.Sprintf("event-%d", index), now, now, now); err != nil {
			t.Fatal(err)
		}
	}
	type claimResult struct {
		events []outbox.Event
		err    error
	}
	claims := make(chan claimResult, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for _, input := range []struct {
		store  *outboxmysqlstore.Store
		worker string
	}{{storeA, "relay-a"}, {storeB, "relay-b"}} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			events, claimErr := input.store.Claim(ctx, outbox.ClaimRequest{WorkerID: input.worker, Limit: 2, Now: now, LeaseDuration: 30 * time.Second})
			claims <- claimResult{events: events, err: claimErr}
		}()
	}
	close(start)
	wait.Wait()
	close(claims)
	seen := make(map[string]outbox.Event)
	for claim := range claims {
		if claim.err != nil {
			t.Fatal(claim.err)
		}
		for _, event := range claim.events {
			if _, duplicate := seen[event.ID]; duplicate {
				t.Fatalf("event %s was claimed twice", event.ID)
			}
			seen[event.ID] = event
		}
	}
	if len(seen) != 3 {
		t.Fatalf("claimed %d events, want 3", len(seen))
	}
	for _, event := range seen {
		stale := event
		stale.LeaseRevision--
		if err := storeA.MarkSent(ctx, stale, now); !errors.Is(err, outbox.ErrLeaseLost) {
			t.Fatalf("stale lease update = %v", err)
		}
		owner := storeA
		if event.LockedBy == "relay-b" {
			owner = storeB
		}
		if err := owner.MarkSent(ctx, event, now); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(t, raw, `SELECT COUNT(*) FROM outbox_events WHERE status = 'SENT'`, 3)

	if _, err := raw.Exec(`
		INSERT INTO outbox_events (
			id, aggregate_type, aggregate_id, event_type, payload_version, payload,
			idempotency_key, status, lease_revision, attempts, next_attempt_at,
			locked_by, locked_until, created_at, updated_at
		) VALUES ('20000000-0000-4000-8000-000000000001', 'RELEASE_ORDER', 'order', 'CONFIGURATION_CHANGED', 1,
			JSON_OBJECT('schemaVersion', 1), 'expired-event', 'PROCESSING', 4, 1, ?, 'dead-relay', ?, ?, ?)
	`, now, now.Add(-time.Second), now, now); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := storeA.Claim(ctx, outbox.ClaimRequest{WorkerID: "relay-a", Limit: 1, Now: now, LeaseDuration: 30 * time.Second})
	if err != nil || len(reclaimed) != 1 || reclaimed[0].LeaseRevision != 5 || reclaimed[0].Attempts != 2 {
		t.Fatalf("expired lease reclaim = %+v, %v", reclaimed, err)
	}
	status, err := storeA.MarkFailed(ctx, reclaimed[0], "still unavailable", now.Add(time.Minute), 2, now)
	if err != nil || status != outbox.StatusDeadLetter {
		t.Fatalf("dead letter = %s, %v", status, err)
	}
	assertCount(t, raw, `SELECT COUNT(*) FROM outbox_events WHERE status = 'DEAD_LETTER' AND lease_revision = 6`, 1)
	if _, err := storeA.Replay(ctx, outbox.ReplayRequest{EventID: reclaimed[0].ID, ExpectedRevision: 5, Reason: "endpoint recovered", Actor: "platform-operator", Now: now.Add(time.Minute)}); !errors.Is(err, outbox.ErrLeaseLost) {
		t.Fatalf("stale replay = %v", err)
	}
	replayed, err := storeA.Replay(ctx, outbox.ReplayRequest{EventID: reclaimed[0].ID, ExpectedRevision: 6, Reason: "endpoint recovered", Actor: "platform-operator", Now: now.Add(time.Minute)})
	if err != nil || replayed.Status != outbox.StatusPending || replayed.LeaseRevision != 7 || replayed.Attempts != 0 {
		t.Fatalf("replayed event = %+v, %v", replayed, err)
	}
	assertCount(t, raw, `SELECT COUNT(*) FROM audit_records WHERE action = 'OUTBOX_REPLAY' AND resource_id = '20000000-0000-4000-8000-000000000001'`, 1)
}

func TestRealMySQLReleaseConcurrencyAndIdempotency(t *testing.T) {
	dsn := isolatedDatabase(t)
	ctx := context.Background()
	database, err := platformmysql.Open(ctx, platformmysql.Config{DSN: dsn, MaxOpenConns: 12, MaxIdleConns: 12, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store, err := mysqlstore.New(database)
	if err != nil {
		t.Fatal(err)
	}
	clock := fixedClock{now: time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)}
	service := application.NewService(store, &numberedIDs{next: 100, releaseNumber: "REL-20260819-0100"}, clock)
	create := baseCreate("create-visa", "actor", "production", "visa", 7)
	created, err := service.CreateBaseFinal(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	if replayed, err := service.CreateBaseFinal(ctx, create); err != nil || !reflect.DeepEqual(replayed, created) {
		t.Fatalf("create replay = %+v, %v; want %+v", replayed, err, created)
	}
	changed := create
	changed.Items = []application.AddDraft{{Data: map[string]string{"route_code": "visa", "priority": "2"}, ExpectedCollectionRevision: 7}}
	if _, err := service.CreateBaseFinal(ctx, changed); !errors.Is(err, release.ErrIdempotencyKeyReused) {
		t.Fatalf("changed create replay = %v", err)
	}

	action := application.ActCommand{OrderID: created.ID, ActionRequestID: "action-visa", ExpectedRevision: 1, ExpectedCurrentStep: "base-apply", Action: application.ActionExecute, Actor: "actor"}
	type actionResult struct {
		view application.OrderView
		err  error
	}
	actions := make(chan actionResult, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			view, actionErr := service.Act(ctx, action)
			actions <- actionResult{view: view, err: actionErr}
		}()
	}
	wait.Wait()
	close(actions)
	var first application.OrderView
	for result := range actions {
		if result.err != nil {
			t.Fatalf("concurrent action replay: %v", result.err)
		}
		if first.ID == "" {
			first = result.view
		} else if !reflect.DeepEqual(result.view, first) {
			t.Fatalf("action results differ: %+v and %+v", first, result.view)
		}
	}
	if _, err := service.Act(ctx, application.ActCommand{OrderID: created.ID, ActionRequestID: "new-stale-action", ExpectedRevision: 1, ExpectedCurrentStep: "base-apply", Action: application.ActionExecute, Actor: "actor"}); !errors.Is(err, release.ErrAborted) {
		t.Fatalf("new stale action = %v", err)
	}

	assertDatabaseCount(t, dsn, `SELECT COUNT(*) FROM configuration_records WHERE environment = 'production' AND record_key IS NOT NULL`, 1)
	assertDatabaseCount(t, dsn, `SELECT COUNT(*) FROM outbox_events`, 1)
	assertDatabaseCount(t, dsn, `SELECT COUNT(*) FROM release_action_requests WHERE release_order_id = '`+created.ID+`'`, 1)

	sameTarget := baseCreate("", "", "production", "mastercard", 8)
	conflictResults := concurrentCreates(ctx,
		application.NewService(store, &numberedIDs{next: 200, releaseNumber: "REL-20260819-0200"}, clock), withRequestIdentity(sameTarget, "create-mastercard-a", "actor-a"),
		application.NewService(store, &numberedIDs{next: 300, releaseNumber: "REL-20260819-0300"}, clock), withRequestIdentity(sameTarget, "create-mastercard-b", "actor-b"),
	)
	var succeeded, conflicted int
	for _, result := range conflictResults {
		switch {
		case result.err == nil:
			succeeded++
		case errors.Is(result.err, release.ErrActiveConflict):
			conflicted++
		default:
			t.Fatalf("same-target create = %v", result.err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("same-target outcomes: success=%d conflict=%d", succeeded, conflicted)
	}

	environmentResults := concurrentCreates(ctx,
		application.NewService(store, &numberedIDs{next: 400, releaseNumber: "REL-20260819-0400"}, clock), baseCreate("create-amex-prod", "actor-prod", "production", "amex", 8),
		application.NewService(store, &numberedIDs{next: 500, releaseNumber: "REL-20260819-0500"}, clock), baseCreate("create-amex-stage", "actor-stage", "staging", "amex", 7),
	)
	for _, result := range environmentResults {
		if result.err != nil {
			t.Fatalf("different-environment create: %v", result.err)
		}
	}

	replayCommand := baseCreate("create-discover", "actor-replay", "production", "discover", 8)
	replayResults := concurrentCreates(ctx,
		application.NewService(store, &numberedIDs{next: 600, releaseNumber: "REL-20260819-0600"}, clock), replayCommand,
		application.NewService(store, &numberedIDs{next: 700, releaseNumber: "REL-20260819-0700"}, clock), replayCommand,
	)
	if replayResults[0].err != nil || replayResults[1].err != nil || replayResults[0].view.ID != replayResults[1].view.ID {
		t.Fatalf("concurrent create replay = %+v", replayResults)
	}
}

func TestRealMySQLManualApprovalJourney(t *testing.T) {
	dsn := isolatedDatabase(t)
	ctx := context.Background()
	database, err := platformmysql.Open(ctx, platformmysql.Config{DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 2, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	raw, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`
		INSERT INTO release_templates (
			code, version, model_code, release_type_code, active_slot, final_effect,
			template, created_at, created_by
		) VALUES (
			'approval-final', 1, 'payment-route-admin', 'approval', 'A', 'BASE_FINAL',
			JSON_OBJECT('steps', JSON_ARRAY(
				JSON_OBJECT('code', 'review', 'type', 'MANUAL_REVIEW', 'requiredRoles', JSON_ARRAY('RELEASE_APPROVER'), 'params', JSON_OBJECT('selfApprovalPolicy', 'DENY_PRODUCTION')),
				JSON_OBJECT('code', 'apply', 'type', 'BASE_APPLY', 'params', JSON_OBJECT('cleanupScopeOverlay', TRUE)),
				JSON_OBJECT('code', 'done', 'type', 'COMPLETE', 'params', JSON_OBJECT())
			)), UTC_TIMESTAMP(6), 'seed'
		)
	`); err != nil {
		t.Fatal(err)
	}

	clock := fixedClock{now: time.Date(2026, 8, 19, 11, 0, 0, 0, time.UTC)}
	store, err := mysqlstore.New(database)
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, &numberedIDs{next: 900, releaseNumber: "REL-20260819-0900"}, clock)
	created, err := service.CreateBaseFinal(ctx, application.CreateBaseFinalCommand{
		IdempotencyKey: "approval-create", ModelCode: "payment-route-admin", ReleaseTypeCode: "approval",
		Scope: release.Scope{Region: "cn", Environment: "production"}, Actor: "creator@example.com",
		Items: []application.AddDraft{{Data: map[string]string{"route_code": "approval-route", "priority": "1"}, ExpectedCollectionRevision: 7}},
	})
	if err != nil || created.CurrentStep != release.StepManualReview || created.CurrentStepStatus != release.StepPending || !created.CanExecute || len(created.Steps) != 3 || created.Steps[1].Code != "apply" {
		t.Fatalf("create = %+v, %v", created, err)
	}

	submitted, err := service.Act(ctx, application.ActCommand{
		OrderID: created.ID, ActionRequestID: "approval-submit", ExpectedRevision: 1,
		ExpectedCurrentStep: "review", Action: application.ActionExecute, Actor: "creator@example.com",
	})
	if err != nil || submitted.Revision != 2 || !submitted.CanApprove || !submitted.CanReject {
		t.Fatalf("submit = %+v, %v", submitted, err)
	}
	if _, err := service.Act(ctx, application.ActCommand{
		OrderID: created.ID, ActionRequestID: "approval-self", ExpectedRevision: 2,
		ExpectedCurrentStep: "review", Action: application.ActionApprove,
		Actor: "creator@example.com", Roles: []string{"RELEASE_APPROVER"},
	}); !errors.Is(err, release.ErrForbidden) {
		t.Fatalf("self approve = %v", err)
	}
	assertCount(t, raw, `SELECT COUNT(*) FROM release_operation_logs WHERE release_order_id = '`+created.ID+`'`, 1)

	approveCommand := application.ActCommand{
		OrderID: created.ID, ActionRequestID: "approval-approve", ExpectedRevision: 2,
		ExpectedCurrentStep: "review", Action: application.ActionApprove,
		Actor: "approver@example.com", Roles: []string{"RELEASE_APPROVER"}, Comment: "reviewed",
	}
	approved, err := service.Act(ctx, approveCommand)
	if err != nil || approved.Revision != 3 || !approved.CanAdvance {
		t.Fatalf("approve = %+v, %v", approved, err)
	}
	if replayed, err := service.Act(ctx, approveCommand); err != nil || !reflect.DeepEqual(replayed, approved) {
		t.Fatalf("approve replay = %+v, %v; want %+v", replayed, err, approved)
	}

	advanced, err := service.Act(ctx, application.ActCommand{OrderID: created.ID, ActionRequestID: "approval-advance-review", ExpectedRevision: 3, ExpectedCurrentStep: "review", Action: application.ActionAdvance, Actor: "operator@example.com"})
	if err != nil || advanced.CurrentStep != release.StepBaseApply || advanced.Revision != 4 || !advanced.CanExecute {
		t.Fatalf("advance review = %+v, %v", advanced, err)
	}
	applied, err := service.Act(ctx, application.ActCommand{OrderID: created.ID, ActionRequestID: "approval-apply", ExpectedRevision: 4, ExpectedCurrentStep: "apply", Action: application.ActionExecute, Actor: "operator@example.com"})
	if err != nil || applied.Revision != 5 || !applied.CanAdvance {
		t.Fatalf("apply = %+v, %v", applied, err)
	}
	advanced, err = service.Act(ctx, application.ActCommand{OrderID: created.ID, ActionRequestID: "approval-advance-apply", ExpectedRevision: 5, ExpectedCurrentStep: "apply", Action: application.ActionAdvance, Actor: "operator@example.com"})
	if err != nil || advanced.CurrentStep != release.StepComplete || advanced.Revision != 6 || !advanced.CanExecute {
		t.Fatalf("advance apply = %+v, %v", advanced, err)
	}
	completed, err := service.Act(ctx, application.ActCommand{OrderID: created.ID, ActionRequestID: "approval-complete", ExpectedRevision: 6, ExpectedCurrentStep: "done", Action: application.ActionExecute, Actor: "operator@example.com"})
	if err != nil || completed.Status != release.OrderSucceeded || completed.Revision != 7 || completed.CanExecute || completed.CanAdvance {
		t.Fatalf("complete = %+v, %v", completed, err)
	}

	assertCount(t, raw, `SELECT COUNT(*) FROM release_operation_logs WHERE release_order_id = '`+created.ID+`'`, 6)
	assertCount(t, raw, `SELECT COUNT(*) FROM release_action_requests WHERE release_order_id = '`+created.ID+`'`, 6)
	assertCount(t, raw, `SELECT COUNT(*) FROM configuration_records WHERE environment = 'production'`, 1)
	assertCount(t, raw, `SELECT COUNT(*) FROM release_step_states WHERE release_order_id = '`+created.ID+`' AND step_code = 'review' AND status = 'APPROVED' AND JSON_UNQUOTE(JSON_EXTRACT(approval, '$.status')) = 'APPROVED'`, 1)
}

func TestRealMySQLOverlayApplyAndRollbackTransaction(t *testing.T) {
	dsn := isolatedDatabase(t)
	ctx := context.Background()
	database, err := platformmysql.Open(ctx, platformmysql.Config{DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 2, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	raw, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	if _, err := raw.Exec(`
		INSERT INTO release_templates (
			code, version, model_code, release_type_code, active_slot, final_effect,
			template, created_at, created_by
		) VALUES (
			'overlay-final', 1, 'payment-route-admin', 'scope', 'A', 'OVERLAY_FINAL',
			JSON_OBJECT('steps', JSON_ARRAY(
				JSON_OBJECT('code', 'apply-overlay', 'type', 'OVERLAY_APPLY', 'params', JSON_OBJECT()),
				JSON_OBJECT('code', 'done', 'type', 'COMPLETE', 'params', JSON_OBJECT())
			)), ?, 'seed'
		)
	`, now); err != nil {
		t.Fatal(err)
	}
	baseData := map[string]string{"route_code": "visa", "priority": "1", "enabled": "false"}
	key, err := catalog.EncodeKey([]string{"route_code"}, baseData)
	if err != nil {
		t.Fatal(err)
	}
	baseJSON, _ := json.Marshal(baseData)
	if _, err := raw.Exec(`
		INSERT INTO configuration_records (
			collection_name, environment, record_key, data, config_revision,
			created_at, created_by, updated_at, updated_by
		) VALUES ('payment_routes', 'production', ?, ?, 5, ?, 'seed', ?, 'seed')
	`, key, baseJSON, now, now); err != nil {
		t.Fatal(err)
	}

	store, err := mysqlstore.New(database)
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, &numberedIDs{next: 1000, releaseNumber: "REL-20260819-1000"}, fixedClock{now: now})
	created, err := service.CreateRelease(ctx, application.CreateReleaseCommand{
		IdempotencyKey: "overlay-create", ModelCode: "payment-route-admin", ReleaseTypeCode: "scope",
		Scope: release.Scope{Region: "cn", Environment: "production", Stage: "blue"}, Actor: "operator@example.com",
		Items: []application.ReleaseDraft{{
			Action: release.ChangeModify, BaseBefore: baseData, EffectiveBefore: baseData,
			After:                  map[string]string{"route_code": "visa", "priority": "2", "enabled": "false"},
			ExpectedRecordRevision: 5, ExpectedCollectionRevision: 7,
		}},
	})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	executed, err := service.Act(ctx, application.ActCommand{
		OrderID: created.ID, ActionRequestID: "40000000-0000-4000-8000-000000000001",
		ExpectedRevision: 1, ExpectedCurrentStep: "apply-overlay", Action: application.ActionExecute, Actor: "operator@example.com",
	})
	if err != nil {
		t.Fatalf("execute overlay: %v", err)
	}
	assertCount(t, raw, `SELECT COUNT(*) FROM configuration_overlays WHERE environment = 'production' AND stage = 'blue' AND JSON_UNQUOTE(JSON_EXTRACT(content, '$.priority')) = '2'`, 1)
	assertCount(t, raw, `SELECT COUNT(*) FROM configuration_versions WHERE collection_name = 'payment_routes' AND environment = 'production' AND config_revision = 8 AND overlay_digest <> '4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945'`, 1)
	assertCount(t, raw, `SELECT COUNT(*) FROM release_step_states WHERE release_order_id = '`+created.ID+`' AND JSON_EXTRACT(effect, '$.effectVersion') = 1 AND JSON_EXTRACT(effect, '$.overlay.appliedRevision') = 8`, 1)
	assertCount(t, raw, `SELECT COUNT(*) FROM configuration_change_log WHERE release_order_id = '`+created.ID+`' AND kind = 'OVERLAY'`, 1)
	assertCount(t, raw, `SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = '`+created.ID+`'`, 1)
	distributionSource, err := mysqlsource.New(database)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := snapshot.NewManager(distributionSource, snapshot.IdentitySeed{ServerEpoch: "overlay-epoch", ServerInstanceID: "overlay-server", SnapshotInstance: "overlay-snapshot"}, fixedClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(ctx, "production"); err != nil {
		t.Fatal(err)
	}
	page, err := pagequery.New(manager).Query(pagequery.Request{ModelCode: "payment-route-admin", Region: "cn", Environment: "production", Stage: "blue", Type: pagequery.TypeAll})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 1 || page.Rows[0].Values["priority"] != "2" || page.Rows[0].BaseValues["priority"] != "1" {
		t.Fatalf("scope-aware MySQL page = %+v", page)
	}

	rolledBack, err := service.Act(ctx, application.ActCommand{
		OrderID: created.ID, ActionRequestID: "40000000-0000-4000-8000-000000000002",
		ExpectedRevision: executed.Revision, ExpectedCurrentStep: "apply-overlay", Action: application.ActionRollback, Actor: "operator@example.com",
	})
	if err != nil {
		t.Fatalf("rollback overlay: %v", err)
	}
	if rolledBack.Status != release.OrderRolledBack {
		t.Fatalf("rolled back view = %+v", rolledBack)
	}
	assertCount(t, raw, `SELECT COUNT(*) FROM configuration_overlays WHERE environment = 'production' AND stage = 'blue'`, 0)
	assertCount(t, raw, `SELECT COUNT(*) FROM configuration_versions WHERE collection_name = 'payment_routes' AND environment = 'production' AND config_revision = 9 AND overlay_digest = '4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945'`, 1)
	assertCount(t, raw, `SELECT COUNT(*) FROM release_orders WHERE id = '`+created.ID+`' AND status = 'ROLLED_BACK'`, 1)
	assertCount(t, raw, `SELECT COUNT(*) FROM release_step_states WHERE release_order_id = '`+created.ID+`' AND status = 'ROLLED_BACK' AND JSON_EXTRACT(effect, '$.overlay.appliedRevision') = 8`, 1)
	assertCount(t, raw, `SELECT COUNT(*) FROM configuration_change_log WHERE release_order_id = '`+created.ID+`' AND kind = 'OVERLAY'`, 2)
	assertCount(t, raw, `SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = '`+created.ID+`'`, 2)
}

func TestRealMySQLPercentageRolloutTransaction(t *testing.T) {
	dsn := isolatedDatabase(t)
	ctx := context.Background()
	database, err := platformmysql.Open(ctx, platformmysql.Config{DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 2, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	raw, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	now := time.Date(2026, 8, 19, 16, 0, 0, 0, time.UTC)
	if _, err := raw.Exec(`
		INSERT INTO release_templates (
			code, version, model_code, release_type_code, active_slot, final_effect,
			template, created_at, created_by
		) VALUES (
			'percent-final', 1, 'payment-route-admin', 'percentage', 'A', 'BASE_FINAL',
			JSON_OBJECT('steps', JSON_ARRAY(
				JSON_OBJECT('code', 'percent-10', 'type', 'PERCENT_ROLLOUT', 'params', JSON_OBJECT('ranges', JSON_ARRAY(JSON_OBJECT('start', 0, 'end', 9)))),
				JSON_OBJECT('code', 'compare', 'type', 'COMPARE', 'params', JSON_OBJECT('mode', 'EFFECTIVE', 'previewBucket', 6)),
				JSON_OBJECT('code', 'promote', 'type', 'BASE_APPLY', 'params', JSON_OBJECT('cleanupScopeOverlay', TRUE)),
				JSON_OBJECT('code', 'complete', 'type', 'COMPLETE', 'params', JSON_OBJECT())
			)), ?, 'seed'
		)
	`, now); err != nil {
		t.Fatal(err)
	}
	store, err := mysqlstore.New(database)
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, &numberedIDs{next: 1200, releaseNumber: "REL-20260819-1200"}, fixedClock{now: now})
	created, err := service.CreateRelease(ctx, application.CreateReleaseCommand{
		IdempotencyKey: "percent-create", ModelCode: "payment-route-admin", ReleaseTypeCode: "percentage",
		Scope: release.Scope{Region: "cn", Environment: "production", Stage: "blue"}, Actor: "operator@example.com",
		Items: []application.ReleaseDraft{{Action: release.ChangeAdd, After: map[string]string{"route_code": "visa", "priority": "9"}, ExpectedCollectionRevision: 7}},
	})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	executed, err := service.Act(ctx, application.ActCommand{
		OrderID: created.ID, ActionRequestID: "50000000-0000-4000-8000-000000000001",
		ExpectedRevision: 1, ExpectedCurrentStep: "percent-10", Action: application.ActionExecute, Actor: "operator@example.com",
	})
	if err != nil {
		t.Fatalf("execute percentage: %v", err)
	}
	assertCount(t, raw, `SELECT COUNT(*) FROM configuration_overlays WHERE release_order_id = '`+created.ID+`' AND stage = 'blue' AND JSON_EXTRACT(rollout_ranges, '$[0].start') = 0 AND JSON_EXTRACT(rollout_ranges, '$[0].end') = 9`, 1)
	assertCount(t, raw, `SELECT COUNT(*) FROM configuration_versions WHERE collection_name = 'payment_routes' AND environment = 'production' AND config_revision = 8`, 1)
	assertCount(t, raw, `SELECT COUNT(*) FROM release_step_states WHERE release_order_id = '`+created.ID+`' AND JSON_EXTRACT(effect, '$.percent.appliedRevision') = 8`, 1)
	assertCount(t, raw, `SELECT COUNT(*) FROM audit_records WHERE resource_id = '`+created.ID+`' AND action = 'PERCENT_ROLLOUT'`, 1)
	distributionSource, err := mysqlsource.New(database)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := snapshot.NewManager(distributionSource, snapshot.IdentitySeed{ServerEpoch: "percent-epoch", ServerInstanceID: "percent-server", SnapshotInstance: "percent-snapshot"}, fixedClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(ctx, "production"); err != nil {
		t.Fatal(err)
	}
	configService := configserver.New(manager, distributionSource)
	selectedClient, err := finconfig.New(finconfig.Config{
		ConsumerID: "payment-service", ClientID: "pod-10", Region: "cn", Environment: "production", Stage: "blue",
		Transport: configTransport{service: configService},
	})
	if err != nil {
		t.Fatal(err)
	}
	unselectedClient, err := finconfig.New(finconfig.Config{
		ConsumerID: "payment-service", ClientID: "pod-8", Region: "cn", Environment: "production", Stage: "blue",
		Transport: configTransport{service: configService},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := selectedClient.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err := unselectedClient.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	definition, _ := manager.Current().Definition("payment_routes")
	rolloutRecord, _ := definition.NewRecord("production", map[string]string{"route_code": "visa", "priority": "9"})
	if record, ok := selectedClient.GetByKey("payment_routes", rolloutRecord.RecordKey); !ok || record.Values["priority"] != "9" {
		t.Fatalf("selected SDK record = %+v, %t", record, ok)
	}
	if _, ok := unselectedClient.GetByKey("payment_routes", rolloutRecord.RecordKey); ok {
		t.Fatal("unselected SDK observed percentage ADD")
	}
	advanced, err := service.Act(ctx, application.ActCommand{
		OrderID: created.ID, ActionRequestID: "50000000-0000-4000-8000-000000000002",
		ExpectedRevision: executed.Revision, ExpectedCurrentStep: "percent-10", Action: application.ActionAdvance, Actor: "operator@example.com",
	})
	if err != nil || advanced.CurrentStep != release.StepCompare || advanced.CurrentStepCode != "compare" {
		t.Fatalf("reload and advance percentage order: view=%+v error=%v", advanced, err)
	}
	compared, err := service.Act(ctx, application.ActCommand{
		OrderID: created.ID, ActionRequestID: "50000000-0000-4000-8000-000000000003",
		ExpectedRevision: advanced.Revision, ExpectedCurrentStep: "compare", Action: application.ActionExecute, Actor: "operator@example.com",
	})
	if err != nil {
		t.Fatalf("compare percentage: %v", err)
	}
	assertCount(t, raw, `SELECT COUNT(*) FROM release_step_states WHERE release_order_id = '`+created.ID+`' AND step_code = 'compare' AND status = 'EXECUTED' AND JSON_LENGTH(JSON_EXTRACT(compare_result, '$.diffKeys')) = 0 AND JSON_UNQUOTE(JSON_EXTRACT(compare_result, '$.expectedDigest.value')) = JSON_UNQUOTE(JSON_EXTRACT(compare_result, '$.actualDigest.value'))`, 1)
	advancedToPromote, err := service.Act(ctx, application.ActCommand{
		OrderID: created.ID, ActionRequestID: "50000000-0000-4000-8000-000000000004",
		ExpectedRevision: compared.Revision, ExpectedCurrentStep: "compare", Action: application.ActionAdvance, Actor: "operator@example.com",
	})
	if err != nil || advancedToPromote.CurrentStep != release.StepBaseApply || advancedToPromote.CurrentStepCode != "promote" {
		t.Fatalf("advance compare: view=%+v error=%v", advancedToPromote, err)
	}
	promoted, err := service.Act(ctx, application.ActCommand{
		OrderID: created.ID, ActionRequestID: "50000000-0000-4000-8000-000000000005",
		ExpectedRevision: advancedToPromote.Revision, ExpectedCurrentStep: "promote", Action: application.ActionExecute, Actor: "operator@example.com",
	})
	if err != nil {
		t.Fatalf("promote percentage: %v", err)
	}
	if !promoted.CanRollback {
		t.Fatalf("promoted order cannot roll back: %+v", promoted)
	}
	assertCount(t, raw, `SELECT COUNT(*) FROM configuration_records WHERE collection_name = 'payment_routes' AND environment = 'production' AND JSON_UNQUOTE(JSON_EXTRACT(data, '$.priority')) = '9'`, 1)
	assertCount(t, raw, `SELECT COUNT(*) FROM configuration_overlays WHERE release_order_id = '`+created.ID+`'`, 0)
	assertCount(t, raw, `SELECT COUNT(*) FROM configuration_versions WHERE collection_name = 'payment_routes' AND environment = 'production' AND config_revision = 9 AND overlay_digest = '4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945'`, 1)
	assertCount(t, raw, `SELECT COUNT(*) FROM release_step_states WHERE release_order_id = '`+created.ID+`' AND step_code = 'promote' AND JSON_EXTRACT(effect, '$.base.appliedRevision') = 9`, 1)
	if _, err := manager.Refresh(ctx, "production"); err != nil {
		t.Fatal(err)
	}
	if err := unselectedClient.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if record, ok := unselectedClient.GetByKey("payment_routes", rolloutRecord.RecordKey); !ok || record.Values["priority"] != "9" {
		t.Fatalf("promoted SDK record = %+v, %t", record, ok)
	}

	rolledBack, err := service.Act(ctx, application.ActCommand{
		OrderID: created.ID, ActionRequestID: "50000000-0000-4000-8000-000000000006",
		ExpectedRevision: promoted.Revision, ExpectedCurrentStep: "promote", Action: application.ActionRollback, Actor: "operator@example.com",
	})
	if err != nil {
		t.Fatalf("rollback promotion: %v", err)
	}
	if rolledBack.Status != release.OrderRolledBack {
		t.Fatalf("rolled back promotion view = %+v", rolledBack)
	}
	assertCount(t, raw, `SELECT COUNT(*) FROM configuration_records WHERE collection_name = 'payment_routes' AND environment = 'production'`, 0)
	assertCount(t, raw, `SELECT COUNT(*) FROM configuration_overlays WHERE release_order_id = '`+created.ID+`' AND JSON_EXTRACT(rollout_ranges, '$[0].start') = 0 AND JSON_EXTRACT(rollout_ranges, '$[0].end') = 9`, 1)
	assertCount(t, raw, `SELECT COUNT(*) FROM configuration_versions WHERE collection_name = 'payment_routes' AND environment = 'production' AND config_revision = 10`, 1)
	assertCount(t, raw, `SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = '`+created.ID+`'`, 3)
	if _, err := manager.Refresh(ctx, "production"); err != nil {
		t.Fatal(err)
	}
	if err := unselectedClient.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := unselectedClient.GetByKey("payment_routes", rolloutRecord.RecordKey); ok {
		t.Fatal("promotion rollback did not restore unselected SDK view")
	}
	if err := store.WithinTransaction(ctx, func(transaction application.Transaction) error {
		return transaction.RecordAction(ctx, application.ActionRecord{
			OrderID: created.ID, StepCode: "compare", Action: application.ActionExecute,
			Actor: "operator@example.com", Scope: release.Scope{Region: "cn", Environment: "production", Stage: "blue"}, At: now,
			Failure: &application.ActionFailure{Code: "COMPARE_MISMATCH", Message: "comparison differs for 1 record(s)"},
		})
	}); err != nil {
		t.Fatalf("persist failed compare diagnostics: %v", err)
	}
	assertCount(t, raw, `SELECT COUNT(*) FROM release_operation_logs WHERE release_order_id = '`+created.ID+`' AND step_code = 'compare' AND result = 'FAILED' AND error_code = 'COMPARE_MISMATCH'`, 1)
	assertCount(t, raw, `SELECT COUNT(*) FROM audit_records WHERE resource_id = '`+created.ID+`' AND action = 'EXECUTE' AND result = 'FAILED' AND JSON_UNQUOTE(JSON_EXTRACT(metadata, '$.errorCode')) = 'COMPARE_MISMATCH'`, 1)
}

func TestRealMySQLHTTPWalkingSkeleton(t *testing.T) {
	dsn := isolatedDatabase(t)
	ctx := context.Background()
	database, err := platformmysql.Open(ctx, platformmysql.Config{DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 2, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store, err := mysqlstore.New(database)
	if err != nil {
		t.Fatal(err)
	}
	clock := fixedClock{now: time.Date(2026, 8, 19, 7, 0, 0, 0, time.UTC)}
	service := application.NewService(store, &ids{values: []string{"018fb4a7-a189-7216-8df4-c9f0aad7166e", "018fb4a7-a8c8-7a3f-b370-ce86eeaf5c9d"}}, clock)
	distributionSource, err := mysqlsource.New(database)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := snapshot.NewManager(distributionSource, snapshot.IdentitySeed{ServerEpoch: "epoch-http", ServerInstanceID: "server-http", SnapshotInstance: "snapshot-http"}, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(ctx, "production"); err != nil {
		t.Fatal(err)
	}
	handler, err := adminbff.New(pagequery.New(manager), service, httpActor{})
	if err != nil {
		t.Fatal(err)
	}

	initial := postJSON(t, handler, "/api/v1/query-page", "", map[string]any{"modelCode": "payment-route-admin", "scope": map[string]string{"region": "cn", "environment": "production"}, "queryType": "ALL"})
	if initial.Code != http.StatusOK || !bytes.Contains(initial.Body.Bytes(), []byte(`"rows":[]`)) ||
		!bytes.Contains(initial.Body.Bytes(), []byte(`{"available":true,"code":"direct","name":"Direct","templateCode":"base-final"}`)) ||
		!bytes.Contains(initial.Body.Bytes(), []byte(`{"available":false,"code":"approval","name":"Approval","templateCode":"approval-final","unavailableReasonCode":"ACTIVE_TEMPLATE_NOT_FOUND"}`)) {
		t.Fatalf("initial query = %d %s", initial.Code, initial.Body.String())
	}
	created := postJSON(t, handler, "/api/v1/releases", "018fb4a7-afd0-7d19-8177-790193deaf14", map[string]any{
		"modelCode": "payment-route-admin", "releaseTypeCode": "direct", "description": "Add visa route",
		"scope": map[string]string{"region": "cn", "environment": "production"},
		"items": []any{map[string]any{"action": "ADD", "after": map[string]string{"route_code": "visa-cn", "priority": "+0007"}, "expectedRecordRevision": 0, "expectedCollectionRevision": 7}},
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	var detail struct {
		Order struct {
			ID             string `json:"id"`
			CurrentStep    string `json:"currentStep"`
			EntityRevision uint64 `json:"entityRevision"`
			Status         string `json:"status"`
		} `json:"order"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	actions := []struct {
		id, action, step string
		revision         uint64
	}{
		{"018fb4a7-b751-7690-a20b-6266d78a024a", "EXECUTE", "base-apply", 1},
		{"018fb4a7-be8a-706d-b953-967f6f92c14a", "ADVANCE", "base-apply", 2},
		{"018fb4a7-c5d0-749e-b977-ac6be1f2bd65", "EXECUTE", "complete", 3},
	}
	for _, action := range actions {
		response := postJSON(t, handler, "/api/v1/releases/"+detail.Order.ID+"/actions", action.id, map[string]any{"action": action.action, "expectedOrderRevision": action.revision, "expectedCurrentStep": action.step})
		if response.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", action.action, response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
	}
	if detail.Order.Status != "SUCCEEDED" {
		t.Fatalf("completed detail = %+v", detail)
	}
	if _, err := manager.Refresh(ctx, "production"); err != nil {
		t.Fatal(err)
	}
	queried := postJSON(t, handler, "/api/v1/query-page", "", map[string]any{"modelCode": "payment-route-admin", "scope": map[string]string{"region": "cn", "environment": "production"}, "queryType": "ALL"})
	if queried.Code != http.StatusOK || !bytes.Contains(queried.Body.Bytes(), []byte(`"priority":"7"`)) {
		t.Fatalf("published query = %d %s", queried.Code, queried.Body.String())
	}
	configService := configserver.New(manager, distributionSource)
	sdkClient, err := finconfig.New(finconfig.Config{ConsumerID: "payment-service", ClientID: "pod-http", Region: "cn", Environment: "production", Transport: configTransport{service: configService}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sdkClient.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	definition, _ := manager.Current().Definition("payment_routes")
	record, _ := definition.NewRecord("production", map[string]string{"route_code": "visa-cn", "priority": "7"})
	if sdkRecord, ok := sdkClient.GetByKey("payment_routes", record.RecordKey); !ok || sdkRecord.Values["priority"] != "7" {
		t.Fatalf("SDK result = %+v, %t", sdkRecord, ok)
	}
}

func TestRealMySQLBaseFinalTransaction(t *testing.T) {
	dsn := isolatedDatabase(t)
	ctx := context.Background()
	database, err := platformmysql.Open(ctx, platformmysql.Config{
		DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 2,
		ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store, err := mysqlstore.New(database)
	if err != nil {
		t.Fatal(err)
	}
	clock := fixedClock{now: time.Date(2026, 8, 19, 7, 0, 0, 0, time.UTC)}
	service := application.NewService(store, &ids{values: []string{
		"018fb4a7-6c54-7d34-bc21-357b4e943c30",
		"018fb4a7-74b6-7a5f-a4d0-11c74002dadd",
	}}, clock)
	created, err := service.CreateBaseFinal(ctx, application.CreateBaseFinalCommand{
		IdempotencyKey: "create-request-1", ModelCode: "payment-route-admin",
		Scope: release.Scope{Region: "cn", Environment: "production"}, Actor: "operator@example.com",
		Items: []application.AddDraft{{Data: map[string]string{"route_code": "visa-cn", "priority": "+0007"}, ExpectedCollectionRevision: 7}},
	})
	if err != nil {
		t.Fatalf("CreateBaseFinal: %v", err)
	}
	if _, err := service.Act(ctx, application.ActCommand{OrderID: created.ID, ActionRequestID: "018fb4a7-7c43-7de2-bad4-5ea3fc262630", ExpectedRevision: 1, ExpectedCurrentStep: "base-apply", Action: application.ActionExecute, Actor: "operator@example.com"}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	advanced, err := service.Act(ctx, application.ActCommand{OrderID: created.ID, ActionRequestID: "018fb4a7-83c8-73aa-924d-9b57558d3200", ExpectedRevision: 2, ExpectedCurrentStep: "base-apply", Action: application.ActionAdvance, Actor: "operator@example.com"})
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	completed, err := service.Act(ctx, application.ActCommand{OrderID: created.ID, ActionRequestID: "018fb4a7-8a7e-786b-a60d-8d285f483a1a", ExpectedRevision: advanced.Revision, ExpectedCurrentStep: "complete", Action: application.ActionExecute, Actor: "operator@example.com"})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if completed.Status != release.OrderSucceeded {
		t.Fatalf("completed = %+v", completed)
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertCount(t, db, `SELECT COUNT(*) FROM configuration_records WHERE environment = 'production'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM configuration_records WHERE environment = 'staging'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM configuration_change_log`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM outbox_events WHERE status = 'PENDING'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM audit_records`, 5)
	assertCount(t, db, `SELECT COUNT(*) FROM release_operation_logs`, 3)
	var productionRevision, stagingRevision uint64
	if err := db.QueryRow(`SELECT config_revision FROM configuration_versions WHERE collection_name = 'payment_routes' AND environment = 'production'`).Scan(&productionRevision); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT config_revision FROM configuration_versions WHERE collection_name = 'payment_routes' AND environment = 'staging'`).Scan(&stagingRevision); err != nil {
		t.Fatal(err)
	}
	if productionRevision != 8 || stagingRevision != 7 {
		t.Fatalf("environment revisions = production %d, staging %d", productionRevision, stagingRevision)
	}

	distributionSource, err := mysqlsource.New(database)
	if err != nil {
		t.Fatal(err)
	}
	productionSnapshots, err := snapshot.NewManager(distributionSource, snapshot.IdentitySeed{ServerEpoch: "epoch-1", ServerInstanceID: "server-1", SnapshotInstance: "snapshot-1"}, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := productionSnapshots.Refresh(ctx, "production"); err != nil {
		t.Fatalf("refresh production snapshot: %v", err)
	}
	page, err := pagequery.New(productionSnapshots).Query(pagequery.Request{ModelCode: "payment-route-admin", Region: "cn", Environment: "production", Type: pagequery.TypeAll})
	if err != nil {
		t.Fatalf("query production page: %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].Values["priority"] != "7" || page.CollectionRevision != 8 {
		t.Fatalf("production page = %+v", page)
	}
	configService := configserver.New(productionSnapshots, distributionSource)
	sdkClient, err := finconfig.New(finconfig.Config{
		ConsumerID: "payment-service", ClientID: "pod-1", Region: "cn", Environment: "production",
		Transport: configTransport{service: configService},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sdkClient.Refresh(ctx); err != nil {
		t.Fatalf("SDK refresh: %v", err)
	}
	if sdkRecord, ok := sdkClient.GetByKey("payment_routes", page.Rows[0].RecordKey); !ok || sdkRecord.Values["priority"] != "7" {
		t.Fatalf("SDK record = %+v, %t", sdkRecord, ok)
	}

	stagingSnapshots, err := snapshot.NewManager(distributionSource, snapshot.IdentitySeed{ServerEpoch: "epoch-1", ServerInstanceID: "server-2", SnapshotInstance: "snapshot-2"}, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stagingSnapshots.Refresh(ctx, "staging"); err != nil {
		t.Fatalf("refresh staging snapshot: %v", err)
	}
	stagingPage, err := pagequery.New(stagingSnapshots).Query(pagequery.Request{ModelCode: "payment-route-admin", Region: "cn", Environment: "staging", Type: pagequery.TypeOnlyData})
	if err != nil {
		t.Fatalf("query staging page: %v", err)
	}
	if len(stagingPage.Rows) != 0 || stagingPage.CollectionRevision != 7 {
		t.Fatalf("staging page = %+v", stagingPage)
	}
}

func isolatedDatabase(t *testing.T) string {
	t.Helper()
	base := os.Getenv("FINCONFIG_TEST_MYSQL_DSN")
	if base == "" {
		t.Skip("FINCONFIG_TEST_MYSQL_DSN is not set")
	}
	config, err := drivermysql.ParseDSN(base)
	if err != nil {
		t.Fatal(err)
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	databaseName := "finconfig_release_" + hex.EncodeToString(random)
	adminConfig := config.Clone()
	adminConfig.DBName = ""
	admin, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE `" + databaseName + "`") })
	config.DBName = databaseName
	dsn := config.FormatDSN()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrations.Up(context.Background(), db, migrationDirectory(t)); err != nil {
		t.Fatal(err)
	}
	seedCatalog(t, db)
	return dsn
}

func seedCatalog(t *testing.T, db *sql.DB) {
	t.Helper()
	defaultEnabled := "false"
	fields := []catalog.FieldDefinition{
		{Name: "route_code", DisplayName: "Route code", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 0},
		{Name: "priority", DisplayName: "Priority", Type: catalog.FieldTypeInt64, Required: true, DisplayOrder: 1},
		{Name: "enabled", DisplayName: "Enabled", Type: catalog.FieldTypeBool, Required: true, DefaultValue: &defaultEnabled, DisplayOrder: 2},
	}
	model := catalog.ModelSpec{
		Fields: []catalog.ModelField{
			{Name: "route_code", Type: catalog.FieldTypeString, Required: true, Editable: true, Queryable: true, UIControl: catalog.UIControlInput, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}},
			{Name: "priority", Type: catalog.FieldTypeInt64, Required: true, Editable: true, Queryable: true, UIControl: catalog.UIControlNumber, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}},
			{Name: "enabled", Type: catalog.FieldTypeBool, Required: true, Editable: true, Queryable: true, DefaultValue: &defaultEnabled, UIControl: catalog.UIControlBoolean, AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}},
		},
		ProjectionFields: []string{"route_code", "priority", "enabled"}, KeyFields: []string{"route_code"}, DefaultPageSize: 20, MaxPageSize: 100,
		ReleaseTypes: []catalog.ReleaseTypeDefinition{
			{Code: "direct", Name: "Direct", TemplateCode: "base-final", Enabled: true},
			{Code: "approval", Name: "Approval", TemplateCode: "approval-final", Enabled: true},
		},
	}
	fieldsJSON, _ := json.Marshal(fields)
	keysJSON, _ := json.Marshal([]string{"route_code"})
	modelJSON, _ := json.Marshal(model)
	now := time.Date(2026, 8, 19, 6, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`UPDATE configuration_revision_counters SET current_revision = 7, updated_at = ? WHERE counter_name = 'global'`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO configuration_collections (name, description, fields, key_fields, sdk_delivery_enabled, schema_version, status, config_revision, created_at, created_by, updated_at, updated_by) VALUES ('payment_routes', 'Routes', ?, ?, TRUE, 1, 'ENABLED', 1, ?, 'seed', ?, 'seed')`, fieldsJSON, keysJSON, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO configuration_models (code, name, collection_name, definition, enabled, config_revision, created_at, created_by, updated_at, updated_by) VALUES ('payment-route-admin', 'Payment routes', 'payment_routes', ?, TRUE, 2, ?, 'seed', ?, 'seed')`, modelJSON, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO release_templates (code, version, model_code, release_type_code, active_slot, final_effect, template, created_at, created_by) VALUES ('base-final', 1, 'payment-route-admin', 'direct', 'A', 'BASE_FINAL', JSON_OBJECT('steps', JSON_ARRAY(JSON_OBJECT('code', 'base-apply', 'type', 'BASE_APPLY', 'params', JSON_OBJECT('cleanupScopeOverlay', TRUE)), JSON_OBJECT('code', 'complete', 'type', 'COMPLETE', 'params', JSON_OBJECT()))), ?, 'seed')`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO configuration_subscriptions (id, consumer_id, collection_name, index_name, index_fields, cardinality, enabled, config_revision, created_at, created_by, updated_at, updated_by) VALUES ('018fb4a7-91a7-70d7-8cd2-18820702cd67', 'payment-service', 'payment_routes', 'by_route_code', JSON_ARRAY('route_code'), 'ONE_TO_ONE', TRUE, 3, ?, 'seed', ?, 'seed')`, now, now); err != nil {
		t.Fatal(err)
	}
	emptyDigest := "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945"
	for _, environment := range []string{"production", "staging"} {
		if _, err := db.Exec(`INSERT INTO configuration_versions (collection_name, environment, config_revision, base_digest, overlay_digest, release_order_id, updated_at) VALUES ('payment_routes', ?, 7, ?, ?, NULL, ?)`, environment, emptyDigest, emptyDigest, now); err != nil {
			t.Fatal(err)
		}
	}
}

func migrationDirectory(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate migration test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../db/migrations/mysql"))
}

func assertCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("count for %q = %d, want %d", query, got, want)
	}
}

type createResult struct {
	view application.OrderView
	err  error
}

func concurrentCreates(ctx context.Context, firstService *application.Service, firstCommand application.CreateBaseFinalCommand, secondService *application.Service, secondCommand application.CreateBaseFinalCommand) []createResult {
	start := make(chan struct{})
	results := make(chan createResult, 2)
	var wait sync.WaitGroup
	for _, input := range []struct {
		service *application.Service
		command application.CreateBaseFinalCommand
	}{{firstService, firstCommand}, {secondService, secondCommand}} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			view, err := input.service.CreateBaseFinal(ctx, input.command)
			results <- createResult{view: view, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	collected := make([]createResult, 0, 2)
	for result := range results {
		collected = append(collected, result)
	}
	return collected
}

func baseCreate(idempotencyKey, actor, environment, route string, collectionRevision catalog.ConfigRevision) application.CreateBaseFinalCommand {
	return application.CreateBaseFinalCommand{
		IdempotencyKey: idempotencyKey, ModelCode: "payment-route-admin", Scope: release.Scope{Region: "cn", Environment: environment}, Actor: actor,
		Items: []application.AddDraft{{Data: map[string]string{"route_code": route, "priority": "1"}, ExpectedCollectionRevision: collectionRevision}},
	}
}

func withRequestIdentity(command application.CreateBaseFinalCommand, idempotencyKey, actor string) application.CreateBaseFinalCommand {
	command.IdempotencyKey = idempotencyKey
	command.Actor = actor
	return command
}

func assertDatabaseCount(t *testing.T, dsn, query string, want int) {
	t.Helper()
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	assertCount(t, database, query, want)
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type ids struct {
	values []string
	next   int
}

func (ids *ids) NewID() string {
	value := ids.values[ids.next]
	ids.next++
	return value
}

func (*ids) NewReleaseNumber(time.Time) string { return "REL-20260819-0001" }

type numberedIDs struct {
	next          uint64
	releaseNumber string
}

func (ids *numberedIDs) NewID() string {
	ids.next++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", ids.next)
}

func (ids *numberedIDs) NewReleaseNumber(time.Time) string { return ids.releaseNumber }

type httpActor struct{}

func (httpActor) Authenticate(*http.Request) (adminbff.Principal, error) {
	return adminbff.Principal{Subject: "operator@example.com"}, nil
}

func postJSON(t *testing.T, handler http.Handler, path, idempotency string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	if idempotency != "" {
		request.Header.Set("Idempotency-Key", idempotency)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

type configTransport struct{ service *configserver.Service }

func (transport configTransport) GetSnapshot(ctx context.Context, request finconfig.SnapshotRequest) (finconfig.SnapshotResponse, error) {
	known := make([]configserver.Version, len(request.KnownVersions))
	for index, version := range request.KnownVersions {
		known[index] = configserver.Version{Collection: version.Collection, Revision: version.Revision, Digest: version.Digest}
	}
	response, err := transport.service.GetSnapshot(ctx, configserver.GetSnapshotRequest{
		ConsumerID: request.ConsumerID, ClientID: request.ClientID, Region: request.Region,
		Environment: request.Environment, Stage: request.Stage, KnownVersions: known,
	})
	if err != nil {
		return finconfig.SnapshotResponse{}, err
	}
	converted := finconfig.SnapshotResponse{
		Identity: finconfig.SnapshotIdentity{
			ServerEpoch: response.Identity.ServerEpoch, ServerInstanceID: response.Identity.ServerInstanceID,
			SnapshotInstance: response.Identity.SnapshotInstance, Generation: response.Identity.Generation,
		},
		Environment: response.Environment, DeletedCollections: response.DeletedCollections,
		Collections: make([]finconfig.CollectionPayload, len(response.Collections)),
	}
	for index, collection := range response.Collections {
		converted.Collections[index] = finconfig.CollectionPayload{Name: collection.Name, Revision: collection.Revision, Digest: collection.Digest, Records: make([]finconfig.Record, len(collection.Records))}
		for recordIndex, record := range collection.Records {
			converted.Collections[index].Records[recordIndex] = finconfig.Record{Key: record.RecordKey, Revision: record.RecordRevision, Values: record.Data}
		}
	}
	return converted, nil
}

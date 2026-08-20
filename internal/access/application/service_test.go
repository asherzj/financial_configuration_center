package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	access "github.com/asherzj/financial_configuration_center/internal/access/application"
	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
)

func TestRevealReturnsPlaintextOnlyAfterAuthorityAndAuditSucceed(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	result, err := fixture.service.Reveal(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if result.Value != "authority-secret" || !result.ExpiresAt.Equal(fixture.now.Add(time.Minute)) || len(fixture.store.audits) != 1 {
		t.Fatalf("result=%+v audits=%+v", result, fixture.store.audits)
	}
	if fixture.snapshot.query.ModelCode != fixture.command.ModelCode || fixture.snapshot.query.Scope != fixture.command.Scope {
		t.Fatalf("snapshot query = %+v", fixture.snapshot.query)
	}
	encoded, _ := json.Marshal(fixture.store.audits[0].Metadata)
	if string(encoded) == "" || strings.Contains(string(encoded), "authority-secret") {
		t.Fatalf("audit leaked plaintext: %s", encoded)
	}
}

func TestRevealFailsClosedWhenSnapshotAuthorityIsUnavailable(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.snapshot.err = access.ErrSnapshotUnavailable
	result, err := fixture.service.Reveal(context.Background(), fixture.command)
	if !errors.Is(err, access.ErrFailedPrecondition) || result.Value != "" || fixture.store.transactions != 0 {
		t.Fatalf("result=%+v error=%v transactions=%d", result, err, fixture.store.transactions)
	}
}

func TestRevealRejectsTypedNilSnapshotAuthorityReader(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	var reader *snapshotAuthorityReader
	service := access.NewService(fixture.store, reader, fixedClock{fixture.now})
	result, err := service.Reveal(context.Background(), fixture.command)
	if err == nil || result.Value != "" || fixture.store.transactions != 0 {
		t.Fatalf("result=%+v error=%v transactions=%d", result, err, fixture.store.transactions)
	}
}

func TestRevealRejectsForbiddenAndEveryStaleAuthorityFact(t *testing.T) {
	t.Parallel()
	t.Run("scope", func(t *testing.T) {
		fixture := newFixture(t)
		fixture.command.Principal.AllowedScopes[0].Environment = "staging"
		if result, err := fixture.service.Reveal(context.Background(), fixture.command); !errors.Is(err, access.ErrForbidden) || result.Value != "" || fixture.snapshot.calls != 0 || fixture.store.transactions != 0 {
			t.Fatalf("result=%+v err=%v snapshot=%d transactions=%d", result, err, fixture.snapshot.calls, fixture.store.transactions)
		}
	})
	t.Run("role", func(t *testing.T) {
		fixture := newFixture(t)
		fixture.command.Principal.Roles = nil
		if result, err := fixture.service.Reveal(context.Background(), fixture.command); !errors.Is(err, access.ErrForbidden) || result.Value != "" || fixture.store.transactions != 0 {
			t.Fatalf("result=%+v err=%v transactions=%d", result, err, fixture.store.transactions)
		}
	})
	for _, test := range []struct {
		name             string
		mutate           func(*fixture)
		wantTransactions int
	}{
		{name: "snapshot not found", mutate: func(f *fixture) { f.snapshot.authority.Found = false }},
		{name: "environment", mutate: func(f *fixture) { f.snapshot.authority.Environment = "staging" }},
		{name: "server epoch", mutate: func(f *fixture) { f.command.ExpectedServerEpoch = "old" }},
		{name: "snapshot instance", mutate: func(f *fixture) { f.snapshot.authority.SnapshotInstance = "other" }},
		{name: "snapshot generation", mutate: func(f *fixture) { f.command.ExpectedSnapshotGeneration++ }},
		{name: "model revision", mutate: func(f *fixture) { f.command.ExpectedModelRevision-- }},
		{name: "collection revision", mutate: func(f *fixture) { f.command.ExpectedCollectionRevision-- }},
		{name: "record revision", mutate: func(f *fixture) { f.store.record.BaseRecords[0].ConfigRevision++ }, wantTransactions: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			test.mutate(fixture)
			result, err := fixture.service.Reveal(context.Background(), fixture.command)
			if !errors.Is(err, access.ErrAborted) || result.Value != "" || len(fixture.store.audits) != 0 || fixture.store.transactions != test.wantTransactions {
				t.Fatalf("result=%+v error=%v audits=%d transactions=%d", result, err, len(fixture.store.audits), fixture.store.transactions)
			}
		})
	}
}

func TestRevealAuditFailureReturnsNoPlaintext(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.store.auditErr = errors.New("audit unavailable")
	result, err := fixture.service.Reveal(context.Background(), fixture.command)
	if err == nil || result.Value != "" || len(fixture.store.audits) != 0 {
		t.Fatalf("result=%+v error=%v audits=%d", result, err, len(fixture.store.audits))
	}
}

type fixture struct {
	now      time.Time
	store    *fakeStore
	snapshot *snapshotAuthorityReader
	service  *access.Service
	command  access.RevealCommand
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	definition, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: "credentials", KeyFields: []string{"name"}, SchemaVersion: 1,
		Fields: []catalog.FieldDefinition{
			{Name: "name", DisplayName: "Name", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "secret", DisplayName: "Secret", Type: catalog.FieldTypeString, Required: true, Sensitive: true, DisplayOrder: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	model, err := catalog.CompileModel(definition, catalog.ModelSpec{
		Code: "credential-admin", Name: "Credentials", Collection: definition.Name(),
		Fields: []catalog.ModelField{
			{Name: "name", Type: catalog.FieldTypeString, Required: true, Editable: true, UIControl: catalog.UIControlInput},
			{Name: "secret", Type: catalog.FieldTypeString, Required: true, Sensitive: true, Editable: true, UIControl: catalog.UIControlInput},
		},
		ProjectionFields: []string{"name", "secret"}, KeyFields: []string{"name"}, DefaultPageSize: 20, MaxPageSize: 100, ConfigRevision: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	record, _ := definition.NewRecord("production", map[string]string{"name": "primary", "secret": "authority-secret"})
	record.ConfigRevision = 8
	now := time.Date(2026, 8, 19, 23, 30, 0, 0, time.UTC)
	store := &fakeStore{
		catalog: access.CatalogAuthority{Definition: definition, Model: model},
		record:  access.RecordAuthority{CollectionRevision: 8, BaseRecords: []catalog.ConfigurationRecord{record}},
	}
	snapshot := &snapshotAuthorityReader{authority: access.SnapshotAuthority{
		Found: true, Environment: "production", ServerEpoch: "epoch", SnapshotInstance: "instance",
		SnapshotGeneration: 1, ModelRevision: 7, CollectionRevision: 8,
	}}
	return &fixture{
		now: now, store: store, snapshot: snapshot, service: access.NewService(store, snapshot, fixedClock{now}),
		command: access.RevealCommand{
			ModelCode: model.Code(), Scope: access.Scope{Region: "cn", Environment: "production"}, RecordKey: record.RecordKey, FieldName: "secret",
			ExpectedRecordRevision: 8, ExpectedCollectionRevision: 8, ExpectedModelRevision: 7,
			ExpectedServerEpoch: "epoch", ExpectedSnapshotInstance: "instance", ExpectedSnapshotGeneration: 1,
			Reason: "incident diagnosis", RequestID: "request-1", Principal: access.Principal{
				Subject: "viewer", DisplayName: "Viewer", Roles: []string{access.SensitiveViewerRole},
				AllowedScopes: []platformauth.ScopePattern{{Region: "cn", Environment: "production", Stage: "*"}},
			},
		},
	}
}

type fakeStore struct {
	catalog      access.CatalogAuthority
	record       access.RecordAuthority
	audits       []access.AuditEntry
	auditErr     error
	transactions int
}

func (store *fakeStore) WithinTransaction(ctx context.Context, work func(access.Transaction) error) error {
	store.transactions++
	return work((*fakeTransaction)(store))
}

type fakeTransaction fakeStore

func (transaction *fakeTransaction) LoadCatalog(context.Context, string) (access.CatalogAuthority, error) {
	return transaction.catalog, nil
}

func (transaction *fakeTransaction) LoadRecordAuthority(context.Context, string, access.Scope, string) (access.RecordAuthority, error) {
	result := transaction.record
	result.BaseRecords = append([]catalog.ConfigurationRecord(nil), result.BaseRecords...)
	result.Rules = append([]overlay.Rule(nil), result.Rules...)
	return result, nil
}

func (transaction *fakeTransaction) InsertRevealAudit(_ context.Context, entry access.AuditEntry) error {
	if transaction.auditErr != nil {
		return transaction.auditErr
	}
	entry.Metadata = cloneAnyMap(entry.Metadata)
	transaction.audits = append(transaction.audits, entry)
	return nil
}

type snapshotAuthorityReader struct {
	authority access.SnapshotAuthority
	err       error
	query     access.SnapshotAuthorityQuery
	calls     int
}

func (reader *snapshotAuthorityReader) ReadSnapshotAuthority(_ context.Context, query access.SnapshotAuthorityQuery) (access.SnapshotAuthority, error) {
	reader.calls++
	reader.query = query
	return reader.authority, reader.err
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func cloneAnyMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

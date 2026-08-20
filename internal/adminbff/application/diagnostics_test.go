package application_test

import (
	"context"
	"errors"
	"testing"

	bffapp "github.com/asherzj/financial_configuration_center/internal/adminbff/application"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
)

func TestDiagnosticsServiceAuthorizesRoleAndManagedEnvironmentBeforeReading(t *testing.T) {
	t.Parallel()

	production := scopePattern(t, "*", "production", "*")
	staging := scopePattern(t, "*", "staging", "*")
	tests := map[string]bffapp.DiagnosticPrincipal{
		"missing subject": {Roles: []string{"PLATFORM_OPERATOR"}, AllowedScopes: []platformauth.ScopePattern{production}},
		"wrong role":      {Subject: "viewer", Roles: []string{"CONFIG_VIEWER"}, AllowedScopes: []platformauth.ScopePattern{production}},
		"wrong scope":     {Subject: "operator", Roles: []string{"PLATFORM_OPERATOR"}, AllowedScopes: []platformauth.ScopePattern{staging}},
		"missing scope":   {Subject: "auditor", Roles: []string{"AUDITOR"}},
	}
	for name, principal := range tests {
		name, principal := name, principal
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			reader := &diagnosticsReaderStub{}
			service, err := bffapp.NewDiagnosticsService(reader, "production")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.SnapshotDiagnostics(context.Background(), principal); !errors.Is(err, bffapp.ErrDiagnosticsForbidden) {
				t.Fatalf("snapshot error = %v", err)
			}
			if _, err := service.CollectionDiagnostics(context.Background(), principal, "routes"); !errors.Is(err, bffapp.ErrDiagnosticsForbidden) {
				t.Fatalf("collection error = %v", err)
			}
			if reader.snapshotCalls != 0 || reader.collectionCalls != 0 {
				t.Fatalf("reader calls snapshot=%d collection=%d", reader.snapshotCalls, reader.collectionCalls)
			}
		})
	}
}

func TestDiagnosticsServiceAllowsOperatorOrAuditorAndForwardsContext(t *testing.T) {
	t.Parallel()

	for _, role := range []string{"PLATFORM_OPERATOR", "AUDITOR"} {
		role := role
		t.Run(role, func(t *testing.T) {
			t.Parallel()
			reader := &diagnosticsReaderStub{
				snapshot:   bffapp.SnapshotDiagnostics{Environment: "production"},
				collection: bffapp.CollectionDiagnostic{Name: "routes", Environment: "production"},
			}
			service, err := bffapp.NewDiagnosticsService(reader, "production")
			if err != nil {
				t.Fatal(err)
			}
			type contextKey struct{}
			ctx := context.WithValue(context.Background(), contextKey{}, "trace")
			principal := bffapp.DiagnosticPrincipal{
				Subject: "operator", Roles: []string{role}, AllowedScopes: []platformauth.ScopePattern{scopePattern(t, "*", "*", "*")},
			}
			if result, err := service.SnapshotDiagnostics(ctx, principal); err != nil || result.Environment != "production" {
				t.Fatalf("snapshot=%+v error=%v", result, err)
			}
			if result, err := service.CollectionDiagnostics(ctx, principal, "routes"); err != nil || result.Name != "routes" {
				t.Fatalf("collection=%+v error=%v", result, err)
			}
			if reader.ctx != ctx || reader.collectionName != "routes" || reader.snapshotCalls != 1 || reader.collectionCalls != 1 {
				t.Fatalf("reader context=%v collection=%q calls=%d/%d", reader.ctx, reader.collectionName, reader.snapshotCalls, reader.collectionCalls)
			}
		})
	}
}

func TestNewDiagnosticsServiceRejectsTypedNilReader(t *testing.T) {
	t.Parallel()
	var reader *diagnosticsReaderStub
	if _, err := bffapp.NewDiagnosticsService(reader, "production"); err == nil {
		t.Fatal("expected typed nil reader rejection")
	}
}

func TestDiagnosticsServiceRejectsReaderBoundToAnotherEnvironmentOrCollection(t *testing.T) {
	t.Parallel()

	principal := bffapp.DiagnosticPrincipal{
		Subject: "operator", Roles: []string{"PLATFORM_OPERATOR"},
		AllowedScopes: []platformauth.ScopePattern{scopePattern(t, "*", "production", "*")},
	}
	reader := &diagnosticsReaderStub{
		snapshot:   bffapp.SnapshotDiagnostics{Environment: "staging"},
		collection: bffapp.CollectionDiagnostic{Name: "other", Environment: "staging"},
	}
	service, err := bffapp.NewDiagnosticsService(reader, "production")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SnapshotDiagnostics(context.Background(), principal); !errors.Is(err, bffapp.ErrDiagnosticsInconsistent) {
		t.Fatalf("snapshot error = %v", err)
	}
	if _, err := service.CollectionDiagnostics(context.Background(), principal, "routes"); !errors.Is(err, bffapp.ErrDiagnosticsInconsistent) {
		t.Fatalf("collection error = %v", err)
	}
}

func scopePattern(t *testing.T, region, environment, stage string) platformauth.ScopePattern {
	t.Helper()
	pattern, err := platformauth.CompileScopePattern(region, environment, stage)
	if err != nil {
		t.Fatal(err)
	}
	return pattern
}

type diagnosticsReaderStub struct {
	snapshot        bffapp.SnapshotDiagnostics
	collection      bffapp.CollectionDiagnostic
	snapshotCalls   int
	collectionCalls int
	ctx             context.Context
	collectionName  string
}

func (stub *diagnosticsReaderStub) ReadSnapshotDiagnostics(ctx context.Context) (bffapp.SnapshotDiagnostics, error) {
	stub.snapshotCalls++
	stub.ctx = ctx
	return stub.snapshot, nil
}

func (stub *diagnosticsReaderStub) ReadCollectionDiagnostics(ctx context.Context, name string) (bffapp.CollectionDiagnostic, error) {
	stub.collectionCalls++
	stub.ctx = ctx
	stub.collectionName = name
	return stub.collection, nil
}

package application

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
)

var (
	ErrDiagnosticsNotFound     = errors.New("diagnostics resource not found")
	ErrDiagnosticsForbidden    = errors.New("diagnostics access forbidden")
	ErrDiagnosticsInconsistent = errors.New("diagnostics result is inconsistent with the managed environment")
)

const (
	diagnosticsPlatformOperatorRole = "PLATFORM_OPERATOR"
	diagnosticsAuditorRole          = "AUDITOR"
)

type DiagnosticDigest struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

type DiagnosticIdentity struct {
	ServerEpoch      string
	ServerInstanceID string
	SnapshotInstance string
	Generation       uint64
	PublishedAt      time.Time
}

type CollectionDiagnostic struct {
	Name          string
	Environment   string
	Revision      uint64
	Cursor        uint64
	Digest        DiagnosticDigest
	LastErrorCode string
}

type SnapshotDiagnostics struct {
	Identity               DiagnosticIdentity
	Environment            string
	Collections            []CollectionDiagnostic
	FailedDependencyGroups [][]string
	LastErrorCode          string
}

type DiagnosticPrincipal struct {
	Subject       string
	Roles         []string
	AllowedScopes []platformauth.ScopePattern
}

type DiagnosticsReader interface {
	ReadSnapshotDiagnostics(context.Context) (SnapshotDiagnostics, error)
	ReadCollectionDiagnostics(context.Context, string) (CollectionDiagnostic, error)
}

type DiagnosticsUseCase interface {
	SnapshotDiagnostics(context.Context, DiagnosticPrincipal) (SnapshotDiagnostics, error)
	CollectionDiagnostics(context.Context, DiagnosticPrincipal, string) (CollectionDiagnostic, error)
}

type diagnosticsService struct {
	reader             DiagnosticsReader
	managedEnvironment string
}

func NewDiagnosticsService(reader DiagnosticsReader, managedEnvironment string) (DiagnosticsUseCase, error) {
	environment, err := platformauth.CompileEnvironment(managedEnvironment)
	if reader == nil || isNilDiagnosticsDependency(reader) || err != nil {
		return nil, errors.New("new diagnostics service: reader and managed environment are required")
	}
	return &diagnosticsService{reader: reader, managedEnvironment: environment}, nil
}

func (service *diagnosticsService) SnapshotDiagnostics(ctx context.Context, principal DiagnosticPrincipal) (SnapshotDiagnostics, error) {
	if !service.authorized(principal) {
		return SnapshotDiagnostics{}, ErrDiagnosticsForbidden
	}
	result, err := service.reader.ReadSnapshotDiagnostics(ctx)
	if err != nil {
		return SnapshotDiagnostics{}, err
	}
	if result.Environment != service.managedEnvironment {
		return SnapshotDiagnostics{}, ErrDiagnosticsInconsistent
	}
	return result, nil
}

func (service *diagnosticsService) CollectionDiagnostics(ctx context.Context, principal DiagnosticPrincipal, collection string) (CollectionDiagnostic, error) {
	if !service.authorized(principal) {
		return CollectionDiagnostic{}, ErrDiagnosticsForbidden
	}
	result, err := service.reader.ReadCollectionDiagnostics(ctx, collection)
	if err != nil {
		return CollectionDiagnostic{}, err
	}
	if result.Environment != service.managedEnvironment || result.Name != collection {
		return CollectionDiagnostic{}, ErrDiagnosticsInconsistent
	}
	return result, nil
}

func (service *diagnosticsService) authorized(principal DiagnosticPrincipal) bool {
	if strings.TrimSpace(principal.Subject) == "" ||
		(!slices.Contains(principal.Roles, diagnosticsPlatformOperatorRole) && !slices.Contains(principal.Roles, diagnosticsAuditorRole)) {
		return false
	}
	for _, pattern := range principal.AllowedScopes {
		if pattern.Environment == "*" || pattern.Environment == service.managedEnvironment {
			return true
		}
	}
	return false
}

func isNilDiagnosticsDependency(value any) bool {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

var _ DiagnosticsUseCase = (*diagnosticsService)(nil)

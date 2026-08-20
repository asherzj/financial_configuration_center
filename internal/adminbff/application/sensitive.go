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
	ErrSensitiveInvalid            = errors.New("invalid sensitive access request")
	ErrSensitiveForbidden          = errors.New("sensitive access forbidden")
	ErrSensitiveAborted            = errors.New("sensitive access authority is stale")
	ErrSensitiveNotFound           = errors.New("sensitive field value not found")
	ErrSensitiveFailedPrecondition = errors.New("sensitive access precondition failed")
)

const SensitiveViewerRole = "SENSITIVE_VIEWER"

type SensitiveScope struct {
	Region      string
	Environment string
	Stage       string
}

type SensitivePrincipal struct {
	Subject       string
	DisplayName   string
	Roles         []string
	AllowedScopes []platformauth.ScopePattern
}

type RevealSensitiveCommand struct {
	ModelCode                  string
	Scope                      SensitiveScope
	RecordKey                  string
	FieldName                  string
	ExpectedRecordRevision     uint64
	ExpectedCollectionRevision uint64
	ExpectedModelRevision      uint64
	ExpectedServerEpoch        string
	ExpectedSnapshotInstance   string
	ExpectedSnapshotGeneration uint64
	Reason                     string
	PreviewBucket              *int32
	RequestID                  string
	TraceID                    string
	TraceParent                string
	TraceState                 string
	Principal                  SensitivePrincipal
}

type RevealSensitiveResult struct {
	Value     string
	ExpiresAt time.Time
}

type SensitiveAccessPort interface {
	RevealField(context.Context, RevealSensitiveCommand) (RevealSensitiveResult, error)
}

type SensitiveAccessUseCase interface {
	Reveal(context.Context, RevealSensitiveCommand) (RevealSensitiveResult, error)
}

type sensitiveAccessService struct{ port SensitiveAccessPort }

func NewSensitiveAccessService(port SensitiveAccessPort) (SensitiveAccessUseCase, error) {
	if port == nil || isNilSensitiveDependency(port) {
		return nil, errors.New("new sensitive access service: port is required")
	}
	return &sensitiveAccessService{port: port}, nil
}

func (service *sensitiveAccessService) Reveal(ctx context.Context, command RevealSensitiveCommand) (RevealSensitiveResult, error) {
	if ctx == nil || strings.TrimSpace(command.Principal.Subject) == "" {
		return RevealSensitiveResult{}, ErrSensitiveInvalid
	}
	scope, err := platformauth.CompileScope(command.Scope.Region, command.Scope.Environment, command.Scope.Stage)
	if err != nil {
		return RevealSensitiveResult{}, ErrSensitiveInvalid
	}
	if !sensitiveScopeCovered(command.Principal.AllowedScopes, scope) {
		return RevealSensitiveResult{}, ErrSensitiveForbidden
	}
	if !slices.Contains(command.Principal.Roles, SensitiveViewerRole) {
		return RevealSensitiveResult{}, ErrSensitiveForbidden
	}
	command.Scope = SensitiveScope{Region: scope.Region, Environment: scope.Environment, Stage: scope.Stage}
	command.Principal = cloneSensitivePrincipal(command.Principal)
	return service.port.RevealField(ctx, command)
}

func sensitiveScopeCovered(patterns []platformauth.ScopePattern, scope platformauth.Scope) bool {
	for _, pattern := range patterns {
		if pattern.Matches(scope) {
			return true
		}
	}
	return false
}

func cloneSensitivePrincipal(source SensitivePrincipal) SensitivePrincipal {
	return SensitivePrincipal{
		Subject: source.Subject, DisplayName: source.DisplayName,
		Roles: append([]string(nil), source.Roles...), AllowedScopes: append([]platformauth.ScopePattern(nil), source.AllowedScopes...),
	}
}

func isNilSensitiveDependency(value any) bool {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

var _ SensitiveAccessUseCase = (*sensitiveAccessService)(nil)

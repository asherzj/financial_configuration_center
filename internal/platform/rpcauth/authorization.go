package rpcauth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
)

const (
	roleConfigViewer     = "CONFIG_VIEWER"
	rolePlatformOperator = "PLATFORM_OPERATOR"
	roleAuditor          = "AUDITOR"
)

type AuthorizationPolicy struct {
	AdditionalPageQueryRoles []string
	RefreshRelaySubjects     []string
}

type RequestAuthorizer struct {
	pageQueryRoles map[string]struct{}
	refreshRelays  map[string]struct{}
	initialized    bool
}

var diagnosticRoles = map[string]struct{}{rolePlatformOperator: {}, roleAuditor: {}}

func NewRequestAuthorizer(policy AuthorizationPolicy) (*RequestAuthorizer, error) {
	pageQueryRoles, err := exactSet(policy.AdditionalPageQueryRoles, false)
	if err != nil {
		return nil, fmt.Errorf("page query roles: %w", err)
	}
	pageQueryRoles[roleConfigViewer] = struct{}{}
	refreshRelays, err := exactSet(policy.RefreshRelaySubjects, true)
	if err != nil {
		return nil, fmt.Errorf("refresh relay subjects: %w", err)
	}
	return &RequestAuthorizer{
		pageQueryRoles: pageQueryRoles,
		refreshRelays:  refreshRelays,
		initialized:    true,
	}, nil
}

func (authorizer *RequestAuthorizer) AuthorizeConsumer(ctx context.Context, consumerID string, scope platformauth.Scope) error {
	if authorizer == nil || !authorizer.initialized {
		return permissionDenied()
	}
	identity, ok := ConsumerIdentityFromContext(ctx)
	if !ok || consumerID == "" || identity.ConsumerID != consumerID || !scopeCovered(identity.Scopes, scope) {
		return permissionDenied()
	}
	return nil
}

func (authorizer *RequestAuthorizer) AuthorizePageQuery(ctx context.Context, scope platformauth.Scope) error {
	if authorizer == nil || !authorizer.initialized {
		return permissionDenied()
	}
	identity, ok := InternalCallerIdentityFromContext(ctx)
	if !ok || !hasAnyRole(identity.Roles, authorizer.pageQueryRoles) || !scopeCovered(identity.Scopes, scope) {
		return permissionDenied()
	}
	return nil
}

func (authorizer *RequestAuthorizer) AuthorizeRefresh(ctx context.Context, environment string) error {
	if authorizer == nil || !authorizer.initialized {
		return permissionDenied()
	}
	identity, ok := InternalCallerIdentityFromContext(ctx)
	_, allowedSubject := authorizer.refreshRelays[identity.Subject]
	if !ok || !allowedSubject || !environmentCovered(identity.Scopes, environment) {
		return permissionDenied()
	}
	return nil
}

func (authorizer *RequestAuthorizer) AuthorizeDiagnostics(ctx context.Context, environment string) error {
	if authorizer == nil || !authorizer.initialized {
		return permissionDenied()
	}
	identity, ok := InternalCallerIdentityFromContext(ctx)
	if !ok || !hasAnyRole(identity.Roles, diagnosticRoles) || !environmentCovered(identity.Scopes, environment) {
		return permissionDenied()
	}
	return nil
}

func exactSet(values []string, required bool) (map[string]struct{}, error) {
	if required && len(values) == 0 {
		return nil, errors.New("at least one value is required")
	}
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || value != strings.TrimSpace(value) {
			return nil, errors.New("values must be non-empty without surrounding whitespace")
		}
		if _, duplicate := result[value]; duplicate {
			return nil, fmt.Errorf("duplicate value %q", value)
		}
		result[value] = struct{}{}
	}
	return result, nil
}

func scopeCovered(patterns []platformauth.ScopePattern, scope platformauth.Scope) bool {
	if scope.Region == "" || scope.Environment == "" {
		return false
	}
	for _, pattern := range patterns {
		if pattern.Matches(scope) {
			return true
		}
	}
	return false
}

func environmentCovered(patterns []platformauth.ScopePattern, environment string) bool {
	if environment == "" {
		return false
	}
	for _, pattern := range patterns {
		if pattern.Environment == "*" || pattern.Environment == environment {
			return true
		}
	}
	return false
}

func hasAnyRole(identityRoles []string, allowed map[string]struct{}) bool {
	for _, role := range identityRoles {
		if _, ok := allowed[role]; ok {
			return true
		}
	}
	return false
}

func permissionDenied() error {
	return rpcStatusError(kitexcodes.PermissionDenied, "authenticated identity is not authorized for this request")
}

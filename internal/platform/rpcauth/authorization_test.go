package rpcauth

import (
	"context"
	"testing"

	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
)

func TestRequestAuthorizerBindsConsumerSubjectAndScope(t *testing.T) {
	t.Parallel()
	authorizer := newRequestAuthorizer(t)
	ctx := withConsumerIdentity(context.Background(), platformauth.ConsumerIdentity{
		ConsumerID: "payments",
		Scopes:     []platformauth.ScopePattern{{Region: "cn", Environment: "production", Stage: "*"}},
	})
	requestScope := platformauth.Scope{Region: "cn", Environment: "production", Stage: "blue"}
	if err := authorizer.AuthorizeConsumer(ctx, "payments", requestScope); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		consumerID string
		scope      platformauth.Scope
	}{
		"subject mismatch": {consumerID: "orders", scope: requestScope},
		"scope mismatch":   {consumerID: "payments", scope: platformauth.Scope{Region: "us", Environment: "production", Stage: "blue"}},
	} {
		if err := authorizer.AuthorizeConsumer(ctx, test.consumerID, test.scope); kitexstatus.Code(err) != kitexcodes.PermissionDenied {
			t.Fatalf("%s code=%v err=%v", name, kitexstatus.Code(err), err)
		}
	}
	if err := authorizer.AuthorizeConsumer(context.Background(), "payments", requestScope); kitexstatus.Code(err) != kitexcodes.PermissionDenied {
		t.Fatalf("missing identity code=%v err=%v", kitexstatus.Code(err), err)
	}
}

func TestRequestAuthorizerRequiresPageQueryRoleAndScope(t *testing.T) {
	t.Parallel()
	authorizer := newRequestAuthorizer(t)
	scope := platformauth.Scope{Region: "cn", Environment: "production", Stage: "blue"}
	for _, role := range []string{"CONFIG_VIEWER", "SUPPORT_DIAGNOSTIC"} {
		ctx := withInternalCallerIdentity(context.Background(), platformauth.InternalCallerIdentity{
			Subject: "admin-bff", Roles: []string{role},
			Scopes: []platformauth.ScopePattern{{Region: "cn", Environment: "production", Stage: "*"}},
		})
		if err := authorizer.AuthorizePageQuery(ctx, scope); err != nil {
			t.Fatalf("role %s: %v", role, err)
		}
	}
	for name, identity := range map[string]platformauth.InternalCallerIdentity{
		"role":  {Subject: "admin-bff", Roles: []string{"AUDITOR"}, Scopes: []platformauth.ScopePattern{{Region: "cn", Environment: "production", Stage: "*"}}},
		"scope": {Subject: "admin-bff", Roles: []string{"CONFIG_VIEWER"}, Scopes: []platformauth.ScopePattern{{Region: "us", Environment: "production", Stage: "*"}}},
	} {
		err := authorizer.AuthorizePageQuery(withInternalCallerIdentity(context.Background(), identity), scope)
		if kitexstatus.Code(err) != kitexcodes.PermissionDenied {
			t.Fatalf("%s code=%v err=%v", name, kitexstatus.Code(err), err)
		}
	}
}

func TestRequestAuthorizerBindsRelaySubjectAndManagedEnvironment(t *testing.T) {
	t.Parallel()
	authorizer := newRequestAuthorizer(t)
	valid := withInternalCallerIdentity(context.Background(), platformauth.InternalCallerIdentity{
		Subject: "control-plane-relay", Scopes: []platformauth.ScopePattern{{Region: "*", Environment: "production", Stage: "*"}},
	})
	if err := authorizer.AuthorizeRefresh(valid, "production"); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		identity    platformauth.InternalCallerIdentity
		environment string
	}{
		"subject": {identity: platformauth.InternalCallerIdentity{Subject: "admin-bff", Scopes: []platformauth.ScopePattern{{Region: "*", Environment: "production", Stage: "*"}}}, environment: "production"},
		"scope":   {identity: platformauth.InternalCallerIdentity{Subject: "control-plane-relay", Scopes: []platformauth.ScopePattern{{Region: "*", Environment: "staging", Stage: "*"}}}, environment: "production"},
	} {
		err := authorizer.AuthorizeRefresh(withInternalCallerIdentity(context.Background(), test.identity), test.environment)
		if kitexstatus.Code(err) != kitexcodes.PermissionDenied {
			t.Fatalf("%s code=%v err=%v", name, kitexstatus.Code(err), err)
		}
	}
}

func TestRequestAuthorizerRequiresDiagnosticRoleAndManagedEnvironment(t *testing.T) {
	t.Parallel()
	authorizer := newRequestAuthorizer(t)
	for _, role := range []string{"PLATFORM_OPERATOR", "AUDITOR"} {
		ctx := withInternalCallerIdentity(context.Background(), platformauth.InternalCallerIdentity{
			Subject: "operator", Roles: []string{role},
			Scopes: []platformauth.ScopePattern{{Region: "*", Environment: "production", Stage: "*"}},
		})
		if err := authorizer.AuthorizeDiagnostics(ctx, "production"); err != nil {
			t.Fatalf("role %s: %v", role, err)
		}
	}
	denied := withInternalCallerIdentity(context.Background(), platformauth.InternalCallerIdentity{
		Subject: "viewer", Roles: []string{"CONFIG_VIEWER"},
		Scopes: []platformauth.ScopePattern{{Region: "*", Environment: "production", Stage: "*"}},
	})
	if err := authorizer.AuthorizeDiagnostics(denied, "production"); kitexstatus.Code(err) != kitexcodes.PermissionDenied {
		t.Fatalf("diagnostic role code=%v err=%v", kitexstatus.Code(err), err)
	}
}

func TestRequestAuthorizerRejectsIncompleteOrAmbiguousPolicy(t *testing.T) {
	t.Parallel()
	valid := AuthorizationPolicy{RefreshRelaySubjects: []string{"control-plane-relay"}}
	for name, policy := range map[string]AuthorizationPolicy{
		"relays":    {},
		"duplicate": {RefreshRelaySubjects: []string{"relay", "relay"}},
		"role":      {RefreshRelaySubjects: valid.RefreshRelaySubjects, AdditionalPageQueryRoles: []string{" CONFIG_VIEWER"}},
	} {
		if _, err := NewRequestAuthorizer(policy); err == nil {
			t.Fatalf("%s policy accepted", name)
		}
	}
}

func TestRequestAuthorizerNilAndZeroValuesFailClosed(t *testing.T) {
	t.Parallel()
	ctx := withConsumerIdentity(context.Background(), platformauth.ConsumerIdentity{
		ConsumerID: "payments", Scopes: []platformauth.ScopePattern{{Region: "cn", Environment: "production", Stage: "*"}},
	})
	scope := platformauth.Scope{Region: "cn", Environment: "production", Stage: "blue"}
	for name, authorizer := range map[string]*RequestAuthorizer{"nil": nil, "zero": {}} {
		if err := authorizer.AuthorizeConsumer(ctx, "payments", scope); kitexstatus.Code(err) != kitexcodes.PermissionDenied {
			t.Fatalf("%s authorizer code=%v err=%v", name, kitexstatus.Code(err), err)
		}
	}
}

func newRequestAuthorizer(t *testing.T) *RequestAuthorizer {
	t.Helper()
	authorizer, err := NewRequestAuthorizer(AuthorizationPolicy{
		AdditionalPageQueryRoles: []string{"SUPPORT_DIAGNOSTIC"},
		RefreshRelaySubjects:     []string{"control-plane-relay"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return authorizer
}

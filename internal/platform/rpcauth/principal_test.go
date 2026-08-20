package rpcauth

import (
	"context"
	"testing"

	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	kitexmetadata "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/metadata"
)

func TestInternalPrincipalResolverUsesAuthenticatedIdentityAndValidatedMetadata(t *testing.T) {
	t.Parallel()
	ctx := withInternalCallerIdentity(context.Background(), platformauth.InternalCallerIdentity{
		Subject: "operator", DisplayName: "Operator", Roles: []string{"SENSITIVE_VIEWER"},
		Scopes: []platformauth.ScopePattern{{Region: "cn", Environment: "production", Stage: "*"}},
	})
	ctx = kitexmetadata.NewIncomingContext(ctx, kitexmetadata.Pairs(
		"x-request-id", "reveal-1",
		"traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	))
	resolver := InternalPrincipalResolver{}
	subject, err := resolver.Subject(ctx)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := resolver.Roles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scopes, err := resolver.Scopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	displayName, err := resolver.DisplayName(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if subject != "operator" || displayName != "Operator" || len(roles) != 1 || len(scopes) != 1 || resolver.RequestID(ctx) != "reveal-1" || resolver.TraceID(ctx) != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("subject=%q roles=%v scopes=%v request=%q trace=%q", subject, roles, scopes, resolver.RequestID(ctx), resolver.TraceID(ctx))
	}
	roles[0] = "mutated"
	scopes[0].Environment = "mutated"
	againRoles, _ := resolver.Roles(ctx)
	againScopes, _ := resolver.Scopes(ctx)
	if againRoles[0] != "SENSITIVE_VIEWER" || againScopes[0].Environment != "production" {
		t.Fatal("resolver exposed mutable identity slices")
	}
}

func TestInternalPrincipalResolverRejectsMalformedOrAmbiguousMetadata(t *testing.T) {
	t.Parallel()
	resolver := InternalPrincipalResolver{}
	ctx := kitexmetadata.NewIncomingContext(context.Background(), kitexmetadata.Pairs(
		"x-request-id", "one", "x-request-id", "two", "traceparent", "not-a-trace",
	))
	if resolver.RequestID(ctx) != "" || resolver.TraceID(ctx) != "" {
		t.Fatalf("request=%q trace=%q", resolver.RequestID(ctx), resolver.TraceID(ctx))
	}
	if _, err := resolver.Subject(ctx); err == nil {
		t.Fatal("expected missing authenticated identity rejection")
	}
}

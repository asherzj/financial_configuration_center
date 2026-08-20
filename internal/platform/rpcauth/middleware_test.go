package rpcauth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	"github.com/asherzj/financial_configuration_center/internal/platform/rpcauth"
	"github.com/cloudwego/kitex/pkg/endpoint"
	"github.com/cloudwego/kitex/pkg/endpoint/sep"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	"github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/metadata"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/pkg/streaming"
)

func TestUnaryMiddlewareSelectsConsumerProfileAndStoresReadonlyIdentity(t *testing.T) {
	t.Parallel()
	consumerCalls, internalCalls := 0, 0
	authenticator := newAuthenticator(t,
		consumerVerifierFunc(func(_ context.Context, token string) (platformauth.ConsumerIdentity, error) {
			consumerCalls++
			if token != "consumer-token" {
				return platformauth.ConsumerIdentity{}, platformauth.ErrTokenInvalid
			}
			return platformauth.ConsumerIdentity{ConsumerID: "payments", JWTID: "consumer-jti", Scopes: []platformauth.ScopePattern{{Region: "cn", Environment: "production", Stage: "*"}}}, nil
		}),
		internalVerifierFunc(func(context.Context, string) (platformauth.InternalCallerIdentity, error) {
			internalCalls++
			return platformauth.InternalCallerIdentity{}, nil
		}),
	)
	called := false
	next := endpoint.Endpoint(func(ctx context.Context, _, _ interface{}) error {
		called = true
		identity, ok := rpcauth.ConsumerIdentityFromContext(ctx)
		if !ok || identity.ConsumerID != "payments" || len(identity.Scopes) != 1 {
			t.Fatalf("consumer identity=%+v ok=%v", identity, ok)
		}
		identity.Scopes[0].Region = "mutated"
		again, _ := rpcauth.ConsumerIdentityFromContext(ctx)
		if again.Scopes[0].Region != "cn" {
			t.Fatalf("stored identity was mutable: %+v", again)
		}
		return nil
	})
	err := authenticator.UnaryMiddleware()(next)(rpcContext("ConfigService", "GetSnapshot", "Bearer consumer-token"), nil, nil)
	if err != nil || !called || consumerCalls != 1 || internalCalls != 0 {
		t.Fatalf("called=%v consumer=%d internal=%d err=%v", called, consumerCalls, internalCalls, err)
	}
}

func TestUnaryMiddlewareSelectsInternalProfile(t *testing.T) {
	t.Parallel()
	authenticator := newAuthenticator(t,
		consumerVerifierFunc(func(context.Context, string) (platformauth.ConsumerIdentity, error) {
			t.Fatal("consumer verifier called for internal RPC")
			return platformauth.ConsumerIdentity{}, nil
		}),
		internalVerifierFunc(func(_ context.Context, token string) (platformauth.InternalCallerIdentity, error) {
			if token != "internal-token" {
				return platformauth.InternalCallerIdentity{}, platformauth.ErrTokenInvalid
			}
			return platformauth.InternalCallerIdentity{Subject: "admin-bff", JWTID: "internal-jti", Roles: []string{"CONFIG_VIEWER"}}, nil
		}),
	)
	next := endpoint.Endpoint(func(ctx context.Context, _, _ interface{}) error {
		identity, ok := rpcauth.InternalCallerIdentityFromContext(ctx)
		if !ok || identity.Subject != "admin-bff" || len(identity.Roles) != 1 {
			t.Fatalf("internal identity=%+v ok=%v", identity, ok)
		}
		return nil
	})
	if err := authenticator.UnaryMiddleware()(next)(rpcContext("PageQueryService", "QueryPage", "Bearer internal-token"), nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestStreamMiddlewareAuthenticatesWatchWithConsumerProfile(t *testing.T) {
	t.Parallel()
	authenticator := newAuthenticator(t,
		consumerVerifierFunc(func(_ context.Context, token string) (platformauth.ConsumerIdentity, error) {
			return platformauth.ConsumerIdentity{ConsumerID: token, JWTID: "watch-jti"}, nil
		}),
		internalVerifierFunc(func(context.Context, string) (platformauth.InternalCallerIdentity, error) {
			t.Fatal("internal verifier called for Watch")
			return platformauth.InternalCallerIdentity{}, nil
		}),
	)
	called := false
	next := sep.StreamEndpoint(func(ctx context.Context, _ streaming.ServerStream) error {
		called = true
		identity, ok := rpcauth.ConsumerIdentityFromContext(ctx)
		if !ok || identity.ConsumerID != "watch-token" {
			t.Fatalf("watch identity=%+v ok=%v", identity, ok)
		}
		return nil
	})
	if err := authenticator.StreamMiddleware()(next)(rpcContext("ConfigService", "Watch", "Bearer watch-token"), nil); err != nil || !called {
		t.Fatalf("called=%v err=%v", called, err)
	}
}

func TestMiddlewareRejectsMissingDuplicateMalformedAndOversizedAuthorization(t *testing.T) {
	t.Parallel()
	authenticator := newAuthenticator(t,
		consumerVerifierFunc(func(context.Context, string) (platformauth.ConsumerIdentity, error) {
			t.Fatal("verifier called for invalid authorization metadata")
			return platformauth.ConsumerIdentity{}, nil
		}),
		internalVerifierFunc(func(context.Context, string) (platformauth.InternalCallerIdentity, error) {
			t.Fatal("verifier called for invalid authorization metadata")
			return platformauth.InternalCallerIdentity{}, nil
		}),
	)
	tests := map[string][]string{
		"missing":    nil,
		"duplicate":  {"Bearer one", "Bearer two"},
		"wrong case": {"bearer token"},
		"whitespace": {"Bearer token "},
		"oversized":  {"Bearer " + strings.Repeat("x", 16<<10+1)},
	}
	for name, values := range tests {
		name, values := name, values
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			called := false
			next := endpoint.Endpoint(func(context.Context, interface{}, interface{}) error { called = true; return nil })
			err := authenticator.UnaryMiddleware()(next)(rpcContext("ConfigService", "GetSnapshot", values...), nil, nil)
			if kitexstatus.Code(err) != kitexcodes.Unauthenticated || called {
				t.Fatalf("code=%v called=%v err=%v", kitexstatus.Code(err), called, err)
			}
		})
	}
}

func TestMiddlewareRejectsInvalidTokenAndUnregisteredMethodBeforeHandler(t *testing.T) {
	t.Parallel()
	authenticator := newAuthenticator(t,
		consumerVerifierFunc(func(context.Context, string) (platformauth.ConsumerIdentity, error) {
			return platformauth.ConsumerIdentity{}, errors.New("sensitive verifier detail")
		}),
		internalVerifierFunc(func(context.Context, string) (platformauth.InternalCallerIdentity, error) {
			return platformauth.InternalCallerIdentity{}, errors.New("sensitive verifier detail")
		}),
	)
	next := endpoint.Endpoint(func(context.Context, interface{}, interface{}) error { t.Fatal("handler called"); return nil })
	invalid := authenticator.UnaryMiddleware()(next)(rpcContext("ConfigService", "GetSnapshot", "Bearer invalid"), nil, nil)
	if kitexstatus.Code(invalid) != kitexcodes.Unauthenticated || strings.Contains(invalid.Error(), "sensitive") {
		t.Fatalf("invalid token error=%v", invalid)
	}
	unknown := authenticator.UnaryMiddleware()(next)(rpcContext("ConfigService", "Unknown", "Bearer token"), nil, nil)
	if kitexstatus.Code(unknown) != kitexcodes.Internal {
		t.Fatalf("unknown method code=%v err=%v", kitexstatus.Code(unknown), unknown)
	}
}

func TestKitexServerOptionsInstallUnaryCompatibilityAndBothMiddlewarePaths(t *testing.T) {
	t.Parallel()
	authenticator := newAuthenticator(t,
		consumerVerifierFunc(func(context.Context, string) (platformauth.ConsumerIdentity, error) {
			return platformauth.ConsumerIdentity{}, nil
		}),
		internalVerifierFunc(func(context.Context, string) (platformauth.InternalCallerIdentity, error) {
			return platformauth.InternalCallerIdentity{}, nil
		}),
	)
	options, err := rpcauth.KitexServerOptions(authenticator)
	if err != nil {
		t.Fatal(err)
	}
	if len(options) != 3 {
		t.Fatalf("server option count=%d, want unary compatibility, unary auth, and stream auth", len(options))
	}
}

func TestMethodPolicyMatrixKeepsConsumerAndInternalProfilesSeparate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		service  string
		method   string
		consumer bool
	}{
		{service: "ConfigService", method: "GetSnapshot", consumer: true},
		{service: "ConfigService", method: "DiffVersions", consumer: true},
		{service: "ConfigService", method: "GetCollections", consumer: true},
		{service: "ConfigService", method: "Watch", consumer: true},
		{service: "PageQueryService", method: "QueryPage"},
		{service: "RefreshService", method: "Notify"},
		{service: "DiagnosticsService", method: "GetSnapshotStatus"},
		{service: "DiagnosticsService", method: "GetCollectionStatus"},
		{service: "SensitiveAccessService", method: "RevealField"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.service+"/"+test.method, func(t *testing.T) {
			consumerCalled, internalCalled := false, false
			authenticator := newAuthenticator(t,
				consumerVerifierFunc(func(context.Context, string) (platformauth.ConsumerIdentity, error) {
					consumerCalled = true
					return platformauth.ConsumerIdentity{ConsumerID: "consumer"}, nil
				}),
				internalVerifierFunc(func(context.Context, string) (platformauth.InternalCallerIdentity, error) {
					internalCalled = true
					return platformauth.InternalCallerIdentity{Subject: "internal"}, nil
				}),
			)
			next := endpoint.Endpoint(func(context.Context, interface{}, interface{}) error { return nil })
			if err := authenticator.UnaryMiddleware()(next)(rpcContext(test.service, test.method, "Bearer token"), nil, nil); err != nil {
				t.Fatal(err)
			}
			if consumerCalled != test.consumer || internalCalled == test.consumer {
				t.Fatalf("consumer called=%v internal called=%v", consumerCalled, internalCalled)
			}
		})
	}
}

func TestAuthenticatorRequiresBothTrustProfiles(t *testing.T) {
	t.Parallel()
	consumer := consumerVerifierFunc(func(context.Context, string) (platformauth.ConsumerIdentity, error) {
		return platformauth.ConsumerIdentity{}, nil
	})
	internal := internalVerifierFunc(func(context.Context, string) (platformauth.InternalCallerIdentity, error) {
		return platformauth.InternalCallerIdentity{}, nil
	})
	if _, err := rpcauth.New(nil, internal); err == nil {
		t.Fatal("missing Consumer verifier accepted")
	}
	if _, err := rpcauth.New(consumer, nil); err == nil {
		t.Fatal("missing Internal verifier accepted")
	}
	if _, err := rpcauth.KitexServerOptions(nil); err == nil {
		t.Fatal("nil Authenticator silently produced unauthenticated server options")
	}
	if _, err := rpcauth.KitexServerOptions(&rpcauth.Authenticator{}); err == nil {
		t.Fatal("zero-value Authenticator was accepted for server assembly")
	}
}

func newAuthenticator(t *testing.T, consumer rpcauth.ConsumerVerifier, internal rpcauth.InternalVerifier) *rpcauth.Authenticator {
	t.Helper()
	authenticator, err := rpcauth.New(consumer, internal)
	if err != nil {
		t.Fatal(err)
	}
	return authenticator
}

func rpcContext(service, method string, authorization ...string) context.Context {
	ctx := context.Background()
	if authorization != nil {
		ctx = metadata.NewIncomingContext(ctx, metadata.MD{"authorization": authorization})
	}
	info := rpcinfo.NewRPCInfo(nil, nil, rpcinfo.NewInvocation(service, method), nil, nil)
	return rpcinfo.NewCtxWithRPCInfo(ctx, info)
}

type consumerVerifierFunc func(context.Context, string) (platformauth.ConsumerIdentity, error)

func (verify consumerVerifierFunc) Verify(ctx context.Context, token string) (platformauth.ConsumerIdentity, error) {
	return verify(ctx, token)
}

type internalVerifierFunc func(context.Context, string) (platformauth.InternalCallerIdentity, error)

func (verify internalVerifierFunc) Verify(ctx context.Context, token string) (platformauth.InternalCallerIdentity, error) {
	return verify(ctx, token)
}

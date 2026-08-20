package rpcauth

import (
	"context"
	"errors"
	"strings"

	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	"github.com/cloudwego/kitex/pkg/endpoint"
	"github.com/cloudwego/kitex/pkg/endpoint/sep"
	"github.com/cloudwego/kitex/pkg/kerrors"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	"github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/metadata"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/pkg/streaming"
	"github.com/cloudwego/kitex/server"
)

const maxBearerTokenBytes = 16 << 10

type ConsumerVerifier interface {
	Verify(context.Context, string) (platformauth.ConsumerIdentity, error)
}

type InternalVerifier interface {
	Verify(context.Context, string) (platformauth.InternalCallerIdentity, error)
}

type Authenticator struct {
	consumer ConsumerVerifier
	internal InternalVerifier
}

func New(consumer ConsumerVerifier, internal InternalVerifier) (*Authenticator, error) {
	if consumer == nil || internal == nil {
		return nil, errors.New("RPC authentication requires Consumer and Internal token verifiers")
	}
	return &Authenticator{consumer: consumer, internal: internal}, nil
}

func (authenticator *Authenticator) UnaryMiddleware() endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, request, response interface{}) error {
			authenticated, err := authenticator.authenticate(ctx)
			if err != nil {
				return err
			}
			return next(authenticated, request, response)
		}
	}
}

func (authenticator *Authenticator) StreamMiddleware() sep.StreamMiddleware {
	return func(next sep.StreamEndpoint) sep.StreamEndpoint {
		return func(ctx context.Context, stream streaming.ServerStream) error {
			authenticated, err := authenticator.authenticate(ctx)
			if err != nil {
				return err
			}
			return next(authenticated, stream)
		}
	}
}

func KitexServerOptions(authenticator *Authenticator) ([]server.Option, error) {
	if authenticator == nil || authenticator.consumer == nil || authenticator.internal == nil {
		return nil, errors.New("Kitex server authentication requires a constructed Authenticator")
	}
	return []server.Option{
		server.WithCompatibleMiddlewareForUnary(),
		server.WithMiddleware(authenticator.UnaryMiddleware()),
		server.WithStreamOptions(server.WithStreamMiddleware(authenticator.StreamMiddleware())),
	}, nil
}

func (authenticator *Authenticator) authenticate(ctx context.Context) (context.Context, error) {
	if authenticator == nil || authenticator.consumer == nil || authenticator.internal == nil {
		return nil, rpcStatusError(kitexcodes.Internal, "RPC authentication is not configured")
	}
	profile, ok := policyForContext(ctx)
	if !ok {
		return nil, rpcStatusError(kitexcodes.Internal, "RPC authentication policy is not configured")
	}
	token, ok := bearerToken(ctx)
	if !ok {
		return nil, rpcStatusError(kitexcodes.Unauthenticated, "valid Bearer authentication is required")
	}
	switch profile {
	case consumerProfile:
		identity, err := authenticator.consumer.Verify(ctx, token)
		if err != nil {
			return nil, rpcStatusError(kitexcodes.Unauthenticated, "valid Consumer authentication is required")
		}
		return withConsumerIdentity(ctx, identity), nil
	case internalProfile:
		identity, err := authenticator.internal.Verify(ctx, token)
		if err != nil {
			return nil, rpcStatusError(kitexcodes.Unauthenticated, "valid Internal authentication is required")
		}
		return withInternalCallerIdentity(ctx, identity), nil
	default:
		return nil, rpcStatusError(kitexcodes.Internal, "RPC authentication policy is invalid")
	}
}

func rpcStatusError(code kitexcodes.Code, message string) error {
	businessError := kerrors.NewGRPCBizStatusError(int32(code), message)
	businessError.(kerrors.GRPCStatusIface).SetGRPCStatus(kitexstatus.New(code, message))
	return businessError
}

func bearerToken(ctx context.Context) (string, bool) {
	values := []string(nil)
	if incoming, ok := metadata.FromIncomingContext(ctx); ok {
		values = incoming.Get("authorization")
	}
	if len(values) != 1 || len(values[0]) > len("Bearer ")+maxBearerTokenBytes || !strings.HasPrefix(values[0], "Bearer ") {
		return "", false
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if token == "" || len(token) > maxBearerTokenBytes || token != strings.TrimSpace(token) || strings.ContainsAny(token, " \t\r\n") {
		return "", false
	}
	return token, true
}

type authenticationProfile uint8

const (
	consumerProfile authenticationProfile = iota + 1
	internalProfile
)

var methodPolicies = map[string]authenticationProfile{
	"ConfigService/GetSnapshot":              consumerProfile,
	"ConfigService/DiffVersions":             consumerProfile,
	"ConfigService/GetCollections":           consumerProfile,
	"ConfigService/Watch":                    consumerProfile,
	"PageQueryService/QueryPage":             internalProfile,
	"RefreshService/Notify":                  internalProfile,
	"DiagnosticsService/GetSnapshotStatus":   internalProfile,
	"DiagnosticsService/GetCollectionStatus": internalProfile,
	"SensitiveAccessService/RevealField":     internalProfile,
}

func policyForContext(ctx context.Context) (authenticationProfile, bool) {
	info := rpcinfo.GetRPCInfo(ctx)
	if info == nil || info.Invocation() == nil {
		return 0, false
	}
	invocation := info.Invocation()
	profile, ok := methodPolicies[invocation.ServiceName()+"/"+invocation.MethodName()]
	return profile, ok
}

package rpcauth

import (
	"context"

	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
)

type identityContextKey uint8

const (
	consumerIdentityKey identityContextKey = iota + 1
	internalCallerIdentityKey
)

func ConsumerIdentityFromContext(ctx context.Context) (platformauth.ConsumerIdentity, bool) {
	identity, ok := ctx.Value(consumerIdentityKey).(platformauth.ConsumerIdentity)
	if !ok {
		return platformauth.ConsumerIdentity{}, false
	}
	return cloneConsumerIdentity(identity), true
}

func InternalCallerIdentityFromContext(ctx context.Context) (platformauth.InternalCallerIdentity, bool) {
	identity, ok := ctx.Value(internalCallerIdentityKey).(platformauth.InternalCallerIdentity)
	if !ok {
		return platformauth.InternalCallerIdentity{}, false
	}
	return cloneInternalCallerIdentity(identity), true
}

func withConsumerIdentity(ctx context.Context, identity platformauth.ConsumerIdentity) context.Context {
	return context.WithValue(ctx, consumerIdentityKey, cloneConsumerIdentity(identity))
}

func withInternalCallerIdentity(ctx context.Context, identity platformauth.InternalCallerIdentity) context.Context {
	return context.WithValue(ctx, internalCallerIdentityKey, cloneInternalCallerIdentity(identity))
}

func cloneConsumerIdentity(identity platformauth.ConsumerIdentity) platformauth.ConsumerIdentity {
	identity.Scopes = append([]platformauth.ScopePattern(nil), identity.Scopes...)
	return identity
}

func cloneInternalCallerIdentity(identity platformauth.InternalCallerIdentity) platformauth.InternalCallerIdentity {
	identity.Roles = append([]string(nil), identity.Roles...)
	identity.Scopes = append([]platformauth.ScopePattern(nil), identity.Scopes...)
	return identity
}

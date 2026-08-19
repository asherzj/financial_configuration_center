package runtime

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	platformconfig "github.com/asherzj/financial_configuration_center/internal/platform/config"
	"github.com/asherzj/financial_configuration_center/internal/platform/rpcauth"
)

type Security struct {
	Authenticator *rpcauth.Authenticator
	Authorizer    *rpcauth.RequestAuthorizer

	consumerVerifier *platformauth.ConsumerJWTVerifier
	internalVerifier *platformauth.InternalJWTVerifier
	transport        http.RoundTripper
	closeOnce        sync.Once
}

type securityDependencies struct {
	loadKeyRing   func(map[string]string) (platformauth.StaticKeys, error)
	httpTransport http.RoundTripper
	clock         func() time.Time
}

func NewProductionSecurity(config platformconfig.AuthConfig) (*Security, error) {
	return newProductionSecurity(config, securityDependencies{})
}

func newProductionSecurity(config platformconfig.AuthConfig, dependencies securityDependencies) (*Security, error) {
	if err := config.ValidateRPC(); err != nil {
		return nil, err
	}
	if dependencies.httpTransport != nil && isNil(dependencies.httpTransport) {
		return nil, errors.New("production security HTTP transport is typed nil")
	}
	loadKeyRing := dependencies.loadKeyRing
	if loadKeyRing == nil {
		loadKeyRing = platformauth.LoadEd25519PublicKeyRing
	}
	clock := dependencies.clock
	if clock == nil {
		clock = time.Now
	}
	transport := dependencies.httpTransport
	if transport == nil {
		transport = newJWKSHTTPTransport(config.ConsumerJWT.HTTPTimeout.Duration)
	}
	keys, err := loadKeyRing(config.InternalJWT.PublicKeyFiles)
	if err != nil {
		closeIdleConnections(transport)
		return nil, err
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   config.ConsumerJWT.HTTPTimeout.Duration,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("JWKS redirects are not allowed")
		},
	}
	remoteKeys, err := platformauth.NewRemoteJWKS(config.ConsumerJWT.JWKSURL, client, config.ConsumerJWT.JWKSCacheTTL.Duration, clock)
	if err != nil {
		closeIdleConnections(transport)
		return nil, err
	}
	consumerVerifier, err := platformauth.NewConsumerJWTVerifier(remoteKeys, config.ConsumerJWT.Issuer, config.ConsumerJWT.Audience, clock)
	if err != nil {
		closeIdleConnections(transport)
		return nil, err
	}
	internalVerifier, err := platformauth.NewInternalJWTVerifier(keys, config.InternalJWT.Issuer, config.InternalJWT.Audience, clock)
	if err != nil {
		closeIdleConnections(transport)
		return nil, err
	}
	authenticator, err := rpcauth.New(consumerVerifier, internalVerifier)
	if err != nil {
		closeIdleConnections(transport)
		return nil, err
	}
	authorizer, err := rpcauth.NewRequestAuthorizer(rpcauth.AuthorizationPolicy{
		AdditionalPageQueryRoles: config.AdditionalPageQueryRoles,
		RefreshRelaySubjects:     config.RefreshRelaySubjects,
	})
	if err != nil {
		closeIdleConnections(transport)
		return nil, err
	}
	return &Security{
		Authenticator: authenticator, Authorizer: authorizer,
		consumerVerifier: consumerVerifier, internalVerifier: internalVerifier, transport: transport,
	}, nil
}

func newJWKSHTTPTransport(timeout time.Duration) *http.Transport {
	connectionTimeout := min(timeout, 10*time.Second)
	return &http.Transport{
		Proxy:                  http.ProxyFromEnvironment,
		DialContext:            (&net.Dialer{Timeout: connectionTimeout, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           16,
		MaxIdleConnsPerHost:    4,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    connectionTimeout,
		ResponseHeaderTimeout:  timeout,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
	}
}

func (security *Security) Close() error {
	if security == nil {
		return nil
	}
	security.closeOnce.Do(func() { closeIdleConnections(security.transport) })
	return nil
}

func closeIdleConnections(transport http.RoundTripper) {
	if closer, ok := transport.(interface{ CloseIdleConnections() }); ok && !isNil(closer) {
		closer.CloseIdleConnections()
	}
}

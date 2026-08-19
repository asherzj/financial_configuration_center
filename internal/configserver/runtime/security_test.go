package runtime

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	platformconfig "github.com/asherzj/financial_configuration_center/internal/platform/config"
	"github.com/asherzj/financial_configuration_center/internal/platform/rpcauth"
)

func TestNewProductionSecurityComposesBothJWTProfiles(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	internalPublic, internalPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	consumerPrivate, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	security, err := newProductionSecurity(validSecurityConfig(), securityDependencies{
		clock: func() time.Time { return now },
		loadKeyRing: func(files map[string]string) (platformauth.StaticKeys, error) {
			if files["internal-key"] != "/run/secrets/internal.pem" {
				t.Fatalf("unexpected key files %v", files)
			}
			return platformauth.StaticKeys{"internal-key": append(ed25519.PublicKey(nil), internalPublic...)}, nil
		},
		httpTransport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.String() != "https://identity.example.com/jwks" || request.Header.Get("Authorization") != "" {
				t.Fatalf("unsafe JWKS request = %s, Authorization=%q", request.URL, request.Header.Get("Authorization"))
			}
			body := jwksDocument(t, "consumer-key", &consumerPrivate.PublicKey)
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer security.Close()
	if security.Authenticator == nil || security.Authorizer == nil {
		t.Fatal("runtime security did not expose both composition components")
	}
	if options, err := rpcauth.KitexServerOptions(security.Authenticator); err != nil || len(options) != 3 {
		t.Fatalf("Kitex auth options = %d, %v", len(options), err)
	}

	consumerToken := signConsumerJWT(t, "consumer-key", consumerPrivate, map[string]any{
		"iss": "https://identity.example.com", "aud": []string{"another-api", "config-api"},
		"sub": "payments", "jti": "consumer-jti", "iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(time.Minute).Unix(),
		"scopes": []map[string]string{{"region": "cn", "environment": "production", "stage": "*"}},
	})
	consumerIdentity, err := security.consumerVerifier.Verify(t.Context(), consumerToken)
	if err != nil || consumerIdentity.ConsumerID != "payments" {
		t.Fatalf("Consumer identity = %+v, %v", consumerIdentity, err)
	}
	internalToken, err := platformauth.SignJWT("internal-key", internalPrivate, platformauth.Claims{
		Issuer: "control-plane", Audience: "config-api", Subject: "relay-a", JWTID: "internal-jti",
		Roles: []string{"CONFIG_VIEWER"}, Scopes: []platformauth.ScopePattern{{Region: "*", Environment: "production", Stage: "*"}},
		IssuedAt: now.Unix(), NotBefore: now.Unix(), ExpiresAt: now.Add(60 * time.Second).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	internalIdentity, err := security.internalVerifier.Verify(t.Context(), internalToken)
	if err != nil || internalIdentity.Subject != "relay-a" {
		t.Fatalf("Internal identity = %+v, %v", internalIdentity, err)
	}
}

func TestNewProductionSecurityBindsHardenedRuntimeDependencies(t *testing.T) {
	t.Parallel()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/internal.pem"
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	config := validSecurityConfig()
	config.InternalJWT.PublicKeyFiles["internal-key"] = path
	security, err := NewProductionSecurity(config)
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := security.transport.(*http.Transport)
	if !ok || transport == http.DefaultTransport || transport.TLSClientConfig == nil || transport.TLSClientConfig.MinVersion < tls.VersionTLS12 || transport.ResponseHeaderTimeout != config.ConsumerJWT.HTTPTimeout.Duration {
		t.Fatalf("production JWKS transport is not hardened: %#v", security.transport)
	}
	if err := security.Close(); err != nil {
		t.Fatal(err)
	}
	if err := security.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNewProductionSecurityFailsBeforeServing(t *testing.T) {
	t.Parallel()
	config := validSecurityConfig()
	config.DevAuthEnabled = true
	if _, err := NewProductionSecurity(config); err == nil {
		t.Fatal("development authentication entered production security factory")
	}
	config = validSecurityConfig()
	if _, err := newProductionSecurity(config, securityDependencies{loadKeyRing: func(map[string]string) (platformauth.StaticKeys, error) {
		return nil, errors.New("unavailable")
	}}); err == nil || strings.Contains(err.Error(), "/run/secrets/internal.pem") {
		t.Fatalf("mounted key read failure = %v", err)
	}
	var typedNilTransport *nilRoundTripper
	if _, err := newProductionSecurity(validSecurityConfig(), securityDependencies{httpTransport: typedNilTransport}); err == nil {
		t.Fatal("typed-nil HTTP transport was accepted")
	}
}

func TestProductionSecurityJWKSClientEnforcesConfiguredTimeout(t *testing.T) {
	t.Parallel()
	internalPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	consumerPrivate, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	config := validSecurityConfig()
	config.ConsumerJWT.HTTPTimeout.Duration = 20 * time.Millisecond
	security, err := newProductionSecurity(config, securityDependencies{
		loadKeyRing: func(map[string]string) (platformauth.StaticKeys, error) {
			return platformauth.StaticKeys{"internal-key": append(ed25519.PublicKey(nil), internalPublic...)}, nil
		},
		httpTransport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	token := signConsumerJWT(t, "consumer-key", consumerPrivate, map[string]any{
		"iss": "https://identity.example.com", "aud": "config-api", "sub": "payments", "jti": "jti",
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(time.Minute).Unix(), "scopes": []any{},
	})
	started := time.Now()
	_, err = security.consumerVerifier.Verify(context.Background(), token)
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("bounded JWKS fetch returned after %s with %v", time.Since(started), err)
	}
}

func TestProductionSecurityJWKSClientRejectsRedirect(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	internalPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	consumerPrivate, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	security, err := newProductionSecurity(validSecurityConfig(), securityDependencies{
		clock: func() time.Time { return now },
		loadKeyRing: func(map[string]string) (platformauth.StaticKeys, error) {
			return platformauth.StaticKeys{"internal-key": append(ed25519.PublicKey(nil), internalPublic...)}, nil
		},
		httpTransport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"https://attacker.example.com/jwks"}},
				Body:       io.NopCloser(strings.NewReader("")), Request: request,
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	token := signConsumerJWT(t, "consumer-key", consumerPrivate, map[string]any{
		"iss": "https://identity.example.com", "aud": "config-api", "sub": "payments", "jti": "jti",
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(time.Minute).Unix(), "scopes": []any{},
	})
	if _, err := security.consumerVerifier.Verify(t.Context(), token); err == nil || requests != 1 {
		t.Fatalf("JWKS redirect result = %v after %d requests", err, requests)
	}
}

func validSecurityConfig() platformconfig.AuthConfig {
	return platformconfig.AuthConfig{
		ConsumerJWT: platformconfig.ConsumerJWTConfig{
			Issuer: "https://identity.example.com", Audience: "config-api", JWKSURL: "https://identity.example.com/jwks",
			JWKSCacheTTL: platformconfig.Duration{Duration: 5 * time.Minute}, HTTPTimeout: platformconfig.Duration{Duration: time.Second},
		},
		InternalJWT: platformconfig.InternalJWTConfig{
			Issuer: "control-plane", Audience: "config-api", PublicKeyFiles: map[string]string{"internal-key": "/run/secrets/internal.pem"},
		},
		RefreshRelaySubjects: []string{"relay-a"}, AdditionalPageQueryRoles: []string{"CONFIG_ADMIN"},
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type nilRoundTripper struct{}

func (*nilRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	panic("typed-nil transport must be rejected before use")
}

func jwksDocument(t *testing.T, keyID string, key *rsa.PublicKey) string {
	t.Helper()
	exponent := big.NewInt(int64(key.E)).Bytes()
	document, err := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": keyID,
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(exponent),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(document)
}

func signConsumerJWT(t *testing.T, keyID string, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": keyID})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

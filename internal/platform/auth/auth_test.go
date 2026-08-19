package auth_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/platform/auth"
)

func TestScopePatternsOnlyAllowWholeSegmentWildcards(t *testing.T) {
	t.Parallel()
	pattern, err := auth.CompileScopePattern("cn", "production", "*")
	if err != nil || !pattern.Matches(auth.Scope{Region: "cn", Environment: "production", Stage: "blue"}) || pattern.Matches(auth.Scope{Region: "us", Environment: "production", Stage: "blue"}) {
		t.Fatalf("pattern=%+v err=%v", pattern, err)
	}
	for _, invalid := range []string{"prod*", "*duction", "prod?"} {
		if _, err := auth.CompileScopePattern("cn", invalid, "*"); err == nil {
			t.Fatalf("partial wildcard %q accepted", invalid)
		}
	}
}

func TestEd25519JWTValidatesIssuerAudienceAndInternalLifetime(t *testing.T) {
	t.Parallel()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	claims := auth.Claims{Issuer: "control-plane", Audience: "config-server", Subject: "control-plane-relay", JWTID: "jti", IssuedAt: now.Unix(), NotBefore: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(), Roles: []string{"CONFIG_VIEWER"}, Scopes: []auth.ScopePattern{{Region: "cn", Environment: "production", Stage: "*"}}}
	token, err := auth.SignJWT("key-a", privateKey, claims)
	if err != nil {
		t.Fatal(err)
	}
	verifier, _ := auth.NewInternalJWTVerifier(auth.StaticKeys{"key-a": publicKey}, "control-plane", "config-server", func() time.Time { return now })
	verified, err := verifier.Verify(context.Background(), token)
	if err != nil || verified.Subject != "control-plane-relay" {
		t.Fatalf("verified=%+v err=%v", verified, err)
	}
	expiredVerifier, _ := auth.NewInternalJWTVerifier(auth.StaticKeys{"key-a": publicKey}, "control-plane", "config-server", func() time.Time { return now.Add(2 * time.Minute) })
	if _, err := expiredVerifier.Verify(context.Background(), token); !errors.Is(err, auth.ErrTokenExpired) {
		t.Fatalf("expired token=%v", err)
	}
}

func TestEd25519InternalJWTRejectsLifetimeOverSixtySeconds(t *testing.T) {
	t.Parallel()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	token, err := auth.SignJWT("key-a", privateKey, auth.Claims{
		Issuer: "control-plane", Audience: "config-server", Subject: "control-plane-relay", JWTID: "long-lived",
		IssuedAt: now.Unix(), NotBefore: now.Unix(), ExpiresAt: now.Add(61 * time.Second).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := auth.NewInternalJWTVerifier(auth.StaticKeys{"key-a": publicKey}, "control-plane", "config-server", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), token); !errors.Is(err, auth.ErrTokenInvalid) {
		t.Fatalf("long-lived internal token error = %v", err)
	}
}

func TestEd25519InternalJWTRejectsOverflowingLifetime(t *testing.T) {
	t.Parallel()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	token, err := auth.SignJWT("key-a", privateKey, auth.Claims{
		Issuer: "control-plane", Audience: "config-server", Subject: "control-plane-relay", JWTID: "overflow",
		IssuedAt: math.MinInt64, NotBefore: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := auth.NewInternalJWTVerifier(auth.StaticKeys{"key-a": publicKey}, "control-plane", "config-server", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), token); !errors.Is(err, auth.ErrTokenInvalid) {
		t.Fatalf("overflowing internal token error = %v", err)
	}
}

func TestSessionRotationAndCSRFAreBoundToSessionAndOrigin(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	oldKey, newKey, csrfKey := make([]byte, 32), make([]byte, 32), make([]byte, 32)
	for index := range oldKey {
		oldKey[index], newKey[index], csrfKey[index] = 1, 2, 3
	}
	oldCodec, _ := auth.NewSessionCodec("old", map[string][]byte{"old": oldKey}, func() time.Time { return now })
	cookie, err := oldCodec.Seal(auth.Session{SessionID: "session-a", Subject: "admin", AuthTime: now, ExpiresAt: now.Add(30 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	rotated, _ := auth.NewSessionCodec("new", map[string][]byte{"old": oldKey, "new": newKey}, func() time.Time { return now })
	if session, err := rotated.Open(cookie); err != nil || session.Subject != "admin" {
		t.Fatalf("session=%+v err=%v", session, err)
	}
	token, _ := auth.CSRFToken(csrfKey, "session-a")
	if err := auth.ValidateCSRF(csrfKey, "session-a", token, token, "https://admin.example.com", []string{"https://admin.example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.ValidateCSRF(csrfKey, "session-b", token, token, "https://admin.example.com", []string{"https://admin.example.com"}); err == nil {
		t.Fatal("cross-session CSRF token accepted")
	}
	if err := auth.ValidateCSRF(csrfKey, "session-a", token, token, "https://admin.example.com.evil.test", []string{"https://admin.example.com"}); err == nil {
		t.Fatal("lookalike origin accepted")
	}
}

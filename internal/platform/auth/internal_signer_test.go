package auth

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
)

func TestInternalJWTSignerProducesBoundedVerifiableIdentity(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	privateKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	signer, err := newInternalJWTSigner("key-a", privateKey, "admin-bff", "control-plane", func() time.Time { return now }, func() (string, error) { return "jti-1", nil })
	if err != nil {
		t.Fatal(err)
	}
	identity := InternalSigningIdentity{
		Subject: "operator", DisplayName: "Operator", Roles: []string{"SENSITIVE_VIEWER"},
		Scopes: []ScopePattern{{Region: "cn", Environment: "production", Stage: "*"}},
	}
	token, err := signer.Sign(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	identity.Roles[0] = "mutated"
	identity.Scopes[0].Environment = "mutated"
	verifier, err := NewInternalJWTVerifier(StaticKeys{"key-a": privateKey.Public().(ed25519.PublicKey)}, "admin-bff", "control-plane", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifier.Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Subject != "operator" || verified.DisplayName != "Operator" || verified.JWTID != "jti-1" || len(verified.Roles) != 1 || verified.Roles[0] != "SENSITIVE_VIEWER" ||
		len(verified.Scopes) != 1 || verified.Scopes[0].Environment != "production" || !verified.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("identity = %+v", verified)
	}
}

func TestInternalJWTSignerFailsClosedForInvalidIdentityAndJTI(t *testing.T) {
	t.Parallel()
	privateKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	signer, _ := newInternalJWTSigner("key-a", privateKey, "admin-bff", "control-plane", time.Now, func() (string, error) { return "", errors.New("entropy unavailable") })
	if _, err := signer.Sign(context.Background(), InternalSigningIdentity{Subject: "operator"}); err == nil {
		t.Fatal("expected JTI failure")
	}
	valid, _ := NewInternalJWTSigner("key-a", privateKey, "admin-bff", "control-plane")
	for name, identity := range map[string]InternalSigningIdentity{
		"subject":      {Subject: " operator"},
		"display name": {Subject: "operator", DisplayName: "Operator\nInjected"},
		"role":         {Subject: "operator", Roles: []string{"SENSITIVE_VIEWER", "SENSITIVE_VIEWER"}},
		"scope":        {Subject: "operator", Scopes: []ScopePattern{{Region: "c*", Environment: "production", Stage: "*"}}},
	} {
		if _, err := valid.Sign(context.Background(), identity); err == nil {
			t.Fatalf("%s identity accepted", name)
		}
	}
}

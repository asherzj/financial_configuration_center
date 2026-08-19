package auth_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/platform/auth"
)

func TestOIDCVerifierValidatesRS256AudienceAndNonce(t *testing.T) {
	t.Parallel()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	token := signOIDCToken(t, privateKey, "key-a", map[string]any{
		"iss": "https://identity.example.com", "aud": []string{"finconfig", "profile"}, "azp": "finconfig",
		"sub": "user-a", "name": "User A", "nonce": "nonce-a", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(),
		"roles": []string{"CONFIG_ADMIN"},
	})
	verifier, err := auth.NewOIDCVerifier(auth.StaticRSAKeys{"key-a": &privateKey.PublicKey}, "https://identity.example.com", "finconfig", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	identity, err := verifier.VerifyIDToken(context.Background(), token, "nonce-a")
	if err != nil || identity.Subject != "user-a" || identity.DisplayName != "User A" {
		t.Fatalf("identity=%+v err=%v", identity, err)
	}
	if _, err := verifier.VerifyIDToken(context.Background(), token, "other-nonce"); !errors.Is(err, auth.ErrTokenInvalid) {
		t.Fatalf("nonce substitution=%v", err)
	}
	otherAudience, _ := auth.NewOIDCVerifier(auth.StaticRSAKeys{"key-a": &privateKey.PublicKey}, "https://identity.example.com", "other", func() time.Time { return now })
	if _, err := otherAudience.VerifyIDToken(context.Background(), token, "nonce-a"); !errors.Is(err, auth.ErrTokenInvalid) {
		t.Fatalf("audience substitution=%v", err)
	}
}

func signOIDCToken(t *testing.T, privateKey *rsa.PrivateKey, keyID string, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": keyID})
	payload, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

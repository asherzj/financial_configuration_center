package auth_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/platform/auth"
)

func TestConsumerJWTVerifiesRS256AudienceArrayWithoutClientIDClaim(t *testing.T) {
	t.Parallel()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	token := signConsumerJWT(t, "consumer-key", privateKey, map[string]any{
		"iss": "https://identity.example.com", "aud": []string{"another-service", "config-server"},
		"sub": "payment-service", "jti": "consumer-jti", "iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		"scopes": []map[string]string{{"region": "cn", "environment": "production", "stage": "*"}},
	})
	verifier, err := auth.NewConsumerJWTVerifier(
		auth.StaticRSAKeys{"consumer-key": &privateKey.PublicKey},
		"https://identity.example.com", "config-server", func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := verifier.Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if identity.ConsumerID != "payment-service" || identity.JWTID != "consumer-jti" || len(identity.Scopes) != 1 {
		t.Fatalf("consumer identity = %+v", identity)
	}
}

func TestConsumerJWTVerifierComposesWithRemoteJWKS(t *testing.T) {
	t.Parallel()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"keys": []any{rsaJWK("consumer-key", &privateKey.PublicKey)}})
	}))
	defer server.Close()
	keys, err := auth.NewRemoteJWKS(server.URL, server.Client(), time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := auth.NewConsumerJWTVerifier(keys, "https://identity.example.com", "config-server", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	token := signConsumerJWT(t, "consumer-key", privateKey, map[string]any{
		"iss": "https://identity.example.com", "aud": "config-server", "sub": "payment-service", "jti": "consumer-jti",
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		"scopes": []map[string]string{{"region": "cn", "environment": "production", "stage": "*"}},
	})
	if identity, err := verifier.Verify(context.Background(), token); err != nil || identity.ConsumerID != "payment-service" {
		t.Fatalf("consumer identity=%+v err=%v", identity, err)
	}
}

func signConsumerJWT(t *testing.T, keyID string, privateKey *rsa.PrivateKey, claims map[string]any) string {
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
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

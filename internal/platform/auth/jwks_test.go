package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/platform/auth"
)

func TestRemoteJWKSLoadsAndRefreshesBoundedRSAKeys(t *testing.T) {
	t.Parallel()
	first, _ := rsa.GenerateKey(rand.Reader, 2048)
	second, _ := rsa.GenerateKey(rand.Reader, 2048)
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	var mutex sync.Mutex
	activeID, activeKey, requests := "first", &first.PublicKey, 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		requests++
		_ = json.NewEncoder(writer).Encode(map[string]any{"keys": []any{rsaJWK(activeID, activeKey)}})
	}))
	defer server.Close()
	keys, err := auth.NewRemoteJWKS(server.URL, server.Client(), time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := keys.ResolveRSA(t.Context(), "first")
	if err != nil || resolved.N.Cmp(first.N) != 0 {
		t.Fatalf("first key=%v err=%v", resolved, err)
	}
	mutex.Lock()
	activeID, activeKey = "second", &second.PublicKey
	mutex.Unlock()
	now = now.Add(2 * time.Minute)
	resolved, err = keys.ResolveRSA(t.Context(), "second")
	if err != nil || resolved.N.Cmp(second.N) != 0 {
		t.Fatalf("second key=%v err=%v", resolved, err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if requests != 2 {
		t.Fatalf("JWKS requests=%d", requests)
	}
}

func TestRemoteJWKSRejectsInsecureEndpoint(t *testing.T) {
	t.Parallel()
	if _, err := auth.NewRemoteJWKS("http://identity.example.com/jwks", http.DefaultClient, time.Minute, time.Now); err == nil {
		t.Fatal("insecure JWKS endpoint accepted")
	}
}

func rsaJWK(keyID string, key *rsa.PublicKey) map[string]string {
	exponent := big.NewInt(int64(key.E)).Bytes()
	return map[string]string{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": keyID,
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(exponent),
	}
}

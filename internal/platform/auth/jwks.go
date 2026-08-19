package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	maxJWKSBody = 1 << 20
	maxJWKSKeys = 100
)

type RemoteJWKS struct {
	endpoint string
	client   *http.Client
	ttl      time.Duration
	clock    func() time.Time

	mutex     sync.Mutex
	keys      map[string]*rsa.PublicKey
	expiresAt time.Time
}

func NewRemoteJWKS(endpoint string, client *http.Client, ttl time.Duration, clock func() time.Time) (*RemoteJWKS, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("JWKS endpoint must be an absolute HTTPS URL")
	}
	if client == nil || ttl <= 0 || clock == nil {
		return nil, errors.New("JWKS HTTP client, cache TTL, and clock are required")
	}
	return &RemoteJWKS{endpoint: endpoint, client: client, ttl: ttl, clock: clock, keys: make(map[string]*rsa.PublicKey)}, nil
}

func (remote *RemoteJWKS) ResolveRSA(ctx context.Context, keyID string) (*rsa.PublicKey, error) {
	if strings.TrimSpace(keyID) == "" {
		return nil, ErrTokenInvalid
	}
	remote.mutex.Lock()
	defer remote.mutex.Unlock()
	if len(remote.keys) == 0 || !remote.expiresAt.After(remote.clock()) {
		if err := remote.refresh(ctx); err != nil {
			return nil, err
		}
	}
	key := remote.keys[keyID]
	if key == nil {
		return nil, ErrTokenInvalid
	}
	return &rsa.PublicKey{N: new(big.Int).Set(key.N), E: key.E}, nil
}

func (remote *RemoteJWKS) refresh(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, remote.endpoint, nil)
	if err != nil {
		return ErrTokenInvalid
	}
	request.Header.Set("Accept", "application/json")
	response, err := remote.client.Do(request)
	if err != nil {
		return ErrTokenInvalid
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrTokenInvalid
	}
	var document struct {
		Keys []struct {
			KeyType   string `json:"kty"`
			Use       string `json:"use"`
			Algorithm string `json:"alg"`
			KeyID     string `json:"kid"`
			Modulus   string `json:"n"`
			Exponent  string `json:"e"`
		} `json:"keys"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxJWKSBody+1))
	if err := decoder.Decode(&document); err != nil || len(document.Keys) == 0 || len(document.Keys) > maxJWKSKeys {
		return ErrTokenInvalid
	}
	loaded := make(map[string]*rsa.PublicKey, len(document.Keys))
	for _, candidate := range document.Keys {
		if candidate.KeyType != "RSA" || candidate.Algorithm != "RS256" || candidate.Use != "sig" || candidate.KeyID == "" {
			continue
		}
		modulus, err := base64.RawURLEncoding.DecodeString(candidate.Modulus)
		if err != nil || len(modulus) < 256 || len(modulus) > 1024 {
			continue
		}
		exponentBytes, err := base64.RawURLEncoding.DecodeString(candidate.Exponent)
		if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 {
			continue
		}
		exponentValue := new(big.Int).SetBytes(exponentBytes)
		if !exponentValue.IsInt64() || exponentValue.Int64() < 3 || exponentValue.Int64() > 1<<31-1 {
			continue
		}
		if _, duplicate := loaded[candidate.KeyID]; duplicate {
			return ErrTokenInvalid
		}
		loaded[candidate.KeyID] = &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: int(exponentValue.Int64())}
	}
	if len(loaded) == 0 {
		return ErrTokenInvalid
	}
	remote.keys = loaded
	remote.expiresAt = remote.clock().Add(remote.ttl)
	return nil
}

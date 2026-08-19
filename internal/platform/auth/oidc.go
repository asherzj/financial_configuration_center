package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"
)

type OIDCIdentity struct {
	Subject     string
	DisplayName string
	Nonce       string
	AuthTime    time.Time
	Claims      map[string]json.RawMessage
}

type RSAKeyResolver interface {
	ResolveRSA(context.Context, string) (*rsa.PublicKey, error)
}

type StaticRSAKeys map[string]*rsa.PublicKey

func (keys StaticRSAKeys) ResolveRSA(_ context.Context, keyID string) (*rsa.PublicKey, error) {
	key := keys[keyID]
	if key == nil || key.N == nil || key.E < 3 {
		return nil, ErrTokenInvalid
	}
	return &rsa.PublicKey{N: new(big.Int).Set(key.N), E: key.E}, nil
}

type OIDCVerifier struct {
	keys     RSAKeyResolver
	issuer   string
	audience string
	clock    func() time.Time
	leeway   time.Duration
}

func NewOIDCVerifier(keys RSAKeyResolver, issuer, audience string, clock func() time.Time) (*OIDCVerifier, error) {
	if keys == nil || !strings.HasPrefix(issuer, "https://") || strings.TrimSpace(audience) == "" || clock == nil {
		return nil, errors.New("OIDC verifier requires keys, HTTPS issuer, audience, and clock")
	}
	return &OIDCVerifier{keys: keys, issuer: issuer, audience: audience, clock: clock, leeway: 5 * time.Second}, nil
}

func (verifier *OIDCVerifier) VerifyIDToken(ctx context.Context, token, expectedNonce string) (OIDCIdentity, error) {
	if len(token) > 64<<10 {
		return OIDCIdentity{}, ErrTokenInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || expectedNonce == "" {
		return OIDCIdentity{}, ErrTokenInvalid
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	if err := decodeJSONSegment(parts[0], &header); err != nil || header.Algorithm != "RS256" || header.KeyID == "" {
		return OIDCIdentity{}, ErrTokenInvalid
	}
	key, err := verifier.keys.ResolveRSA(ctx, header.KeyID)
	if err != nil {
		return OIDCIdentity{}, ErrTokenInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return OIDCIdentity{}, ErrTokenInvalid
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return OIDCIdentity{}, ErrTokenInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return OIDCIdentity{}, ErrTokenInvalid
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil {
		return OIDCIdentity{}, ErrTokenInvalid
	}
	var claims struct {
		Issuer      string   `json:"iss"`
		Audience    audience `json:"aud"`
		Authorized  string   `json:"azp"`
		Subject     string   `json:"sub"`
		DisplayName string   `json:"name"`
		Nonce       string   `json:"nonce"`
		IssuedAt    int64    `json:"iat"`
		ExpiresAt   int64    `json:"exp"`
		AuthTime    int64    `json:"auth_time"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return OIDCIdentity{}, ErrTokenInvalid
	}
	if claims.Issuer != verifier.issuer || claims.Subject == "" || claims.IssuedAt == 0 || claims.ExpiresAt == 0 || !claims.Audience.contains(verifier.audience) || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(expectedNonce)) != 1 {
		return OIDCIdentity{}, ErrTokenInvalid
	}
	if len(claims.Audience) > 1 && claims.Authorized != verifier.audience {
		return OIDCIdentity{}, ErrTokenInvalid
	}
	now := verifier.clock().UTC()
	if now.After(time.Unix(claims.ExpiresAt, 0).Add(verifier.leeway)) {
		return OIDCIdentity{}, ErrTokenExpired
	}
	if time.Unix(claims.IssuedAt, 0).After(now.Add(verifier.leeway)) {
		return OIDCIdentity{}, ErrTokenInvalid
	}
	identity := OIDCIdentity{Subject: claims.Subject, DisplayName: claims.DisplayName, Nonce: claims.Nonce, Claims: raw}
	if claims.AuthTime > 0 {
		identity.AuthTime = time.Unix(claims.AuthTime, 0).UTC()
	}
	return identity, nil
}

type audience []string

func (value *audience) UnmarshalJSON(data []byte) error {
	var one string
	if json.Unmarshal(data, &one) == nil {
		*value = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil || len(many) == 0 {
		return ErrTokenInvalid
	}
	*value = many
	return nil
}

func (value audience) contains(expected string) bool {
	for _, candidate := range value {
		if candidate == expected {
			return true
		}
	}
	return false
}

func decodeJSONSegment(segment string, target any) error {
	decoded, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return err
	}
	return json.Unmarshal(decoded, target)
}

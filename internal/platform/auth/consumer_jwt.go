package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxConsumerJWTBytes = 16 << 10

type ConsumerIdentity struct {
	ConsumerID string
	JWTID      string
	Scopes     []ScopePattern
	IssuedAt   time.Time
	ExpiresAt  time.Time
}

type ConsumerJWTVerifier struct {
	keys     RSAKeyResolver
	issuer   string
	audience string
	clock    func() time.Time
	leeway   time.Duration
}

func NewConsumerJWTVerifier(keys RSAKeyResolver, issuer, audience string, clock func() time.Time) (*ConsumerJWTVerifier, error) {
	if keys == nil || !strings.HasPrefix(issuer, "https://") || strings.TrimSpace(audience) == "" || clock == nil {
		return nil, errors.New("Consumer JWT verifier requires keys, HTTPS issuer, audience, and clock")
	}
	return &ConsumerJWTVerifier{keys: keys, issuer: issuer, audience: audience, clock: clock, leeway: 5 * time.Second}, nil
}

func (verifier *ConsumerJWTVerifier) Verify(ctx context.Context, token string) (ConsumerIdentity, error) {
	if len(token) == 0 || len(token) > maxConsumerJWTBytes {
		return ConsumerIdentity{}, ErrTokenInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ConsumerIdentity{}, ErrTokenInvalid
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
		KeyID     string `json:"kid"`
	}
	if err := decodeJSONSegment(parts[0], &header); err != nil || header.Algorithm != "RS256" || header.Type != "JWT" || header.KeyID == "" {
		return ConsumerIdentity{}, ErrTokenInvalid
	}
	key, err := verifier.keys.ResolveRSA(ctx, header.KeyID)
	if err != nil {
		return ConsumerIdentity{}, fmt.Errorf("%w: signing key unavailable", ErrTokenInvalid)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return ConsumerIdentity{}, ErrTokenInvalid
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return ConsumerIdentity{}, ErrTokenInvalid
	}
	var claims struct {
		Issuer    string         `json:"iss"`
		Audience  audience       `json:"aud"`
		Subject   string         `json:"sub"`
		JWTID     string         `json:"jti"`
		IssuedAt  int64          `json:"iat"`
		NotBefore int64          `json:"nbf"`
		ExpiresAt int64          `json:"exp"`
		Scopes    []ScopePattern `json:"scopes"`
	}
	if err := decodeJSONSegment(parts[1], &claims); err != nil {
		return ConsumerIdentity{}, ErrTokenInvalid
	}
	if claims.Issuer != verifier.issuer || !claims.Audience.contains(verifier.audience) || claims.Subject == "" || claims.Subject != strings.TrimSpace(claims.Subject) || claims.JWTID == "" || claims.IssuedAt == 0 || claims.NotBefore == 0 || claims.ExpiresAt == 0 || claims.ExpiresAt <= claims.IssuedAt {
		return ConsumerIdentity{}, ErrTokenInvalid
	}
	now := verifier.clock().UTC()
	if now.After(time.Unix(claims.ExpiresAt, 0).Add(verifier.leeway)) {
		return ConsumerIdentity{}, ErrTokenExpired
	}
	if now.Add(verifier.leeway).Before(time.Unix(claims.NotBefore, 0)) || time.Unix(claims.IssuedAt, 0).After(now.Add(verifier.leeway)) {
		return ConsumerIdentity{}, ErrTokenInvalid
	}
	for _, pattern := range claims.Scopes {
		if _, err := CompileScopePattern(pattern.Region, pattern.Environment, pattern.Stage); err != nil {
			return ConsumerIdentity{}, ErrTokenInvalid
		}
	}
	return ConsumerIdentity{
		ConsumerID: claims.Subject,
		JWTID:      claims.JWTID,
		Scopes:     append([]ScopePattern(nil), claims.Scopes...),
		IssuedAt:   time.Unix(claims.IssuedAt, 0).UTC(),
		ExpiresAt:  time.Unix(claims.ExpiresAt, 0).UTC(),
	}, nil
}

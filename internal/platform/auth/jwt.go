package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var (
	ErrTokenInvalid = errors.New("token is invalid")
	ErrTokenExpired = errors.New("token is expired")
)

type Claims struct {
	Issuer    string         `json:"iss"`
	Audience  string         `json:"aud"`
	Subject   string         `json:"sub"`
	Roles     []string       `json:"roles,omitempty"`
	Scopes    []ScopePattern `json:"scopes,omitempty"`
	JWTID     string         `json:"jti"`
	IssuedAt  int64          `json:"iat"`
	NotBefore int64          `json:"nbf"`
	ExpiresAt int64          `json:"exp"`
}

type InternalCallerIdentity struct {
	Subject   string
	JWTID     string
	Roles     []string
	Scopes    []ScopePattern
	IssuedAt  time.Time
	ExpiresAt time.Time
}

type KeyResolver interface {
	Resolve(context.Context, string) (ed25519.PublicKey, error)
}

type StaticKeys map[string]ed25519.PublicKey

func (keys StaticKeys) Resolve(_ context.Context, keyID string) (ed25519.PublicKey, error) {
	key := keys[keyID]
	if len(key) != ed25519.PublicKeySize {
		return nil, ErrTokenInvalid
	}
	return append(ed25519.PublicKey(nil), key...), nil
}

type InternalJWTVerifier struct {
	keys     KeyResolver
	issuer   string
	audience string
	clock    func() time.Time
	leeway   time.Duration
}

func NewInternalJWTVerifier(keys KeyResolver, issuer, audience string, clock func() time.Time) (*InternalJWTVerifier, error) {
	if keys == nil || strings.TrimSpace(issuer) == "" || strings.TrimSpace(audience) == "" || clock == nil {
		return nil, errors.New("Internal JWT verifier keys, issuer, audience, and clock are required")
	}
	return &InternalJWTVerifier{keys: keys, issuer: issuer, audience: audience, clock: clock, leeway: 5 * time.Second}, nil
}

func (verifier *InternalJWTVerifier) verify(ctx context.Context, token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrTokenInvalid
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
		KeyID     string `json:"kid"`
	}
	if err := decodeSegment(parts[0], &header); err != nil || header.Algorithm != "EdDSA" || header.Type != "JWT" || header.KeyID == "" {
		return Claims{}, ErrTokenInvalid
	}
	key, err := verifier.keys.Resolve(ctx, header.KeyID)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: signing key unavailable", ErrTokenInvalid)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(key, []byte(parts[0]+"."+parts[1]), signature) {
		return Claims{}, ErrTokenInvalid
	}
	var claims Claims
	if err := decodeSegment(parts[1], &claims); err != nil {
		return Claims{}, ErrTokenInvalid
	}
	now := verifier.clock().UTC()
	if claims.Issuer != verifier.issuer || claims.Audience != verifier.audience || claims.Subject == "" || claims.JWTID == "" || claims.ExpiresAt == 0 || claims.IssuedAt == 0 || claims.NotBefore == 0 {
		return Claims{}, ErrTokenInvalid
	}
	if now.After(time.Unix(claims.ExpiresAt, 0).Add(verifier.leeway)) {
		return Claims{}, ErrTokenExpired
	}
	if now.Add(verifier.leeway).Before(time.Unix(claims.NotBefore, 0)) || time.Unix(claims.IssuedAt, 0).After(now.Add(verifier.leeway)) {
		return Claims{}, ErrTokenInvalid
	}
	for _, pattern := range claims.Scopes {
		if _, err := CompileScopePattern(pattern.Region, pattern.Environment, pattern.Stage); err != nil {
			return Claims{}, ErrTokenInvalid
		}
	}
	return claims, nil
}

func (verifier *InternalJWTVerifier) Verify(ctx context.Context, token string) (InternalCallerIdentity, error) {
	claims, err := verifier.verify(ctx, token)
	if err != nil {
		return InternalCallerIdentity{}, err
	}
	if claims.IssuedAt < 0 || claims.ExpiresAt <= claims.IssuedAt || claims.ExpiresAt-claims.IssuedAt > 60 {
		return InternalCallerIdentity{}, ErrTokenInvalid
	}
	return InternalCallerIdentity{
		Subject: claims.Subject, JWTID: claims.JWTID,
		Roles: append([]string(nil), claims.Roles...), Scopes: append([]ScopePattern(nil), claims.Scopes...),
		IssuedAt: time.Unix(claims.IssuedAt, 0).UTC(), ExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(),
	}, nil
}

func SignJWT(keyID string, privateKey ed25519.PrivateKey, claims Claims) (string, error) {
	if keyID == "" || len(privateKey) != ed25519.PrivateKeySize {
		return "", errors.New("JWT signing key is invalid")
	}
	header, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": keyID})
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	signature := ed25519.Sign(privateKey, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func decodeSegment(segment string, target any) error {
	data, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ErrTokenInvalid
	}
	return nil
}

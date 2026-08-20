package auth

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const internalJWTLifetime = 60 * time.Second

type InternalSigningIdentity struct {
	Subject     string
	DisplayName string
	Roles       []string
	Scopes      []ScopePattern
}

type InternalJWTSigner struct {
	keyID      string
	privateKey ed25519.PrivateKey
	issuer     string
	audience   string
	clock      func() time.Time
	jti        func() (string, error)
}

func NewInternalJWTSigner(keyID string, privateKey ed25519.PrivateKey, issuer, audience string) (*InternalJWTSigner, error) {
	return newInternalJWTSigner(keyID, privateKey, issuer, audience, time.Now, func() (string, error) {
		value, err := uuid.NewV7()
		return value.String(), err
	})
}

func newInternalJWTSigner(keyID string, privateKey ed25519.PrivateKey, issuer, audience string, clock func() time.Time, jti func() (string, error)) (*InternalJWTSigner, error) {
	if keyID == "" || keyID != strings.TrimSpace(keyID) || len(privateKey) != ed25519.PrivateKeySize ||
		issuer == "" || issuer != strings.TrimSpace(issuer) || audience == "" || audience != strings.TrimSpace(audience) || clock == nil || jti == nil {
		return nil, errors.New("Internal JWT signer key, issuer, audience, clock, and JTI generator are required")
	}
	return &InternalJWTSigner{
		keyID: keyID, privateKey: append(ed25519.PrivateKey(nil), privateKey...), issuer: issuer, audience: audience, clock: clock, jti: jti,
	}, nil
}

func (signer *InternalJWTSigner) Sign(ctx context.Context, identity InternalSigningIdentity) (string, error) {
	if signer == nil || ctx == nil {
		return "", errors.New("Internal JWT signer and context are required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if identity.Subject == "" || identity.Subject != strings.TrimSpace(identity.Subject) {
		return "", errors.New("Internal JWT subject is invalid")
	}
	if !validInternalDisplayName(identity.DisplayName) {
		return "", errors.New("Internal JWT display name is invalid")
	}
	roles, err := exactSigningValues(identity.Roles)
	if err != nil {
		return "", err
	}
	scopes := make([]ScopePattern, len(identity.Scopes))
	for index, scope := range identity.Scopes {
		compiled, err := CompileScopePattern(scope.Region, scope.Environment, scope.Stage)
		if err != nil {
			return "", errors.New("Internal JWT scope is invalid")
		}
		scopes[index] = compiled
	}
	jti, err := signer.jti()
	if err != nil || jti == "" || jti != strings.TrimSpace(jti) {
		return "", errors.New("generate Internal JWT ID")
	}
	now := signer.clock().UTC().Truncate(time.Second)
	return SignJWT(signer.keyID, signer.privateKey, Claims{
		Issuer: signer.issuer, Audience: signer.audience, Subject: identity.Subject, DisplayName: identity.DisplayName,
		Roles: roles, Scopes: scopes, JWTID: jti,
		IssuedAt: now.Unix(), NotBefore: now.Unix(), ExpiresAt: now.Add(internalJWTLifetime).Unix(),
	})
}

func validInternalDisplayName(value string) bool {
	return len(value) <= 256 && value == strings.TrimSpace(value) && !strings.ContainsAny(value, "\r\n")
}

func exactSigningValues(values []string) ([]string, error) {
	result := make([]string, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if value == "" || value != strings.TrimSpace(value) {
			return nil, errors.New("Internal JWT role is invalid")
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, errors.New("Internal JWT role is duplicated")
		}
		seen[value] = struct{}{}
		result[index] = value
	}
	return result, nil
}

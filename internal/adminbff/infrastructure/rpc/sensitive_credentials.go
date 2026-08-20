package rpc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	bffapp "github.com/asherzj/financial_configuration_center/internal/adminbff/application"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	kitexmetadata "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/metadata"
)

type InternalTokenSigner interface {
	Sign(context.Context, platformauth.InternalSigningIdentity) (string, error)
}

type SensitiveJWTCredentialAttacher struct{ signer InternalTokenSigner }

func NewSensitiveJWTCredentialAttacher(signer InternalTokenSigner) (*SensitiveJWTCredentialAttacher, error) {
	if signer == nil || isNil(signer) {
		return nil, errors.New("new sensitive JWT credential attacher: signer is required")
	}
	return &SensitiveJWTCredentialAttacher{signer: signer}, nil
}

func (attacher *SensitiveJWTCredentialAttacher) AttachSensitiveCredentials(ctx context.Context, principal bffapp.SensitivePrincipal) (context.Context, error) {
	if ctx == nil {
		return nil, errors.New("attach sensitive JWT credentials: context is required")
	}
	token, err := attacher.signer.Sign(ctx, platformauth.InternalSigningIdentity{
		Subject: principal.Subject, DisplayName: principal.DisplayName, Roles: append([]string(nil), principal.Roles...),
		Scopes: append([]platformauth.ScopePattern(nil), principal.AllowedScopes...),
	})
	if err != nil {
		return nil, fmt.Errorf("sign sensitive access JWT: %w", err)
	}
	if token == "" || token != strings.TrimSpace(token) || strings.ContainsAny(token, " \t\r\n") {
		return nil, errors.New("sign sensitive access JWT: signer returned an invalid token")
	}
	return kitexmetadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token), nil
}

var _ SensitiveCredentialAttacher = (*SensitiveJWTCredentialAttacher)(nil)

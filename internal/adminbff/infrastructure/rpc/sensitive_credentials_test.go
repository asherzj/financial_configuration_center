package rpc_test

import (
	"context"
	"errors"
	"testing"

	bffapp "github.com/asherzj/financial_configuration_center/internal/adminbff/application"
	bffrpc "github.com/asherzj/financial_configuration_center/internal/adminbff/infrastructure/rpc"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	kitexmetadata "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/metadata"
)

func TestSensitiveJWTCredentialAttacherSignsAuthenticatedPrincipal(t *testing.T) {
	t.Parallel()
	signer := &tokenSignerStub{token: "signed-token"}
	attacher, err := bffrpc.NewSensitiveJWTCredentialAttacher(signer)
	if err != nil {
		t.Fatal(err)
	}
	principal := bffapp.SensitivePrincipal{
		Subject: "operator", DisplayName: "Operator", Roles: []string{bffapp.SensitiveViewerRole},
		AllowedScopes: []platformauth.ScopePattern{{Region: "cn", Environment: "production", Stage: "*"}},
	}
	ctx, err := attacher.AttachSensitiveCredentials(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	metadata, ok := kitexmetadata.FromOutgoingContext(ctx)
	if !ok || first(metadata.Get("authorization")) != "Bearer signed-token" || signer.identity.Subject != "operator" || signer.identity.DisplayName != "Operator" || len(signer.identity.Scopes) != 1 {
		t.Fatalf("metadata=%v identity=%+v", metadata, signer.identity)
	}
	signer.identity.Roles[0] = "mutated"
	if principal.Roles[0] != bffapp.SensitiveViewerRole {
		t.Fatal("signer mutated caller principal")
	}
}

func TestSensitiveJWTCredentialAttacherFailsClosed(t *testing.T) {
	t.Parallel()
	var nilSigner *tokenSignerStub
	if _, err := bffrpc.NewSensitiveJWTCredentialAttacher(nilSigner); err == nil {
		t.Fatal("expected typed-nil signer rejection")
	}
	attacher, _ := bffrpc.NewSensitiveJWTCredentialAttacher(&tokenSignerStub{err: errors.New("signing failed")})
	if ctx, err := attacher.AttachSensitiveCredentials(context.Background(), bffapp.SensitivePrincipal{Subject: "operator"}); err == nil || ctx != nil {
		t.Fatalf("context=%v error=%v", ctx, err)
	}
}

type tokenSignerStub struct {
	token    string
	err      error
	identity platformauth.InternalSigningIdentity
}

func (stub *tokenSignerStub) Sign(_ context.Context, identity platformauth.InternalSigningIdentity) (string, error) {
	stub.identity = identity
	return stub.token, stub.err
}

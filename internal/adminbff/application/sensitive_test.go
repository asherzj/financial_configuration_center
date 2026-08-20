package application_test

import (
	"context"
	"errors"
	"testing"

	bffapp "github.com/asherzj/financial_configuration_center/internal/adminbff/application"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
)

func TestSensitiveAccessServiceAuthorizesRoleAndScopeBeforePort(t *testing.T) {
	t.Parallel()
	port := &sensitivePortStub{result: bffapp.RevealSensitiveResult{Value: "secret"}}
	service, err := bffapp.NewSensitiveAccessService(port)
	if err != nil {
		t.Fatal(err)
	}
	command := sensitiveCommand()
	result, err := service.Reveal(context.Background(), command)
	if err != nil || result.Value != "secret" || port.calls != 1 {
		t.Fatalf("result=%+v error=%v calls=%d", result, err, port.calls)
	}
	if port.command.Scope.Environment != "production" || port.command.Principal.Subject != "operator" {
		t.Fatalf("command = %+v", port.command)
	}
}

func TestSensitiveAccessServiceRejectsInvalidOrUnauthorizedRequestsBeforePort(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*bffapp.RevealSensitiveCommand){
		"blank subject": func(command *bffapp.RevealSensitiveCommand) { command.Principal.Subject = " " },
		"wildcard dto":  func(command *bffapp.RevealSensitiveCommand) { command.Scope.Environment = "*" },
		"missing role":  func(command *bffapp.RevealSensitiveCommand) { command.Principal.Roles = nil },
		"scope mismatch": func(command *bffapp.RevealSensitiveCommand) {
			command.Principal.AllowedScopes[0].Environment = "staging"
		},
	} {
		t.Run(name, func(t *testing.T) {
			port := &sensitivePortStub{}
			service, _ := bffapp.NewSensitiveAccessService(port)
			command := sensitiveCommand()
			mutate(&command)
			_, err := service.Reveal(context.Background(), command)
			if (name == "blank subject" || name == "wildcard dto") && !errors.Is(err, bffapp.ErrSensitiveInvalid) {
				t.Fatalf("error = %v", err)
			}
			if (name == "missing role" || name == "scope mismatch") && !errors.Is(err, bffapp.ErrSensitiveForbidden) {
				t.Fatalf("error = %v", err)
			}
			if port.calls != 0 {
				t.Fatalf("port calls = %d", port.calls)
			}
		})
	}
}

func TestSensitiveAccessServiceRejectsTypedNilPort(t *testing.T) {
	t.Parallel()
	var port *sensitivePortStub
	if _, err := bffapp.NewSensitiveAccessService(port); err == nil {
		t.Fatal("expected typed-nil port rejection")
	}
}

func sensitiveCommand() bffapp.RevealSensitiveCommand {
	return bffapp.RevealSensitiveCommand{
		Scope: bffapp.SensitiveScope{Region: "cn", Environment: "production", Stage: "blue"},
		Principal: bffapp.SensitivePrincipal{
			Subject: "operator", Roles: []string{bffapp.SensitiveViewerRole},
			AllowedScopes: []platformauth.ScopePattern{{Region: "cn", Environment: "production", Stage: "*"}},
		},
	}
}

type sensitivePortStub struct {
	result  bffapp.RevealSensitiveResult
	command bffapp.RevealSensitiveCommand
	calls   int
}

func (stub *sensitivePortStub) RevealField(_ context.Context, command bffapp.RevealSensitiveCommand) (bffapp.RevealSensitiveResult, error) {
	stub.calls++
	stub.command = command
	return stub.result, nil
}

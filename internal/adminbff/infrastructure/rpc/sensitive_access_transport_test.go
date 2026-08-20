package rpc_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/control/v1/sensitiveaccessservice"
	access "github.com/asherzj/financial_configuration_center/internal/access/application"
	accessgrpc "github.com/asherzj/financial_configuration_center/internal/access/grpc"
	bffapp "github.com/asherzj/financial_configuration_center/internal/adminbff/application"
	bffrpc "github.com/asherzj/financial_configuration_center/internal/adminbff/infrastructure/rpc"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	"github.com/asherzj/financial_configuration_center/internal/platform/rpcauth"
	"github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/server"
	"github.com/cloudwego/kitex/transport"
)

func TestSensitiveAccessPreservesAuthenticatedPrincipalAndTraceAcrossKitex(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	privateKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	verifier, err := platformauth.NewInternalJWTVerifier(
		platformauth.StaticKeys{"key-a": privateKey.Public().(ed25519.PublicKey)}, "admin-bff", "control-plane", time.Now,
	)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := rpcauth.New(rejectConsumerVerifier{}, verifier)
	if err != nil {
		t.Fatal(err)
	}
	requestAuthorizer, err := rpcauth.NewRequestAuthorizer(rpcauth.AuthorizationPolicy{RefreshRelaySubjects: []string{"relay"}})
	if err != nil {
		t.Fatal(err)
	}
	application := &transportSensitiveApplication{}
	handler, err := accessgrpc.New(application, rpcauth.InternalPrincipalResolver{}, requestAuthorizer)
	if err != nil {
		t.Fatal(err)
	}
	options, err := rpcauth.KitexServerOptions(authenticator)
	if err != nil {
		t.Fatal(err)
	}
	options = append(options, server.WithListener(listener), server.WithExitWaitTime(time.Second))
	kitexServer := sensitiveaccessservice.NewServer(handler, options...)
	stopped := make(chan error, 1)
	go func() { stopped <- kitexServer.Run() }()
	t.Cleanup(func() {
		if err := kitexServer.Stop(); err != nil {
			t.Errorf("stop Kitex server: %v", err)
		}
		select {
		case err := <-stopped:
			if err != nil {
				t.Errorf("run Kitex server: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("Kitex server did not stop")
		}
	})
	transportClient, err := sensitiveaccessservice.NewClient(
		"SensitiveAccessService", client.WithHostPorts(listener.Addr().String()), client.WithTransportProtocol(transport.GRPC),
	)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := platformauth.NewInternalJWTSigner("key-a", privateKey, "admin-bff", "control-plane")
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := bffrpc.NewSensitiveJWTCredentialAttacher(signer)
	if err != nil {
		t.Fatal(err)
	}
	rpcClient, err := bffrpc.NewSensitiveAccessClient(transportClient, credentials)
	if err != nil {
		t.Fatal(err)
	}
	useCase, err := bffapp.NewSensitiveAccessService(rpcClient)
	if err != nil {
		t.Fatal(err)
	}
	command := validSensitiveCommand()
	command.Principal.DisplayName = "Operator"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		result, callErr := useCase.Reveal(ctx, command)
		if callErr == nil {
			if result.Value != "secret" {
				t.Fatalf("result = %+v", result)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("reveal through Kitex: %v (last error: %v)", ctx.Err(), callErr)
		case <-time.After(10 * time.Millisecond):
		}
	}
	received := application.Command()
	if received.Principal.Subject != "operator@example.com" || received.Principal.DisplayName != "Operator" ||
		len(received.Principal.Roles) != 1 || len(received.Principal.AllowedScopes) != 1 || received.RequestID != "reveal-1" ||
		received.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || received.Scope.Environment != "production" {
		t.Fatalf("received command = %+v", received)
	}
}

type rejectConsumerVerifier struct{}

func (rejectConsumerVerifier) Verify(context.Context, string) (platformauth.ConsumerIdentity, error) {
	return platformauth.ConsumerIdentity{}, errors.New("consumer tokens are not accepted")
}

type transportSensitiveApplication struct {
	mu      sync.Mutex
	command access.RevealCommand
}

func (application *transportSensitiveApplication) Reveal(_ context.Context, command access.RevealCommand) (access.RevealResult, error) {
	application.mu.Lock()
	application.command = command
	application.mu.Unlock()
	return access.RevealResult{Value: "secret", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (application *transportSensitiveApplication) Command() access.RevealCommand {
	application.mu.Lock()
	defer application.mu.Unlock()
	return application.command
}

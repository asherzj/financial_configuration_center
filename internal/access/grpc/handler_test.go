package grpc_test

import (
	"context"
	"net"
	"testing"
	"time"

	commonv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/common/v1"
	controlv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/control/v1"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/control/v1/sensitiveaccessservice"
	access "github.com/asherzj/financial_configuration_center/internal/access/application"
	accessgrpc "github.com/asherzj/financial_configuration_center/internal/access/grpc"
	"github.com/cloudwego/kitex/client"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
	"github.com/cloudwego/kitex/server"
	"github.com/cloudwego/kitex/transport"
)

func TestRevealFieldMapsIdentityAndAuthorityFacts(t *testing.T) {
	t.Parallel()
	expiresAt := time.Date(2026, 8, 20, 0, 1, 0, 0, time.UTC)
	application := &stubApplication{result: access.RevealResult{Value: "secret", ExpiresAt: expiresAt}}
	handler, err := accessgrpc.New(application, identity{})
	if err != nil {
		t.Fatal(err)
	}
	response, err := handler.RevealField(context.Background(), &controlv1.RevealFieldRequest{
		ModelCode: "model", Scope: &commonv1.Scope{Region: "cn", Environment: "production"},
		RecordKey: "record", FieldName: "secret", ExpectedRecordRevision: 8, ExpectedCollectionRevision: 9,
		ExpectedModelRevision: 7, ExpectedServerEpoch: "epoch", ExpectedSnapshotInstance: "instance",
		ExpectedSnapshotGeneration: 3, Reason: "incident",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Value != "secret" || !response.ExpiresAt.AsTime().Equal(expiresAt) || application.last.Principal.Subject != "viewer" || application.last.RequestID != "request" || application.last.ExpectedCollectionRevision != 9 {
		t.Fatalf("response=%+v command=%+v", response, application.last)
	}
}

func TestRevealFieldPreservesApplicationStatusAcrossKitexTransport(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := accessgrpc.New(&statusApplication{}, identity{})
	if err != nil {
		t.Fatal(err)
	}
	kitexServer := sensitiveaccessservice.NewServer(handler, server.WithListener(listener), server.WithExitWaitTime(time.Second))
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
	kitexClient, err := sensitiveaccessservice.NewClient("SensitiveAccessService", client.WithHostPorts(listener.Addr().String()), client.WithTransportProtocol(transport.GRPC))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := validRevealRequest("ready")
	for {
		if _, err := kitexClient.RevealField(ctx, request); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	for model, code := range map[string]kitexcodes.Code{"aborted": kitexcodes.Aborted, "precondition": kitexcodes.FailedPrecondition} {
		if _, err := kitexClient.RevealField(ctx, validRevealRequest(model)); kitexstatus.Code(err) != code {
			t.Fatalf("%s code=%v error=%v", model, kitexstatus.Code(err), err)
		}
	}
}

func validRevealRequest(modelCode string) *controlv1.RevealFieldRequest {
	return &controlv1.RevealFieldRequest{
		ModelCode: modelCode, Scope: &commonv1.Scope{Region: "cn", Environment: "production"},
		RecordKey: "record", FieldName: "secret", ExpectedRecordRevision: 8,
		ExpectedCollectionRevision: 9, ExpectedModelRevision: 7, ExpectedServerEpoch: "epoch",
		ExpectedSnapshotInstance: "instance", ExpectedSnapshotGeneration: 3, Reason: "incident",
	}
}

type stubApplication struct {
	result access.RevealResult
	err    error
	last   access.RevealCommand
}

type statusApplication struct{}

func (*statusApplication) Reveal(_ context.Context, command access.RevealCommand) (access.RevealResult, error) {
	switch command.ModelCode {
	case "aborted":
		return access.RevealResult{}, access.ErrAborted
	case "precondition":
		return access.RevealResult{}, access.ErrFailedPrecondition
	default:
		return access.RevealResult{Value: "ready", ExpiresAt: time.Now().Add(time.Minute)}, nil
	}
}

func (stub *stubApplication) Reveal(_ context.Context, command access.RevealCommand) (access.RevealResult, error) {
	stub.last = command
	return stub.result, stub.err
}

type identity struct{}

func (identity) Subject(context.Context) (string, error)     { return "viewer", nil }
func (identity) DisplayName(context.Context) (string, error) { return "Viewer", nil }
func (identity) Roles(context.Context) ([]string, error) {
	return []string{access.SensitiveViewerRole}, nil
}
func (identity) RequestID(context.Context) string { return "request" }

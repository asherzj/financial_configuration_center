package rpc_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	controlv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/control/v1"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/control/v1/sensitiveaccessservice"
	bffapp "github.com/asherzj/financial_configuration_center/internal/adminbff/application"
	bffrpc "github.com/asherzj/financial_configuration_center/internal/adminbff/infrastructure/rpc"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	"github.com/cloudwego/kitex/client/callopt"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	kitexmetadata "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/metadata"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestSensitiveAccessClientAuthenticatesPrincipalAndMapsRequest(t *testing.T) {
	t.Parallel()
	expiresAt := time.Now().UTC().Add(30 * time.Second)
	credentials := &credentialStub{}
	transport := &sensitiveClientStub{response: &controlv1.RevealFieldResponse{Value: "secret", ExpiresAt: timestamppb.New(expiresAt)}}
	client, err := bffrpc.NewSensitiveAccessClient(transport, credentials)
	if err != nil {
		t.Fatal(err)
	}
	bucket := int32(71)
	command := validSensitiveCommand()
	command.PreviewBucket = &bucket
	result, err := client.RevealField(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "secret" || !result.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("result = %+v", result)
	}
	if credentials.principal.Subject != "operator@example.com" || credentials.principal.DisplayName != "Operator" ||
		len(credentials.principal.Roles) != 1 || credentials.principal.Roles[0] != bffapp.SensitiveViewerRole || len(credentials.principal.AllowedScopes) != 1 {
		t.Fatalf("credential principal = %+v", credentials.principal)
	}
	if transport.request.ModelCode != "routes" || transport.request.Scope.Environment != "production" || transport.request.ExpectedRecordRevision != 8 ||
		transport.request.PreviewBucket == nil || *transport.request.PreviewBucket != 71 {
		t.Fatalf("wire request = %+v", transport.request)
	}
	metadata, ok := kitexmetadata.FromOutgoingContext(transport.ctx)
	if !ok || first(metadata.Get("authorization")) != "Bearer signed" || first(metadata.Get("x-request-id")) != "reveal-1" ||
		first(metadata.Get("traceparent")) != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" || first(metadata.Get("tracestate")) != "vendor=value" {
		t.Fatalf("outgoing metadata = %+v", metadata)
	}
	credentials.principal.Roles[0] = "mutated"
	credentials.principal.AllowedScopes[0].Environment = "mutated"
	if command.Principal.Roles[0] != bffapp.SensitiveViewerRole || command.Principal.AllowedScopes[0].Environment != "production" {
		t.Fatalf("credential attacher mutated caller command: %+v", command.Principal)
	}
}

func TestSensitiveAccessClientMapsStatusesWithoutLeakingTransportError(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		code kitexcodes.Code
		want error
	}{
		"invalid":      {kitexcodes.InvalidArgument, bffapp.ErrSensitiveInvalid},
		"forbidden":    {kitexcodes.PermissionDenied, bffapp.ErrSensitiveForbidden},
		"aborted":      {kitexcodes.Aborted, bffapp.ErrSensitiveAborted},
		"not found":    {kitexcodes.NotFound, bffapp.ErrSensitiveNotFound},
		"precondition": {kitexcodes.FailedPrecondition, bffapp.ErrSensitiveFailedPrecondition},
	} {
		t.Run(name, func(t *testing.T) {
			client, err := bffrpc.NewSensitiveAccessClient(
				&sensitiveClientStub{err: kitexstatus.Err(test.code, "downstream detail")}, &credentialStub{},
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.RevealField(context.Background(), validSensitiveCommand())
			if !errors.Is(err, test.want) || err.Error() != test.want.Error() {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestSensitiveAccessClientFailsClosedForInvalidDependenciesCredentialsAndResponses(t *testing.T) {
	t.Parallel()
	var nilClient *sensitiveClientStub
	var nilCredentials *credentialStub
	if _, err := bffrpc.NewSensitiveAccessClient(nilClient, &credentialStub{}); err == nil {
		t.Fatal("expected typed-nil client rejection")
	}
	if _, err := bffrpc.NewSensitiveAccessClient(&sensitiveClientStub{}, nilCredentials); err == nil {
		t.Fatal("expected typed-nil credential attacher rejection")
	}
	transport := &sensitiveClientStub{}
	client, err := bffrpc.NewSensitiveAccessClient(transport, &credentialStub{err: errors.New("signing failed")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RevealField(context.Background(), validSensitiveCommand()); err == nil || transport.calls != 0 {
		t.Fatalf("credential failure error=%v calls=%d", err, transport.calls)
	}
	for name, response := range map[string]*controlv1.RevealFieldResponse{
		"nil":               nil,
		"missing expiry":    {Value: "secret"},
		"invalid expiry":    {Value: "secret", ExpiresAt: &timestamppb.Timestamp{Seconds: 253402300800}},
		"past expiry":       {Value: "secret", ExpiresAt: timestamppb.New(time.Now().Add(-time.Minute))},
		"far future expiry": {Value: "secret", ExpiresAt: timestamppb.New(time.Now().Add(2 * time.Minute))},
	} {
		t.Run(name, func(t *testing.T) {
			client, err := bffrpc.NewSensitiveAccessClient(&sensitiveClientStub{response: response}, &credentialStub{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.RevealField(context.Background(), validSensitiveCommand()); !errors.Is(err, bffrpc.ErrInvalidSensitiveAccessResponse) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestSensitiveAccessClientRejectsRevisionOverflowBeforeCredentialsOrRPC(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*bffapp.RevealSensitiveCommand){
		"record": func(command *bffapp.RevealSensitiveCommand) {
			command.ExpectedRecordRevision = uint64(math.MaxInt64) + 1
		},
		"collection": func(command *bffapp.RevealSensitiveCommand) {
			command.ExpectedCollectionRevision = uint64(math.MaxInt64) + 1
		},
		"model": func(command *bffapp.RevealSensitiveCommand) {
			command.ExpectedModelRevision = uint64(math.MaxInt64) + 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			transport, credentials := &sensitiveClientStub{}, &credentialStub{}
			client, _ := bffrpc.NewSensitiveAccessClient(transport, credentials)
			command := validSensitiveCommand()
			mutate(&command)
			if _, err := client.RevealField(context.Background(), command); !errors.Is(err, bffapp.ErrSensitiveInvalid) || credentials.calls != 0 || transport.calls != 0 {
				t.Fatalf("error=%v credentials=%d transport=%d", err, credentials.calls, transport.calls)
			}
		})
	}
}

func TestSensitiveAccessClientAcceptsMaximumWireRevision(t *testing.T) {
	t.Parallel()
	transport := &sensitiveClientStub{response: &controlv1.RevealFieldResponse{
		Value: "secret", ExpiresAt: timestamppb.New(time.Now().UTC().Add(30 * time.Second)),
	}}
	credentials := &credentialStub{}
	client, _ := bffrpc.NewSensitiveAccessClient(transport, credentials)
	command := validSensitiveCommand()
	command.ExpectedRecordRevision = math.MaxInt64
	command.ExpectedCollectionRevision = math.MaxInt64
	command.ExpectedModelRevision = math.MaxInt64
	if _, err := client.RevealField(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if credentials.calls != 1 || transport.calls != 1 || transport.request.ExpectedRecordRevision != math.MaxInt64 ||
		transport.request.ExpectedCollectionRevision != math.MaxInt64 || transport.request.ExpectedModelRevision != math.MaxInt64 {
		t.Fatalf("credentials=%d transport=%d request=%+v", credentials.calls, transport.calls, transport.request)
	}
}

func validSensitiveCommand() bffapp.RevealSensitiveCommand {
	return bffapp.RevealSensitiveCommand{
		ModelCode: "routes", Scope: bffapp.SensitiveScope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey: "visa", FieldName: "secret", ExpectedRecordRevision: 8, ExpectedCollectionRevision: 9,
		ExpectedModelRevision: 7, ExpectedServerEpoch: "epoch", ExpectedSnapshotInstance: "instance",
		ExpectedSnapshotGeneration: 3, Reason: "incident", RequestID: "reveal-1",
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", TraceParent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", TraceState: "vendor=value",
		Principal: bffapp.SensitivePrincipal{
			Subject: "operator@example.com", DisplayName: "Operator", Roles: []string{bffapp.SensitiveViewerRole},
			AllowedScopes: []platformauth.ScopePattern{{Region: "cn", Environment: "production", Stage: "*"}},
		},
	}
}

type sensitiveClientStub struct {
	response *controlv1.RevealFieldResponse
	err      error
	request  *controlv1.RevealFieldRequest
	ctx      context.Context
	calls    int
}

func (stub *sensitiveClientStub) RevealField(ctx context.Context, request *controlv1.RevealFieldRequest, _ ...callopt.Option) (*controlv1.RevealFieldResponse, error) {
	stub.calls++
	stub.ctx, stub.request = ctx, request
	return stub.response, stub.err
}

type credentialStub struct {
	principal bffapp.SensitivePrincipal
	err       error
	calls     int
}

func (stub *credentialStub) AttachSensitiveCredentials(ctx context.Context, principal bffapp.SensitivePrincipal) (context.Context, error) {
	stub.calls++
	stub.principal = principal
	if stub.err != nil {
		return nil, stub.err
	}
	return kitexmetadata.AppendToOutgoingContext(ctx, "authorization", "Bearer signed"), nil
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

var _ sensitiveaccessservice.Client = (*sensitiveClientStub)(nil)

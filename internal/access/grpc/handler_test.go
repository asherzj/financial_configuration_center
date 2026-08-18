package grpc_test

import (
	"context"
	"testing"
	"time"

	access "github.com/asherzj/financial_configuration_center/internal/access/application"
	accessgrpc "github.com/asherzj/financial_configuration_center/internal/access/grpc"
	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	controlv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/control/v1"
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

type stubApplication struct {
	result access.RevealResult
	err    error
	last   access.RevealCommand
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

package grpc_test

import (
	"context"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/configserver"
	configgrpc "github.com/asherzj/financial_configuration_center/internal/configserver/grpc"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/config/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestGetSnapshotMapsDeterministicCollectionPayload(t *testing.T) {
	t.Parallel()

	application := stubApplication{response: configserver.GetSnapshotResponse{
		Identity:    snapshot.Identity{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance", Generation: 3, PublishedAt: time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)},
		Environment: "production",
		Collections: []configserver.CollectionPayload{{
			Name: "payment_routes", Revision: 8, Digest: "digest",
			Records: []configserver.Record{{RecordKey: "key", RecordRevision: 8, Data: map[string]string{"priority": "7", "route_code": "visa-cn"}}},
		}},
	}}
	handler, err := configgrpc.New(application)
	if err != nil {
		t.Fatal(err)
	}
	request := &configv1.GetSnapshotRequest{ConsumerId: "payment-service", ClientId: "pod-1", Scope: scope("production")}
	first, err := handler.GetSnapshot(context.Background(), request)
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	second, err := handler.GetSnapshot(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Collections) != 1 || first.Collections[0].Codec != "PROTOBUF" || first.Collections[0].FormatVersion != 1 {
		t.Fatalf("payload envelope = %+v", first.Collections)
	}
	if string(first.Collections[0].Data) != string(second.Collections[0].Data) {
		t.Fatal("same collection produced nondeterministic bytes")
	}
	var body configv1.CollectionData
	if err := proto.Unmarshal(first.Collections[0].Data, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Records) != 1 || body.Records[0].Values["priority"] != "7" || body.Records[0].RecordRevision != 8 {
		t.Fatalf("collection body = %+v", &body)
	}
	if first.Snapshot.SnapshotGeneration != 3 || first.Scope.Environment != "production" {
		t.Fatalf("response authority = %+v", first)
	}
}

func TestGetSnapshotRejectsMissingScopeAtTransportBoundary(t *testing.T) {
	t.Parallel()

	handler, err := configgrpc.New(stubApplication{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.GetSnapshot(context.Background(), &configv1.GetSnapshotRequest{ConsumerId: "consumer", ClientId: "client"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing scope code = %s, err %v", status.Code(err), err)
	}
}

type stubApplication struct {
	response configserver.GetSnapshotResponse
}

func (application stubApplication) GetSnapshot(context.Context, configserver.GetSnapshotRequest) (configserver.GetSnapshotResponse, error) {
	return application.response, nil
}

func scope(environment string) *commonv1.Scope { return &commonv1.Scope{Environment: environment} }

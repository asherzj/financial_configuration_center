package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/config/v1"
	"github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/config/v1/configservice"
	"github.com/cloudwego/kitex/pkg/kerrors"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
	"github.com/cloudwego/kitex/server"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

func main() {
	listenAddress := flag.String("listen", "127.0.0.1:0", "test listener address")
	flag.Parse()

	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	srv := configservice.NewServer(
		&interopConfigService{},
		server.WithListener(listener),
		server.WithExitWaitTime(time.Second),
	)
	fmt.Printf("LISTEN %s\n", listener.Addr().String())
	if err := srv.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type interopConfigService struct{}

func (interopConfigService) GetSnapshot(context.Context, *configv1.GetSnapshotRequest) (*configv1.GetSnapshotResponse, error) {
	return &configv1.GetSnapshotResponse{
		Snapshot: &commonv1.SnapshotIdentity{
			ServerEpoch:        "00000000-0000-7000-8000-000000000001",
			ServerInstanceId:   "00000000-0000-7000-8000-000000000002",
			SnapshotInstance:   "00000000-0000-7000-8000-000000000003",
			SnapshotGeneration: 1,
		},
	}, nil
}

func (interopConfigService) DiffVersions(context.Context, *configv1.DiffVersionsRequest) (*configv1.DiffVersionsResponse, error) {
	bizErr := kerrors.NewGRPCBizStatusError(400, "invalid version request")
	grpcErr := bizErr.(kerrors.GRPCStatusIface)
	st, err := kitexstatus.New(kitexcodes.InvalidArgument, "invalid version request").WithDetails(&errdetails.BadRequest{
		FieldViolations: []*errdetails.BadRequest_FieldViolation{{Field: "consumer_id", Description: "required"}},
	})
	if err != nil {
		return nil, err
	}
	grpcErr.SetGRPCStatus(st)
	return nil, bizErr
}

func (interopConfigService) GetCollections(context.Context, *configv1.GetCollectionsRequest) (*configv1.GetCollectionsResponse, error) {
	return &configv1.GetCollectionsResponse{}, nil
}

func (interopConfigService) Watch(_ *configv1.WatchRequest, stream configv1.ConfigService_WatchServer) error {
	return stream.Send(&configv1.WatchResponse{
		EventId: "initial",
		Snapshot: &commonv1.SnapshotIdentity{
			ServerEpoch:        "00000000-0000-7000-8000-000000000001",
			ServerInstanceId:   "00000000-0000-7000-8000-000000000002",
			SnapshotInstance:   "00000000-0000-7000-8000-000000000003",
			SnapshotGeneration: 1,
		},
	})
}

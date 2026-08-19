package grpc_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/configserver"
	configgrpc "github.com/asherzj/financial_configuration_center/internal/configserver/grpc"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/config/v1"
	"github.com/cloudwego/kitex/pkg/streaming"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestWatchSendsInitialWatermarkAndHonorsCancellation(t *testing.T) {
	t.Parallel()
	manager, err := snapshot.NewManager(hintHandlerSource{}, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance"}, handlerClock{})
	if err != nil {
		t.Fatal(err)
	}
	hub, err := snapshot.NewWatchHub(manager, snapshot.WatchHubOptions{QueueSize: 1, MaxSubscribers: 1})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := configgrpc.NewWithWatch(stubApplication{}, hub, watchAuthorizer{}, allowRequestAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream := &watchStream{ctx: ctx, cancel: cancel}
	if err := handler.Watch(&configv1.WatchRequest{ConsumerId: "consumer", ClientId: "client", Scope: scope("production")}, stream); err != nil {
		t.Fatal(err)
	}
	if len(stream.sent) != 1 || stream.sent[0].Snapshot.ServerEpoch != "epoch" {
		t.Fatalf("watch events = %+v", stream.sent)
	}
}

func TestWatchRejectsAnotherManagedEnvironmentBeforeSubscription(t *testing.T) {
	t.Parallel()
	manager, err := snapshot.NewManager(hintHandlerSource{}, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance"}, handlerClock{})
	if err != nil {
		t.Fatal(err)
	}
	hub, err := snapshot.NewWatchHub(manager, snapshot.WatchHubOptions{QueueSize: 1, MaxSubscribers: 1})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := configgrpc.NewWithWatch(stubApplication{}, hub, watchAuthorizer{}, allowRequestAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	err = handler.Watch(&configv1.WatchRequest{ConsumerId: "consumer", ClientId: "client", Scope: scope("staging")}, &watchStream{ctx: context.Background()})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("managed-environment mismatch code = %s, err %v", status.Code(err), err)
	}
}

func TestGetSnapshotMapsDeterministicCollectionPayload(t *testing.T) {
	t.Parallel()

	application := stubApplication{response: configserver.GetSnapshotResponse{
		Identity: snapshot.Identity{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance", Generation: 3, PublishedAt: time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)},
		Region:   "cn", Environment: "production",
		Collections: []configserver.CollectionPayload{{
			Name: "payment_routes", Revision: 8, Digest: "digest", ChangeCursor: 23,
			Records: []configserver.Record{{RecordKey: "key", RecordRevision: 8, Data: map[string]string{"priority": "7", "route_code": "visa-cn"}}},
		}},
	}}
	handler, err := configgrpc.New(application, allowRequestAuthorizer{}, "production")
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
	if first.Collections[0].ChangeCursor != 23 {
		t.Fatalf("change cursor = %d", first.Collections[0].ChangeCursor)
	}
	if first.Collections[0].Version.EffectiveDigest == nil || first.Collections[0].Version.EffectiveDigest.Value != "digest" || first.Collections[0].Version.BaseDigest != nil {
		t.Fatalf("effective version digest = %+v", first.Collections[0].Version)
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

func TestGetSnapshotRejectsCursorOutsideRPCRange(t *testing.T) {
	t.Parallel()
	handler, err := configgrpc.New(stubApplication{response: configserver.GetSnapshotResponse{
		Collections: []configserver.CollectionPayload{{Name: "routes", Revision: 1, ChangeCursor: math.MaxUint64}},
	}}, allowRequestAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.GetSnapshot(context.Background(), &configv1.GetSnapshotRequest{
		ConsumerId: "consumer", ClientId: "client", Scope: scope("production"),
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("cursor overflow code = %s, err %v", status.Code(err), err)
	}
}

func TestGetSnapshotRejectsMissingScopeAtTransportBoundary(t *testing.T) {
	t.Parallel()

	handler, err := configgrpc.New(stubApplication{}, allowRequestAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.GetSnapshot(context.Background(), &configv1.GetSnapshotRequest{ConsumerId: "consumer", ClientId: "client"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing scope code = %s, err %v", status.Code(err), err)
	}
}

func TestGetSnapshotMapsManagedEnvironmentMismatchToFailedPrecondition(t *testing.T) {
	t.Parallel()
	handler, err := configgrpc.New(stubApplication{err: configserver.ErrManagedEnvironmentMismatch}, allowRequestAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.GetSnapshot(context.Background(), &configv1.GetSnapshotRequest{
		ConsumerId: "consumer", ClientId: "client", Scope: scope("staging"),
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("managed-environment mismatch code = %s, err %v", status.Code(err), err)
	}
}

func TestGetSnapshotMapsMissingSnapshotToUnavailable(t *testing.T) {
	t.Parallel()
	handler, err := configgrpc.New(stubApplication{err: configserver.ErrSnapshotUnavailable}, allowRequestAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.GetSnapshot(context.Background(), &configv1.GetSnapshotRequest{
		ConsumerId: "consumer", ClientId: "client", Scope: scope("production"),
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("missing snapshot code = %s, err %v", status.Code(err), err)
	}
}

func TestUnimplementedReadsStillRejectAnotherManagedEnvironment(t *testing.T) {
	t.Parallel()
	handler, err := configgrpc.New(stubApplication{}, allowRequestAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.DiffVersions(context.Background(), &configv1.DiffVersionsRequest{
		ConsumerId: "consumer", ClientId: "client", Scope: scope("staging"),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("DiffVersions cross-environment code = %s, err %v", status.Code(err), err)
	}
	if _, err := handler.GetCollections(context.Background(), &configv1.GetCollectionsRequest{
		ConsumerId: "consumer", ClientId: "client", Scope: scope("staging"), Collections: []string{"routes"},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("GetCollections cross-environment code = %s, err %v", status.Code(err), err)
	}
}

func TestDiffVersionsMapsStableChangeSets(t *testing.T) {
	t.Parallel()
	application := stubApplication{diffResponse: configserver.DiffVersionsResponse{
		Identity: snapshot.Identity{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance", Generation: 4},
		Added:    []string{"added"}, Modified: []string{"modified"}, Deleted: []string{"deleted"},
	}}
	handler, err := configgrpc.New(application, allowRequestAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	response, err := handler.DiffVersions(context.Background(), &configv1.DiffVersionsRequest{
		ConsumerId: "consumer", ClientId: "client", Scope: scope("production"),
		KnownVersions: []*configv1.VersionView{{
			Collection: "routes", ConfigRevision: 7,
			EffectiveDigest: &commonv1.Digest{Algorithm: "SHA-256", Value: strings.Repeat("a", 64)},
		}},
	})
	if err != nil || response.Snapshot.SnapshotGeneration != 4 || response.Added[0] != "added" || response.Modified[0] != "modified" || response.Deleted[0] != "deleted" {
		t.Fatalf("diff response = %+v, %v", response, err)
	}
}

func TestDiffVersionsRejectsMalformedKnownVersions(t *testing.T) {
	t.Parallel()
	handler, err := configgrpc.New(stubApplication{}, allowRequestAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	valid := &commonv1.Digest{Algorithm: "SHA-256", Value: strings.Repeat("a", 64)}
	tests := []struct {
		name     string
		versions []*configv1.VersionView
	}{
		{name: "blank collection", versions: []*configv1.VersionView{{Collection: " ", EffectiveDigest: valid}}},
		{name: "trim duplicate", versions: []*configv1.VersionView{{Collection: "routes", EffectiveDigest: valid}, {Collection: " routes ", EffectiveDigest: valid}}},
		{name: "algorithm", versions: []*configv1.VersionView{{Collection: "routes", EffectiveDigest: &commonv1.Digest{Algorithm: "MD5", Value: strings.Repeat("a", 64)}}}},
		{name: "uppercase", versions: []*configv1.VersionView{{Collection: "routes", EffectiveDigest: &commonv1.Digest{Algorithm: "SHA-256", Value: strings.Repeat("A", 64)}}}},
		{name: "length", versions: []*configv1.VersionView{{Collection: "routes", EffectiveDigest: &commonv1.Digest{Algorithm: "SHA-256", Value: "abc"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := handler.DiffVersions(context.Background(), &configv1.DiffVersionsRequest{
				ConsumerId: "consumer", ClientId: "client", Scope: scope("production"), KnownVersions: test.versions,
			})
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("code = %s, err %v", status.Code(err), err)
			}
		})
	}
}

func TestDiffVersionsMapsApplicationValidationToInvalidArgument(t *testing.T) {
	t.Parallel()
	handler, err := configgrpc.New(stubApplication{err: errors.Join(errors.New("wrapped"), configserver.ErrInvalidArgument)}, allowRequestAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.DiffVersions(context.Background(), &configv1.DiffVersionsRequest{
		ConsumerId: "consumer", ClientId: "client", Scope: scope("production"),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %s, err %v", status.Code(err), err)
	}
}

func TestConfigHandlerBindsConsumerIdentityBeforeApplication(t *testing.T) {
	t.Parallel()
	denied := errors.New("consumer binding denied")
	authorizer := &recordingConsumerAuthorizer{err: denied}
	application := &recordingApplication{}
	handler, err := configgrpc.New(application, authorizer, "production")
	if err != nil {
		t.Fatal(err)
	}
	request := &configv1.GetSnapshotRequest{ConsumerId: "payments", ClientId: "pod", Scope: &commonv1.Scope{Region: "cn", Environment: "production", Stage: "blue"}}
	if _, err := handler.GetSnapshot(context.Background(), request); !errors.Is(err, denied) {
		t.Fatalf("binding error=%v", err)
	}
	if application.called || authorizer.consumerID != "payments" || authorizer.scope != (platformauth.Scope{Region: "cn", Environment: "production", Stage: "blue"}) {
		t.Fatalf("application called=%v consumer=%q scope=%+v", application.called, authorizer.consumerID, authorizer.scope)
	}
}

func TestConfigHandlerNormalizesConcreteScopeOnce(t *testing.T) {
	t.Parallel()
	authorizer := &recordingConsumerAuthorizer{}
	application := &recordingApplication{}
	handler, err := configgrpc.New(application, authorizer, "production")
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.GetSnapshot(context.Background(), &configv1.GetSnapshotRequest{
		ConsumerId: " payments ", ClientId: " pod ",
		Scope: &commonv1.Scope{Region: " cn ", Environment: " production ", Stage: " blue "},
	})
	if err != nil || !application.called || authorizer.consumerID != "payments" || authorizer.scope != (platformauth.Scope{Region: "cn", Environment: "production", Stage: "blue"}) || application.last.Region != "cn" || application.last.Stage != "blue" {
		t.Fatalf("error=%v application=%+v consumer=%q scope=%+v", err, application.last, authorizer.consumerID, authorizer.scope)
	}
}

func TestConfigHandlerRejectsWildcardAndCrossEnvironmentBeforeAuthorization(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		scope *commonv1.Scope
		code  codes.Code
	}{
		"wildcard": {scope: &commonv1.Scope{Region: "*", Environment: "production"}, code: codes.InvalidArgument},
		"routing":  {scope: &commonv1.Scope{Region: "cn", Environment: "staging"}, code: codes.FailedPrecondition},
	} {
		authorizer := &recordingConsumerAuthorizer{}
		application := &recordingApplication{}
		handler, err := configgrpc.New(application, authorizer, "production")
		if err != nil {
			t.Fatal(err)
		}
		_, err = handler.GetSnapshot(context.Background(), &configv1.GetSnapshotRequest{ConsumerId: "payments", ClientId: "pod", Scope: test.scope})
		if status.Code(err) != test.code || application.called || authorizer.consumerID != "" {
			t.Fatalf("%s code=%v application=%v authorized=%q", name, status.Code(err), application.called, authorizer.consumerID)
		}
	}
}

func TestWatchBindsConsumerUsingStreamContextBeforeSubscription(t *testing.T) {
	t.Parallel()
	denied := errors.New("watch binding denied")
	authorizer := &recordingConsumerAuthorizer{err: denied}
	watcher := &recordingWatcher{}
	handler, err := configgrpc.NewWithWatch(stubApplication{}, watcher, watchAuthorizer{}, authorizer, "production")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), watchContextKey{}, "authenticated")
	err = handler.Watch(&configv1.WatchRequest{ConsumerId: "payments", ClientId: "pod", Scope: scope("production")}, &watchStream{ctx: ctx})
	if !errors.Is(err, denied) || watcher.called || authorizer.contextValue != "authenticated" {
		t.Fatalf("error=%v subscribed=%v context=%q", err, watcher.called, authorizer.contextValue)
	}
}

type stubApplication struct {
	response     configserver.GetSnapshotResponse
	diffResponse configserver.DiffVersionsResponse
	err          error
}

type recordingApplication struct {
	called bool
	last   configserver.GetSnapshotRequest
}

func (application *recordingApplication) GetSnapshot(_ context.Context, request configserver.GetSnapshotRequest) (configserver.GetSnapshotResponse, error) {
	application.called = true
	application.last = request
	return configserver.GetSnapshotResponse{}, nil
}

type recordingConsumerAuthorizer struct {
	err          error
	consumerID   string
	scope        platformauth.Scope
	contextValue string
}

func (authorizer *recordingConsumerAuthorizer) AuthorizeConsumer(ctx context.Context, consumerID string, scope platformauth.Scope) error {
	authorizer.consumerID = consumerID
	authorizer.scope = scope
	authorizer.contextValue, _ = ctx.Value(watchContextKey{}).(string)
	return authorizer.err
}

type watchContextKey struct{}

type recordingWatcher struct{ called bool }

func (watcher *recordingWatcher) Subscribe() (*snapshot.WatchSubscription, error) {
	watcher.called = true
	return nil, errors.New("unexpected subscription")
}

func (application stubApplication) GetSnapshot(context.Context, configserver.GetSnapshotRequest) (configserver.GetSnapshotResponse, error) {
	return application.response, application.err
}

func (application stubApplication) DiffVersions(context.Context, configserver.DiffVersionsRequest) (configserver.DiffVersionsResponse, error) {
	return application.diffResponse, application.err
}

func (application *recordingApplication) DiffVersions(context.Context, configserver.DiffVersionsRequest) (configserver.DiffVersionsResponse, error) {
	application.called = true
	return configserver.DiffVersionsResponse{}, nil
}

func scope(environment string) *commonv1.Scope {
	return &commonv1.Scope{Region: "cn", Environment: environment}
}

type watchAuthorizer struct{}

func (watchAuthorizer) AuthorizedCollections(context.Context, string) ([]string, error) {
	return []string{"routes"}, nil
}

type allowRequestAuthorizer struct{}

func (allowRequestAuthorizer) AuthorizeConsumer(context.Context, string, platformauth.Scope) error {
	return nil
}

func (allowRequestAuthorizer) AuthorizeRefresh(context.Context, string) error { return nil }

func (allowRequestAuthorizer) AuthorizeDiagnostics(context.Context, string) error { return nil }

type watchStream struct {
	streaming.Stream
	ctx    context.Context
	cancel context.CancelFunc
	sent   []*configv1.WatchResponse
}

func (stream *watchStream) Context() context.Context { return stream.ctx }

func (stream *watchStream) Send(response *configv1.WatchResponse) error {
	stream.sent = append(stream.sent, response)
	stream.cancel()
	return nil
}

package rpc

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	commonv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1/diagnosticsservice"
	bffapp "github.com/asherzj/financial_configuration_center/internal/adminbff/application"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
)

var ErrInvalidDiagnosticsResponse = errors.New("invalid diagnostics response")

type DiagnosticsClient struct {
	client             diagnosticsservice.Client
	managedEnvironment string
}

func NewDiagnosticsClient(client diagnosticsservice.Client, managedEnvironment string) (*DiagnosticsClient, error) {
	environment, err := platformauth.CompileEnvironment(managedEnvironment)
	if client == nil || isNil(client) || err != nil {
		return nil, errors.New("new BFF diagnostics client: client and managed environment are required")
	}
	return &DiagnosticsClient{client: client, managedEnvironment: environment}, nil
}

func (client *DiagnosticsClient) ReadSnapshotDiagnostics(ctx context.Context) (bffapp.SnapshotDiagnostics, error) {
	if ctx == nil {
		return bffapp.SnapshotDiagnostics{}, ErrInvalidDiagnosticsResponse
	}
	response, err := client.client.GetSnapshotStatus(ctx, &configv1.GetSnapshotStatusRequest{})
	if err != nil {
		return bffapp.SnapshotDiagnostics{}, fmt.Errorf("get Config Server snapshot diagnostics: %w", err)
	}
	if response == nil || response.Environment != client.managedEnvironment || response.Snapshot == nil ||
		response.CollectionCount != int64(len(response.Collections)) || len(response.FailedDependencyGroups) != len(response.FailedDependencyGroupDetails) {
		return bffapp.SnapshotDiagnostics{}, ErrInvalidDiagnosticsResponse
	}
	identity, err := mapDiagnosticIdentity(response.Snapshot)
	if err != nil {
		return bffapp.SnapshotDiagnostics{}, err
	}
	result := bffapp.SnapshotDiagnostics{
		Identity: identity, Environment: response.Environment, LastErrorCode: response.GetLastErrorCode(),
		Collections:            make([]bffapp.CollectionDiagnostic, len(response.Collections)),
		FailedDependencyGroups: make([][]string, len(response.FailedDependencyGroupDetails)),
	}
	for index, collection := range response.Collections {
		mapped, err := mapSnapshotCollection(response.Environment, collection)
		if err != nil {
			return bffapp.SnapshotDiagnostics{}, err
		}
		result.Collections[index] = mapped
	}
	for index, group := range response.FailedDependencyGroupDetails {
		if group == nil || len(group.Collections) == 0 || response.FailedDependencyGroups[index] != strings.Join(group.Collections, ",") {
			return bffapp.SnapshotDiagnostics{}, ErrInvalidDiagnosticsResponse
		}
		result.FailedDependencyGroups[index] = append([]string(nil), group.Collections...)
	}
	return result, nil
}

func (client *DiagnosticsClient) ReadCollectionDiagnostics(ctx context.Context, collection string) (bffapp.CollectionDiagnostic, error) {
	if ctx == nil || strings.TrimSpace(collection) == "" {
		return bffapp.CollectionDiagnostic{}, ErrInvalidDiagnosticsResponse
	}
	response, err := client.client.GetCollectionStatus(ctx, &configv1.GetCollectionStatusRequest{
		Collection: collection, Environment: client.managedEnvironment,
	})
	if err != nil {
		if kitexstatus.Code(err) == kitexcodes.NotFound {
			return bffapp.CollectionDiagnostic{}, bffapp.ErrDiagnosticsNotFound
		}
		return bffapp.CollectionDiagnostic{}, fmt.Errorf("get Config Server collection diagnostics: %w", err)
	}
	if response == nil || response.Collection != collection || response.Environment != client.managedEnvironment || response.Version == nil ||
		response.Version.Collection != collection || response.Version.ConfigRevision <= 0 || response.ChangeCursor < 0 {
		return bffapp.CollectionDiagnostic{}, ErrInvalidDiagnosticsResponse
	}
	digest, err := mapDiagnosticDigest(response.Version.EffectiveDigest)
	if err != nil {
		return bffapp.CollectionDiagnostic{}, err
	}
	return bffapp.CollectionDiagnostic{
		Name: response.Collection, Environment: response.Environment, Revision: uint64(response.Version.ConfigRevision),
		Cursor: uint64(response.ChangeCursor), Digest: digest, LastErrorCode: response.GetLastErrorCode(),
	}, nil
}

func mapDiagnosticIdentity(source *commonv1.SnapshotIdentity) (bffapp.DiagnosticIdentity, error) {
	if source == nil || source.ServerEpoch == "" || source.ServerInstanceId == "" || source.SnapshotInstance == "" || source.SnapshotGeneration == 0 {
		return bffapp.DiagnosticIdentity{}, ErrInvalidDiagnosticsResponse
	}
	publishedAt, err := mapPublishedAt(source.PublishedAt)
	if err != nil {
		return bffapp.DiagnosticIdentity{}, err
	}
	return bffapp.DiagnosticIdentity{
		ServerEpoch: source.ServerEpoch, ServerInstanceID: source.ServerInstanceId,
		SnapshotInstance: source.SnapshotInstance, Generation: source.SnapshotGeneration, PublishedAt: publishedAt,
	}, nil
}

func mapSnapshotCollection(environment string, source *configv1.SnapshotCollectionStatus) (bffapp.CollectionDiagnostic, error) {
	if source == nil || source.Collection == "" || source.ConfigRevision <= 0 || source.ChangeCursor < 0 {
		return bffapp.CollectionDiagnostic{}, ErrInvalidDiagnosticsResponse
	}
	digest, err := mapDiagnosticDigest(source.EffectiveDigest)
	if err != nil {
		return bffapp.CollectionDiagnostic{}, err
	}
	return bffapp.CollectionDiagnostic{
		Name: source.Collection, Environment: environment, Revision: uint64(source.ConfigRevision),
		Cursor: uint64(source.ChangeCursor), Digest: digest,
	}, nil
}

func mapDiagnosticDigest(source *commonv1.Digest) (bffapp.DiagnosticDigest, error) {
	if source == nil || source.Algorithm != "SHA-256" || len(source.Value) != 64 || strings.ToLower(source.Value) != source.Value {
		return bffapp.DiagnosticDigest{}, ErrInvalidDiagnosticsResponse
	}
	if _, err := hex.DecodeString(source.Value); err != nil {
		return bffapp.DiagnosticDigest{}, ErrInvalidDiagnosticsResponse
	}
	return bffapp.DiagnosticDigest{Algorithm: source.Algorithm, Value: source.Value}, nil
}

var _ bffapp.DiagnosticsReader = (*DiagnosticsClient)(nil)

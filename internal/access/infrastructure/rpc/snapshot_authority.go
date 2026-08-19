package rpc

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	commonv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1/pagequeryservice"
	access "github.com/asherzj/financial_configuration_center/internal/access/application"
	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
)

var ErrInvalidSnapshotAuthorityResponse = errors.New("invalid snapshot authority response")

// SnapshotAuthorityReader adapts the Access application port to PageQuery's
// versioned metadata response. Row data is deliberately discarded.
type SnapshotAuthorityReader struct {
	client pagequeryservice.Client
}

func NewSnapshotAuthorityReader(client pagequeryservice.Client) (*SnapshotAuthorityReader, error) {
	if client == nil || isNil(client) {
		return nil, errors.New("new snapshot authority reader: client is required")
	}
	return &SnapshotAuthorityReader{client: client}, nil
}

func (reader *SnapshotAuthorityReader) ReadSnapshotAuthority(ctx context.Context, query access.SnapshotAuthorityQuery) (access.SnapshotAuthority, error) {
	if ctx == nil || query.ModelCode == "" || query.Scope.Region == "" || query.Scope.Environment == "" {
		return access.SnapshotAuthority{}, ErrInvalidSnapshotAuthorityResponse
	}
	pageNumber, pageSize := int32(1), int32(1)
	response, err := reader.client.QueryPage(ctx, &configv1.QueryPageRequest{
		ModelCode: query.ModelCode,
		Scope: &commonv1.Scope{
			Region: query.Scope.Region, Environment: query.Scope.Environment, Stage: query.Scope.Stage,
		},
		QueryType:     commonv1.QueryPageType_QUERY_PAGE_TYPE_ONLY_DATA,
		Page:          &commonv1.PageRequest{Number: &pageNumber, Size: &pageSize},
		PreviewBucket: cloneInt32(query.PreviewBucket),
	})
	if err != nil {
		switch kitexstatus.Code(err) {
		case kitexcodes.NotFound, kitexcodes.FailedPrecondition:
			return access.SnapshotAuthority{Found: false}, nil
		case kitexcodes.Unavailable:
			return access.SnapshotAuthority{}, access.ErrSnapshotUnavailable
		default:
			return access.SnapshotAuthority{}, fmt.Errorf("query Config Server snapshot authority: %w", err)
		}
	}
	if response == nil || response.ModelCode != query.ModelCode || response.Snapshot == nil || response.Snapshot.ServerEpoch == "" || response.Snapshot.SnapshotInstance == "" || response.Snapshot.SnapshotGeneration == 0 || response.ModelRevision <= 0 || response.CollectionRevision <= 0 {
		return access.SnapshotAuthority{}, ErrInvalidSnapshotAuthorityResponse
	}
	return access.SnapshotAuthority{
		Found: true, Environment: query.Scope.Environment,
		ServerEpoch: response.Snapshot.ServerEpoch, SnapshotInstance: response.Snapshot.SnapshotInstance,
		SnapshotGeneration: response.Snapshot.SnapshotGeneration,
		ModelRevision:      catalog.ConfigRevision(response.ModelRevision), CollectionRevision: catalog.ConfigRevision(response.CollectionRevision),
	}, nil
}

func cloneInt32(value *int32) *int32 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func isNil(value any) bool {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

var _ access.SnapshotAuthorityReader = (*SnapshotAuthorityReader)(nil)

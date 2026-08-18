package grpc

import (
	"context"
	"errors"
	"math"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	"github.com/asherzj/financial_configuration_center/internal/pagequery"
	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/config/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Application interface {
	Query(pagequery.Request) (pagequery.Result, error)
}

type Handler struct{ application Application }

func New(application Application) (*Handler, error) {
	if application == nil {
		return nil, errors.New("new PageQueryService handler: application is required")
	}
	return &Handler{application: application}, nil
}

func (handler *Handler) QueryPage(_ context.Context, request *configv1.QueryPageRequest) (*configv1.QueryPageResponse, error) {
	if request == nil || request.Scope == nil {
		return nil, status.Error(codes.InvalidArgument, "model_code and scope are required")
	}
	if len(request.Conditions) != 0 || request.PreviewBucket != nil {
		return nil, status.Error(codes.Unimplemented, "filters and preview bucket are not implemented in the base-only slice")
	}
	queryType, err := fromQueryType(request.QueryType)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	page := pagequery.PageSpec{}
	if request.Page != nil {
		page.Number, page.Size = request.Page.Number, request.Page.Size
	}
	result, err := handler.application.Query(pagequery.Request{
		ModelCode: request.ModelCode, Environment: request.Scope.Environment, Type: queryType, Page: page,
	})
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	modelRevision, err := revisionInt64(result.ModelRevision)
	if err != nil {
		return nil, status.Error(codes.Internal, "model revision exceeds RPC range")
	}
	collectionRevision, err := revisionInt64(result.CollectionRevision)
	if err != nil {
		return nil, status.Error(codes.Internal, "collection revision exceeds RPC range")
	}
	response := &configv1.QueryPageResponse{
		ModelCode: result.ModelCode, ModelName: result.ModelName, QueryType: toQueryType(result.QueryType),
		ProjectionFields: append([]string(nil), result.ProjectionFields...),
		Page:             &commonv1.PageResponse{Number: result.PageNumber, Size: result.PageSize, TotalNumber: result.TotalNumber, TotalPages: result.TotalPages},
		Snapshot:         mapIdentity(result.Snapshot), ModelRevision: modelRevision, CollectionRevision: collectionRevision,
		Rows: make([]*configv1.PageRow, len(result.Rows)), InteractionFields: make([]*configv1.PageInteractionField, len(result.InteractionFields)),
	}
	for index, row := range result.Rows {
		revision, err := revisionInt64(row.RecordRevision)
		if err != nil {
			return nil, status.Error(codes.Internal, "record revision exceeds RPC range")
		}
		response.Rows[index] = &configv1.PageRow{RecordKey: row.RecordKey, RecordRevision: revision, Values: cloneMap(row.Values), MaskedFields: append([]string(nil), row.MaskedFields...)}
	}
	for index, field := range result.InteractionFields {
		operators := make([]commonv1.FilterOperator, len(field.AllowedFilterOperators))
		for operatorIndex, operator := range field.AllowedFilterOperators {
			operators[operatorIndex] = toFilterOperator(operator)
		}
		response.InteractionFields[index] = &configv1.PageInteractionField{
			Name: field.Name, DisplayName: field.DisplayName, Description: field.Description,
			Type: toFieldType(field.Type), UiControl: toUIControl(field.UIControl),
			Queryable: field.Queryable, Editable: field.Editable, IsRequired: field.Required,
			Sensitive: field.Sensitive, Projected: field.Projected, KeyField: field.KeyField,
			AllowedFilterOperators: operators, DefaultFilterOperator: toFilterOperator(field.DefaultFilterOperator),
			DefaultValue: cloneStringPointer(field.DefaultValue), DisplayOrder: field.DisplayOrder,
		}
	}
	return response, nil
}

func fromQueryType(value commonv1.QueryPageType) (pagequery.QueryType, error) {
	switch value {
	case commonv1.QueryPageType_QUERY_PAGE_TYPE_ALL:
		return pagequery.TypeAll, nil
	case commonv1.QueryPageType_QUERY_PAGE_TYPE_ONLY_DATA:
		return pagequery.TypeOnlyData, nil
	default:
		return "", errors.New("query_type must be ALL or ONLY_DATA")
	}
}

func toQueryType(value pagequery.QueryType) commonv1.QueryPageType {
	if value == pagequery.TypeOnlyData {
		return commonv1.QueryPageType_QUERY_PAGE_TYPE_ONLY_DATA
	}
	return commonv1.QueryPageType_QUERY_PAGE_TYPE_ALL
}

func toFieldType(value catalog.FieldType) commonv1.FieldType {
	switch value {
	case catalog.FieldTypeString:
		return commonv1.FieldType_FIELD_TYPE_STRING
	case catalog.FieldTypeInt64:
		return commonv1.FieldType_FIELD_TYPE_INT64
	case catalog.FieldTypeFloat64:
		return commonv1.FieldType_FIELD_TYPE_FLOAT64
	case catalog.FieldTypeBool:
		return commonv1.FieldType_FIELD_TYPE_BOOL
	case catalog.FieldTypeTimestamp:
		return commonv1.FieldType_FIELD_TYPE_TIMESTAMP
	case catalog.FieldTypeJSON:
		return commonv1.FieldType_FIELD_TYPE_JSON
	default:
		return commonv1.FieldType_FIELD_TYPE_UNSPECIFIED
	}
}

func toUIControl(value catalog.UIControlType) commonv1.UiControlType {
	switch value {
	case catalog.UIControlInput:
		return commonv1.UiControlType_UI_CONTROL_TYPE_INPUT
	case catalog.UIControlSelect:
		return commonv1.UiControlType_UI_CONTROL_TYPE_SELECT
	case catalog.UIControlTime:
		return commonv1.UiControlType_UI_CONTROL_TYPE_TIME
	case catalog.UIControlNumber:
		return commonv1.UiControlType_UI_CONTROL_TYPE_NUMBER
	case catalog.UIControlBoolean:
		return commonv1.UiControlType_UI_CONTROL_TYPE_BOOLEAN
	case catalog.UIControlTextarea:
		return commonv1.UiControlType_UI_CONTROL_TYPE_TEXTAREA
	case catalog.UIControlJSON:
		return commonv1.UiControlType_UI_CONTROL_TYPE_JSON
	default:
		return commonv1.UiControlType_UI_CONTROL_TYPE_UNSPECIFIED
	}
}

func toFilterOperator(value catalog.FilterOperator) commonv1.FilterOperator {
	switch value {
	case catalog.FilterExact:
		return commonv1.FilterOperator_FILTER_OPERATOR_EXACT
	case catalog.FilterContains:
		return commonv1.FilterOperator_FILTER_OPERATOR_CONTAINS
	case catalog.FilterClosedRange:
		return commonv1.FilterOperator_FILTER_OPERATOR_CLOSED_RANGE
	case catalog.FilterOpenRange:
		return commonv1.FilterOperator_FILTER_OPERATOR_OPEN_RANGE
	case catalog.FilterIn:
		return commonv1.FilterOperator_FILTER_OPERATOR_IN
	case catalog.FilterNotIn:
		return commonv1.FilterOperator_FILTER_OPERATOR_NOT_IN
	default:
		return commonv1.FilterOperator_FILTER_OPERATOR_UNSPECIFIED
	}
}

func mapIdentity(identity snapshot.Identity) *commonv1.SnapshotIdentity {
	return &commonv1.SnapshotIdentity{
		ServerEpoch: identity.ServerEpoch, ServerInstanceId: identity.ServerInstanceID,
		SnapshotInstance: identity.SnapshotInstance, SnapshotGeneration: identity.Generation,
		PublishedAt: timestamppb.New(identity.PublishedAt),
	}
}

func revisionInt64(revision catalog.ConfigRevision) (int64, error) {
	if uint64(revision) > math.MaxInt64 {
		return 0, errors.New("revision exceeds int64")
	}
	return int64(revision), nil
}

func cloneMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

var _ configv1.PageQueryService = (*Handler)(nil)

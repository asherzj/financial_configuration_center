package grpc

import (
	"context"
	"errors"
	"fmt"
	"math"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	"github.com/asherzj/financial_configuration_center/internal/pagequery"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/config/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Application interface {
	Query(pagequery.Request) (pagequery.Result, error)
}

type RequestAuthorizer interface {
	AuthorizePageQuery(context.Context, platformauth.Scope) error
}

type Handler struct {
	application        Application
	authorizer         RequestAuthorizer
	managedEnvironment string
}

func New(application Application, authorizer RequestAuthorizer, managedEnvironment string) (*Handler, error) {
	compiledEnvironment, environmentErr := platformauth.CompileEnvironment(managedEnvironment)
	if application == nil || authorizer == nil || environmentErr != nil {
		return nil, errors.New("new PageQueryService handler: application, request authorizer, and managed environment are required")
	}
	return &Handler{application: application, authorizer: authorizer, managedEnvironment: compiledEnvironment}, nil
}

func (handler *Handler) QueryPage(ctx context.Context, request *configv1.QueryPageRequest) (*configv1.QueryPageResponse, error) {
	if request == nil || request.Scope == nil {
		return nil, status.Error(codes.InvalidArgument, "model_code and scope are required")
	}
	scope, err := platformauth.CompileScope(request.Scope.Region, request.Scope.Environment, request.Scope.Stage)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "scope must contain concrete region and environment segments")
	}
	if scope.Environment != handler.managedEnvironment {
		return nil, status.Error(codes.FailedPrecondition, "requested environment is not managed by this server")
	}
	if err := handler.authorizer.AuthorizePageQuery(ctx, scope); err != nil {
		return nil, err
	}
	queryType, err := fromQueryType(request.QueryType)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	page := pagequery.PageSpec{}
	if request.Page != nil {
		page.Number, page.Size = request.Page.Number, request.Page.Size
	}
	conditions := make([]pagequery.FilterCondition, len(request.Conditions))
	for index, condition := range request.Conditions {
		mapped, err := fromFilterCondition(condition)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("condition %d: %v", index, err))
		}
		conditions[index] = mapped
	}
	result, err := handler.application.Query(pagequery.Request{
		ModelCode: request.ModelCode, Region: scope.Region, Environment: scope.Environment,
		Stage: scope.Stage, PreviewBucket: request.PreviewBucket, Type: queryType, Page: page, Conditions: conditions,
	})
	if err != nil {
		switch {
		case errors.Is(err, pagequery.ErrManagedEnvironmentMismatch):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		case errors.Is(err, pagequery.ErrSnapshotUnavailable):
			return nil, status.Error(codes.Unavailable, "page query snapshot is not available")
		case errors.Is(err, pagequery.ErrNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		case errors.Is(err, pagequery.ErrInvalidArgument):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		default:
			return nil, status.Error(codes.Internal, "page query failed")
		}
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
		ReleaseTypes: make([]*configv1.ReleaseType, len(result.ReleaseTypes)),
	}
	for index, row := range result.Rows {
		revision, err := revisionInt64(row.RecordRevision)
		if err != nil {
			return nil, status.Error(codes.Internal, "record revision exceeds RPC range")
		}
		response.Rows[index] = &configv1.PageRow{
			RecordKey: row.RecordKey, RecordRevision: revision, Values: cloneMap(row.Values),
			MaskedFields: append([]string(nil), row.MaskedFields...), BasePresent: row.BasePresent,
			BaseValues: cloneMap(row.BaseValues), ChangedFields: append([]string(nil), row.ChangedFields...),
		}
	}
	for index, field := range result.InteractionFields {
		operators := make([]commonv1.FilterOperator, len(field.AllowedFilterOperators))
		for operatorIndex, operator := range field.AllowedFilterOperators {
			operators[operatorIndex] = toFilterOperator(operator)
		}
		options := make([]*configv1.SelectOption, len(field.Options))
		for optionIndex, option := range field.Options {
			options[optionIndex] = &configv1.SelectOption{Code: option.Code, Label: option.Label, Disabled: option.Disabled}
		}
		validationRules := make([]*configv1.ValidationRule, len(field.ValidationRules))
		for ruleIndex, rule := range field.ValidationRules {
			validationRules[ruleIndex] = &configv1.ValidationRule{Kind: string(rule.Kind), Params: cloneMap(rule.Params), Message: rule.Message}
		}
		var autoFill *configv1.AutoFillRule
		if field.AutoFill != nil {
			autoFill = &configv1.AutoFillRule{Source: string(field.AutoFill.Source), Value: field.AutoFill.Value}
		}
		response.InteractionFields[index] = &configv1.PageInteractionField{
			Name: field.Name, DisplayName: field.DisplayName, Description: field.Description,
			Type: toFieldType(field.Type), UiControl: toUIControl(field.UIControl),
			Queryable: field.Queryable, Editable: field.Editable, IsRequired: field.Required,
			Sensitive: field.Sensitive, Projected: field.Projected, KeyField: field.KeyField,
			AutoFill: autoFill, ValidationRules: validationRules,
			AllowedFilterOperators: operators, DefaultFilterOperator: toFilterOperator(field.DefaultFilterOperator),
			DefaultValue: cloneStringPointer(field.DefaultValue), DisplayOrder: field.DisplayOrder, Options: options,
		}
	}
	for index, releaseType := range result.ReleaseTypes {
		response.ReleaseTypes[index] = &configv1.ReleaseType{
			Code: releaseType.Code, Name: releaseType.Name, TemplateCode: releaseType.TemplateCode,
			Available: releaseType.Available, UnavailableReasonCode: optionalString(releaseType.UnavailableReasonCode),
		}
	}
	return response, nil
}

func fromFilterCondition(source *configv1.FilterCondition) (pagequery.FilterCondition, error) {
	if source == nil {
		return pagequery.FilterCondition{}, errors.New("condition is required")
	}
	operator, err := fromFilterOperator(source.Operator)
	if err != nil {
		return pagequery.FilterCondition{}, err
	}
	result := pagequery.FilterCondition{Field: source.Field, Operator: operator, Set: make([]pagequery.ScalarValue, len(source.Set))}
	if source.Value != nil {
		value, err := fromScalar(source.Value)
		if err != nil {
			return pagequery.FilterCondition{}, fmt.Errorf("value: %w", err)
		}
		result.Value = &value
	}
	if source.Lower != nil {
		value, err := fromScalar(source.Lower)
		if err != nil {
			return pagequery.FilterCondition{}, fmt.Errorf("lower: %w", err)
		}
		result.Lower = &value
	}
	if source.Upper != nil {
		value, err := fromScalar(source.Upper)
		if err != nil {
			return pagequery.FilterCondition{}, fmt.Errorf("upper: %w", err)
		}
		result.Upper = &value
	}
	for index, scalar := range source.Set {
		value, err := fromScalar(scalar)
		if err != nil {
			return pagequery.FilterCondition{}, fmt.Errorf("set value %d: %w", index, err)
		}
		result.Set[index] = value
	}
	return result, nil
}

func fromScalar(source *configv1.ScalarValue) (pagequery.ScalarValue, error) {
	if source == nil {
		return pagequery.ScalarValue{}, errors.New("scalar is required")
	}
	fieldType, err := fromFieldType(source.Type)
	if err != nil {
		return pagequery.ScalarValue{}, err
	}
	return pagequery.ScalarValue{Type: fieldType, Canonical: source.Canonical}, nil
}

func fromFieldType(value commonv1.FieldType) (catalog.FieldType, error) {
	switch value {
	case commonv1.FieldType_FIELD_TYPE_STRING:
		return catalog.FieldTypeString, nil
	case commonv1.FieldType_FIELD_TYPE_INT64:
		return catalog.FieldTypeInt64, nil
	case commonv1.FieldType_FIELD_TYPE_FLOAT64:
		return catalog.FieldTypeFloat64, nil
	case commonv1.FieldType_FIELD_TYPE_BOOL:
		return catalog.FieldTypeBool, nil
	case commonv1.FieldType_FIELD_TYPE_TIMESTAMP:
		return catalog.FieldTypeTimestamp, nil
	case commonv1.FieldType_FIELD_TYPE_JSON:
		return catalog.FieldTypeJSON, nil
	default:
		return "", fmt.Errorf("field type %q is invalid", value)
	}
}

func fromFilterOperator(value commonv1.FilterOperator) (catalog.FilterOperator, error) {
	switch value {
	case commonv1.FilterOperator_FILTER_OPERATOR_EXACT:
		return catalog.FilterExact, nil
	case commonv1.FilterOperator_FILTER_OPERATOR_CONTAINS:
		return catalog.FilterContains, nil
	case commonv1.FilterOperator_FILTER_OPERATOR_CLOSED_RANGE:
		return catalog.FilterClosedRange, nil
	case commonv1.FilterOperator_FILTER_OPERATOR_OPEN_RANGE:
		return catalog.FilterOpenRange, nil
	case commonv1.FilterOperator_FILTER_OPERATOR_IN:
		return catalog.FilterIn, nil
	case commonv1.FilterOperator_FILTER_OPERATOR_NOT_IN:
		return catalog.FilterNotIn, nil
	default:
		return "", fmt.Errorf("filter operator %q is invalid", value)
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
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

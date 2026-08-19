package rpc

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	commonv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1/pagequeryservice"
	bffapp "github.com/asherzj/financial_configuration_center/internal/adminbff/application"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var ErrInvalidPageQueryResponse = errors.New("invalid page query response")

type PageQueryClient struct {
	client pagequeryservice.Client
}

func NewPageQueryClient(client pagequeryservice.Client) (*PageQueryClient, error) {
	if client == nil || isNil(client) {
		return nil, errors.New("new BFF page query client: client is required")
	}
	return &PageQueryClient{client: client}, nil
}

func (client *PageQueryClient) QueryPage(ctx context.Context, query bffapp.QueryRequest) (bffapp.QueryResult, error) {
	if ctx == nil {
		return bffapp.QueryResult{}, fmt.Errorf("%w: context is required", bffapp.ErrPageQueryInvalid)
	}
	request, err := mapQueryRequest(query)
	if err != nil {
		return bffapp.QueryResult{}, err
	}
	response, err := client.client.QueryPage(ctx, request)
	if err != nil {
		if kitexstatus.Code(err) == kitexcodes.InvalidArgument {
			return bffapp.QueryResult{}, bffapp.ErrPageQueryInvalid
		}
		return bffapp.QueryResult{}, fmt.Errorf("query Config Server page: %w", err)
	}
	return mapQueryResponse(query.ModelCode, query.Type, response)
}

func mapQueryRequest(source bffapp.QueryRequest) (*configv1.QueryPageRequest, error) {
	queryType, err := toQueryType(source.Type)
	if err != nil {
		return nil, err
	}
	conditions := make([]*configv1.FilterCondition, len(source.Conditions))
	for index, condition := range source.Conditions {
		mapped, err := mapCondition(condition)
		if err != nil {
			return nil, fmt.Errorf("%w: condition %d: %v", bffapp.ErrPageQueryInvalid, index, err)
		}
		conditions[index] = mapped
	}
	return &configv1.QueryPageRequest{
		ModelCode: source.ModelCode,
		Scope: &commonv1.Scope{
			Region: source.Region, Environment: source.Environment, Stage: source.Stage,
		},
		QueryType:     queryType,
		Conditions:    conditions,
		Page:          &commonv1.PageRequest{Number: cloneInt32(source.Page.Number), Size: cloneInt32(source.Page.Size)},
		PreviewBucket: cloneInt32(source.PreviewBucket),
	}, nil
}

func mapCondition(source bffapp.FilterCondition) (*configv1.FilterCondition, error) {
	operator, err := toFilterOperator(source.Operator)
	if err != nil {
		return nil, err
	}
	mapped := &configv1.FilterCondition{
		Field: source.Field, Operator: operator, Set: make([]*configv1.ScalarValue, len(source.Set)),
	}
	if source.Value != nil {
		mapped.Value, err = mapScalar(*source.Value)
		if err != nil {
			return nil, err
		}
	}
	if source.Lower != nil {
		mapped.Lower, err = mapScalar(*source.Lower)
		if err != nil {
			return nil, err
		}
	}
	if source.Upper != nil {
		mapped.Upper, err = mapScalar(*source.Upper)
		if err != nil {
			return nil, err
		}
	}
	for index, value := range source.Set {
		mapped.Set[index], err = mapScalar(value)
		if err != nil {
			return nil, err
		}
	}
	return mapped, nil
}

func mapScalar(source bffapp.ScalarValue) (*configv1.ScalarValue, error) {
	fieldType, err := toFieldType(source.Type)
	if err != nil {
		return nil, err
	}
	return &configv1.ScalarValue{Type: fieldType, Canonical: source.Canonical}, nil
}

func mapQueryResponse(modelCode string, expectedType bffapp.QueryType, source *configv1.QueryPageResponse) (bffapp.QueryResult, error) {
	if source == nil || source.ModelCode != modelCode || source.Page == nil || source.Snapshot == nil || source.ModelRevision <= 0 || source.CollectionRevision <= 0 ||
		source.Page.Number <= 0 || source.Page.Size <= 0 || source.Page.TotalNumber < 0 || source.Page.TotalPages < 0 {
		return bffapp.QueryResult{}, ErrInvalidPageQueryResponse
	}
	queryType, err := fromQueryType(source.QueryType)
	if err != nil {
		return bffapp.QueryResult{}, err
	}
	if queryType != expectedType {
		return bffapp.QueryResult{}, ErrInvalidPageQueryResponse
	}
	publishedAt, err := mapPublishedAt(source.Snapshot.PublishedAt)
	if err != nil || source.Snapshot.ServerEpoch == "" || source.Snapshot.ServerInstanceId == "" || source.Snapshot.SnapshotInstance == "" || source.Snapshot.SnapshotGeneration == 0 {
		return bffapp.QueryResult{}, ErrInvalidPageQueryResponse
	}
	result := bffapp.QueryResult{
		ModelCode: source.ModelCode, ModelName: source.ModelName, QueryType: queryType,
		ProjectionFields: append([]string(nil), source.ProjectionFields...),
		PageNumber:       source.Page.Number, PageSize: source.Page.Size, TotalNumber: source.Page.TotalNumber, TotalPages: source.Page.TotalPages,
		Snapshot: bffapp.SnapshotIdentity{
			ServerEpoch: source.Snapshot.ServerEpoch, ServerInstanceID: source.Snapshot.ServerInstanceId,
			SnapshotInstance: source.Snapshot.SnapshotInstance, Generation: source.Snapshot.SnapshotGeneration, PublishedAt: publishedAt,
		},
		ModelRevision: uint64(source.ModelRevision), CollectionRevision: uint64(source.CollectionRevision),
		Rows: make([]bffapp.Row, len(source.Rows)), InteractionFields: make([]bffapp.InteractionField, len(source.InteractionFields)),
		ReleaseTypes: make([]bffapp.ReleaseType, len(source.ReleaseTypes)),
	}
	for index, row := range source.Rows {
		if row == nil || row.RecordRevision <= 0 {
			return bffapp.QueryResult{}, ErrInvalidPageQueryResponse
		}
		result.Rows[index] = bffapp.Row{
			RecordKey: row.RecordKey, RecordRevision: uint64(row.RecordRevision), Values: cloneMap(row.Values),
			BasePresent: row.BasePresent, BaseValues: cloneMap(row.BaseValues),
			ChangedFields: append([]string(nil), row.ChangedFields...), MaskedFields: append([]string(nil), row.MaskedFields...),
		}
	}
	for index, field := range source.InteractionFields {
		mapped, err := mapInteractionField(field)
		if err != nil {
			return bffapp.QueryResult{}, err
		}
		result.InteractionFields[index] = mapped
	}
	if rowsExposeSensitiveValues(result.Rows, result.InteractionFields) {
		return bffapp.QueryResult{}, ErrInvalidPageQueryResponse
	}
	for index, releaseType := range source.ReleaseTypes {
		if releaseType == nil {
			return bffapp.QueryResult{}, ErrInvalidPageQueryResponse
		}
		result.ReleaseTypes[index] = bffapp.ReleaseType{
			Code: releaseType.Code, Name: releaseType.Name, TemplateCode: releaseType.TemplateCode,
			Available: releaseType.Available, UnavailableReasonCode: releaseType.GetUnavailableReasonCode(),
		}
	}
	return result, nil
}

func rowsExposeSensitiveValues(rows []bffapp.Row, fields []bffapp.InteractionField) bool {
	sensitiveFields := make(map[string]struct{})
	for _, field := range fields {
		if field.Sensitive {
			sensitiveFields[field.Name] = struct{}{}
		}
	}
	for _, row := range rows {
		for _, field := range row.MaskedFields {
			sensitiveFields[field] = struct{}{}
		}
	}
	for _, row := range rows {
		for field := range sensitiveFields {
			if _, exposed := row.Values[field]; exposed {
				return true
			}
			if _, exposed := row.BaseValues[field]; exposed {
				return true
			}
		}
	}
	return false
}

func mapInteractionField(source *configv1.PageInteractionField) (bffapp.InteractionField, error) {
	if source == nil {
		return bffapp.InteractionField{}, ErrInvalidPageQueryResponse
	}
	fieldType, err := fromFieldType(source.Type)
	if err != nil {
		return bffapp.InteractionField{}, err
	}
	uiControl, err := fromUIControl(source.UiControl)
	if err != nil {
		return bffapp.InteractionField{}, err
	}
	operators := make([]bffapp.FilterOperator, len(source.AllowedFilterOperators))
	for index, operator := range source.AllowedFilterOperators {
		operators[index], err = fromFilterOperator(operator)
		if err != nil {
			return bffapp.InteractionField{}, err
		}
	}
	defaultOperator := bffapp.FilterOperator("")
	if source.DefaultFilterOperator != commonv1.FilterOperator_FILTER_OPERATOR_UNSPECIFIED {
		defaultOperator, err = fromFilterOperator(source.DefaultFilterOperator)
		if err != nil {
			return bffapp.InteractionField{}, err
		}
	}
	mapped := bffapp.InteractionField{
		Name: source.Name, DisplayName: source.DisplayName, Description: source.Description,
		Type: fieldType, UIControl: uiControl, Queryable: source.Queryable, Editable: source.Editable,
		Required: source.IsRequired, Sensitive: source.Sensitive, Projected: source.Projected, KeyField: source.KeyField,
		AllowedFilterOperators: operators, DefaultFilterOperator: defaultOperator,
		DefaultValue: cloneString(source.DefaultValue), DisplayOrder: source.DisplayOrder,
		ValidationRules: make([]bffapp.ValidationRule, len(source.ValidationRules)), Options: make([]bffapp.SelectOption, len(source.Options)),
	}
	if source.AutoFill != nil {
		autoFillSource, err := fromAutoFillSource(source.AutoFill.Source)
		if err != nil {
			return bffapp.InteractionField{}, err
		}
		mapped.AutoFill = &bffapp.AutoFillRule{Source: autoFillSource, Value: source.AutoFill.Value}
	}
	for index, rule := range source.ValidationRules {
		if rule == nil {
			return bffapp.InteractionField{}, ErrInvalidPageQueryResponse
		}
		kind, err := fromValidationRuleKind(rule.Kind)
		if err != nil {
			return bffapp.InteractionField{}, err
		}
		mapped.ValidationRules[index] = bffapp.ValidationRule{Kind: kind, Params: cloneMap(rule.Params), Message: rule.Message}
	}
	for index, option := range source.Options {
		if option == nil {
			return bffapp.InteractionField{}, ErrInvalidPageQueryResponse
		}
		mapped.Options[index] = bffapp.SelectOption{Code: option.Code, Label: option.Label, Disabled: option.Disabled}
	}
	return mapped, nil
}

func toQueryType(value bffapp.QueryType) (commonv1.QueryPageType, error) {
	switch value {
	case bffapp.QueryTypeAll:
		return commonv1.QueryPageType_QUERY_PAGE_TYPE_ALL, nil
	case bffapp.QueryTypeOnlyData:
		return commonv1.QueryPageType_QUERY_PAGE_TYPE_ONLY_DATA, nil
	default:
		return 0, fmt.Errorf("%w: query type is invalid", bffapp.ErrPageQueryInvalid)
	}
}

func fromQueryType(value commonv1.QueryPageType) (bffapp.QueryType, error) {
	switch value {
	case commonv1.QueryPageType_QUERY_PAGE_TYPE_ALL:
		return bffapp.QueryTypeAll, nil
	case commonv1.QueryPageType_QUERY_PAGE_TYPE_ONLY_DATA:
		return bffapp.QueryTypeOnlyData, nil
	default:
		return "", ErrInvalidPageQueryResponse
	}
}

func toFieldType(value bffapp.FieldType) (commonv1.FieldType, error) {
	switch value {
	case "":
		return commonv1.FieldType_FIELD_TYPE_UNSPECIFIED, nil
	case bffapp.FieldTypeString:
		return commonv1.FieldType_FIELD_TYPE_STRING, nil
	case bffapp.FieldTypeInt64:
		return commonv1.FieldType_FIELD_TYPE_INT64, nil
	case bffapp.FieldTypeFloat64:
		return commonv1.FieldType_FIELD_TYPE_FLOAT64, nil
	case bffapp.FieldTypeBool:
		return commonv1.FieldType_FIELD_TYPE_BOOL, nil
	case bffapp.FieldTypeTimestamp:
		return commonv1.FieldType_FIELD_TYPE_TIMESTAMP, nil
	case bffapp.FieldTypeJSON:
		return commonv1.FieldType_FIELD_TYPE_JSON, nil
	default:
		return 0, errors.New("scalar field type is invalid")
	}
}

func fromFieldType(value commonv1.FieldType) (bffapp.FieldType, error) {
	switch value {
	case commonv1.FieldType_FIELD_TYPE_STRING:
		return bffapp.FieldTypeString, nil
	case commonv1.FieldType_FIELD_TYPE_INT64:
		return bffapp.FieldTypeInt64, nil
	case commonv1.FieldType_FIELD_TYPE_FLOAT64:
		return bffapp.FieldTypeFloat64, nil
	case commonv1.FieldType_FIELD_TYPE_BOOL:
		return bffapp.FieldTypeBool, nil
	case commonv1.FieldType_FIELD_TYPE_TIMESTAMP:
		return bffapp.FieldTypeTimestamp, nil
	case commonv1.FieldType_FIELD_TYPE_JSON:
		return bffapp.FieldTypeJSON, nil
	default:
		return "", ErrInvalidPageQueryResponse
	}
}

func toFilterOperator(value bffapp.FilterOperator) (commonv1.FilterOperator, error) {
	switch value {
	case bffapp.FilterExact:
		return commonv1.FilterOperator_FILTER_OPERATOR_EXACT, nil
	case bffapp.FilterContains:
		return commonv1.FilterOperator_FILTER_OPERATOR_CONTAINS, nil
	case bffapp.FilterClosedRange:
		return commonv1.FilterOperator_FILTER_OPERATOR_CLOSED_RANGE, nil
	case bffapp.FilterOpenRange:
		return commonv1.FilterOperator_FILTER_OPERATOR_OPEN_RANGE, nil
	case bffapp.FilterIn:
		return commonv1.FilterOperator_FILTER_OPERATOR_IN, nil
	case bffapp.FilterNotIn:
		return commonv1.FilterOperator_FILTER_OPERATOR_NOT_IN, nil
	default:
		return 0, errors.New("filter operator is invalid")
	}
}

func fromFilterOperator(value commonv1.FilterOperator) (bffapp.FilterOperator, error) {
	switch value {
	case commonv1.FilterOperator_FILTER_OPERATOR_EXACT:
		return bffapp.FilterExact, nil
	case commonv1.FilterOperator_FILTER_OPERATOR_CONTAINS:
		return bffapp.FilterContains, nil
	case commonv1.FilterOperator_FILTER_OPERATOR_CLOSED_RANGE:
		return bffapp.FilterClosedRange, nil
	case commonv1.FilterOperator_FILTER_OPERATOR_OPEN_RANGE:
		return bffapp.FilterOpenRange, nil
	case commonv1.FilterOperator_FILTER_OPERATOR_IN:
		return bffapp.FilterIn, nil
	case commonv1.FilterOperator_FILTER_OPERATOR_NOT_IN:
		return bffapp.FilterNotIn, nil
	default:
		return "", ErrInvalidPageQueryResponse
	}
}

func fromUIControl(value commonv1.UiControlType) (bffapp.UIControlType, error) {
	switch value {
	case commonv1.UiControlType_UI_CONTROL_TYPE_INPUT:
		return bffapp.UIControlInput, nil
	case commonv1.UiControlType_UI_CONTROL_TYPE_SELECT:
		return bffapp.UIControlSelect, nil
	case commonv1.UiControlType_UI_CONTROL_TYPE_TIME:
		return bffapp.UIControlTime, nil
	case commonv1.UiControlType_UI_CONTROL_TYPE_NUMBER:
		return bffapp.UIControlNumber, nil
	case commonv1.UiControlType_UI_CONTROL_TYPE_BOOLEAN:
		return bffapp.UIControlBoolean, nil
	case commonv1.UiControlType_UI_CONTROL_TYPE_TEXTAREA:
		return bffapp.UIControlTextarea, nil
	case commonv1.UiControlType_UI_CONTROL_TYPE_JSON:
		return bffapp.UIControlJSON, nil
	default:
		return "", ErrInvalidPageQueryResponse
	}
}

func fromValidationRuleKind(value string) (bffapp.ValidationRuleKind, error) {
	kind := bffapp.ValidationRuleKind(value)
	switch kind {
	case bffapp.ValidationRequired, bffapp.ValidationEnum, bffapp.ValidationRegex,
		bffapp.ValidationMin, bffapp.ValidationMax, bffapp.ValidationMinLength, bffapp.ValidationMaxLength:
		return kind, nil
	default:
		return "", ErrInvalidPageQueryResponse
	}
}

func fromAutoFillSource(value string) (bffapp.AutoFillSource, error) {
	source := bffapp.AutoFillSource(value)
	switch source {
	case bffapp.AutoFillActorSubject, bffapp.AutoFillActorName, bffapp.AutoFillCurrentTime,
		bffapp.AutoFillConstant, bffapp.AutoFillUUID:
		return source, nil
	default:
		return "", ErrInvalidPageQueryResponse
	}
}

func mapPublishedAt(value *timestamppb.Timestamp) (time.Time, error) {
	if value == nil || !value.IsValid() {
		return time.Time{}, ErrInvalidPageQueryResponse
	}
	return value.AsTime().UTC(), nil
}

func cloneInt32(value *int32) *int32 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
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

var _ bffapp.PageQueryPort = (*PageQueryClient)(nil)

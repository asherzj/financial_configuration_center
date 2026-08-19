package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/asherzj/financial_configuration_center/internal/catalog/application"
	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/config/v1"
	controlv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/control/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Application interface {
	CreateCollection(context.Context, application.Principal, application.CollectionInput) (application.CollectionView, error)
	UpdateCollection(context.Context, application.Principal, catalog.ConfigRevision, application.CollectionInput) (application.CollectionView, error)
	GetCollection(context.Context, application.Principal, string) (application.CollectionView, error)
	ListCollections(context.Context, application.Principal, application.PageQuery) (application.CollectionPage, error)
	CreateSubscription(context.Context, application.Principal, application.SubscriptionInput) (application.SubscriptionView, error)
	UpdateSubscription(context.Context, application.Principal, catalog.ConfigRevision, application.SubscriptionInput) (application.SubscriptionView, error)
	ListSubscriptions(context.Context, application.Principal, application.SubscriptionQuery) (application.SubscriptionPage, error)
	CreateModel(context.Context, application.Principal, application.ModelInput) (application.ModelView, error)
	UpdateModel(context.Context, application.Principal, catalog.ConfigRevision, application.ModelInput) (application.ModelView, error)
	GetModel(context.Context, application.Principal, string) (application.ModelView, error)
	ListModels(context.Context, application.Principal, application.ModelQuery) (application.ModelPage, error)
	CreateTemplate(context.Context, application.Principal, application.TemplateInput) (application.TemplateView, error)
	GetTemplate(context.Context, application.Principal, string, int64) (application.TemplateView, error)
	ListTemplates(context.Context, application.Principal, application.TemplateQuery) (application.TemplatePage, error)
}

type PrincipalResolver interface {
	Subject(context.Context) (string, error)
	DisplayName(context.Context) (string, error)
	Roles(context.Context) ([]string, error)
}

type Handler struct {
	application Application
	principals  PrincipalResolver
}

func New(application Application, principals PrincipalResolver) (*Handler, error) {
	if application == nil || principals == nil {
		return nil, errors.New("new CatalogAdminService handler: application and principal resolver are required")
	}
	return &Handler{application: application, principals: principals}, nil
}

func (handler *Handler) CreateCollection(ctx context.Context, request *controlv1.CreateCollectionRequest) (*controlv1.CreateCollectionResponse, error) {
	principal, err := handler.principal(ctx)
	if err != nil {
		return nil, err
	}
	input, err := collectionInput(request.GetCollection())
	if err != nil {
		return nil, err
	}
	view, err := handler.application.CreateCollection(ctx, principal, input)
	if err != nil {
		return nil, catalogError(err)
	}
	return &controlv1.CreateCollectionResponse{Collection: projectCollection(view)}, nil
}

func (handler *Handler) UpdateCollection(ctx context.Context, request *controlv1.UpdateCollectionRequest) (*controlv1.UpdateCollectionResponse, error) {
	principal, err := handler.principal(ctx)
	if err != nil {
		return nil, err
	}
	input, err := collectionInput(request.GetCollection())
	if err != nil {
		return nil, err
	}
	view, err := handler.application.UpdateCollection(ctx, principal, catalog.ConfigRevision(request.GetExpectedCollectionRevision()), input)
	if err != nil {
		return nil, catalogError(err)
	}
	return &controlv1.UpdateCollectionResponse{Collection: projectCollection(view)}, nil
}

func (handler *Handler) GetCollection(ctx context.Context, request *controlv1.GetCollectionRequest) (*controlv1.GetCollectionResponse, error) {
	principal, err := handler.principal(ctx)
	if err != nil {
		return nil, err
	}
	view, err := handler.application.GetCollection(ctx, principal, request.GetName())
	if err != nil {
		return nil, catalogError(err)
	}
	return &controlv1.GetCollectionResponse{Collection: projectCollection(view)}, nil
}

func (handler *Handler) ListCollections(ctx context.Context, request *controlv1.ListCollectionsRequest) (*controlv1.ListCollectionsResponse, error) {
	principal, err := handler.principal(ctx)
	if err != nil {
		return nil, err
	}
	number, size := pageRequest(request.GetPage())
	page, err := handler.application.ListCollections(ctx, principal, application.PageQuery{PageNumber: number, PageSize: size})
	if err != nil {
		return nil, catalogError(err)
	}
	collections := make([]*controlv1.Collection, len(page.Collections))
	for index, collection := range page.Collections {
		collections[index] = projectCollection(collection)
	}
	return &controlv1.ListCollectionsResponse{Collections: collections, Page: projectPage(page.PageNumber, page.PageSize, page.TotalNumber, page.TotalPages)}, nil
}

func (handler *Handler) CreateSubscription(ctx context.Context, request *controlv1.CreateSubscriptionRequest) (*controlv1.CreateSubscriptionResponse, error) {
	principal, err := handler.principal(ctx)
	if err != nil {
		return nil, err
	}
	input, err := subscriptionInput(request.GetSubscription())
	if err != nil {
		return nil, err
	}
	view, err := handler.application.CreateSubscription(ctx, principal, input)
	if err != nil {
		return nil, catalogError(err)
	}
	return &controlv1.CreateSubscriptionResponse{Subscription: projectSubscription(view)}, nil
}

func (handler *Handler) UpdateSubscription(ctx context.Context, request *controlv1.UpdateSubscriptionRequest) (*controlv1.UpdateSubscriptionResponse, error) {
	principal, err := handler.principal(ctx)
	if err != nil {
		return nil, err
	}
	input, err := subscriptionInput(request.GetSubscription())
	if err != nil {
		return nil, err
	}
	view, err := handler.application.UpdateSubscription(ctx, principal, catalog.ConfigRevision(request.GetExpectedSubscriptionRevision()), input)
	if err != nil {
		return nil, catalogError(err)
	}
	return &controlv1.UpdateSubscriptionResponse{Subscription: projectSubscription(view)}, nil
}

func (handler *Handler) ListSubscriptions(ctx context.Context, request *controlv1.ListSubscriptionsRequest) (*controlv1.ListSubscriptionsResponse, error) {
	principal, err := handler.principal(ctx)
	if err != nil {
		return nil, err
	}
	number, size := pageRequest(request.GetPage())
	page, err := handler.application.ListSubscriptions(ctx, principal, application.SubscriptionQuery{ConsumerID: request.GetConsumerId(), Collection: request.GetCollection(), PageNumber: number, PageSize: size})
	if err != nil {
		return nil, catalogError(err)
	}
	subscriptions := make([]*controlv1.Subscription, len(page.Subscriptions))
	for index, subscription := range page.Subscriptions {
		subscriptions[index] = projectSubscription(subscription)
	}
	return &controlv1.ListSubscriptionsResponse{Subscriptions: subscriptions, Page: projectPage(page.PageNumber, page.PageSize, page.TotalNumber, page.TotalPages)}, nil
}

func (handler *Handler) CreateModel(ctx context.Context, request *controlv1.CreateModelRequest) (*controlv1.CreateModelResponse, error) {
	principal, err := handler.principal(ctx)
	if err != nil {
		return nil, err
	}
	input, err := modelInput(request.GetModel())
	if err != nil {
		return nil, err
	}
	view, err := handler.application.CreateModel(ctx, principal, input)
	if err != nil {
		return nil, catalogError(err)
	}
	return &controlv1.CreateModelResponse{Model: projectModel(view)}, nil
}
func (handler *Handler) UpdateModel(ctx context.Context, request *controlv1.UpdateModelRequest) (*controlv1.UpdateModelResponse, error) {
	principal, err := handler.principal(ctx)
	if err != nil {
		return nil, err
	}
	input, err := modelInput(request.GetModel())
	if err != nil {
		return nil, err
	}
	view, err := handler.application.UpdateModel(ctx, principal, catalog.ConfigRevision(request.GetExpectedModelRevision()), input)
	if err != nil {
		return nil, catalogError(err)
	}
	return &controlv1.UpdateModelResponse{Model: projectModel(view)}, nil
}
func (handler *Handler) GetModel(ctx context.Context, request *controlv1.GetModelRequest) (*controlv1.GetModelResponse, error) {
	principal, err := handler.principal(ctx)
	if err != nil {
		return nil, err
	}
	view, err := handler.application.GetModel(ctx, principal, request.GetCode())
	if err != nil {
		return nil, catalogError(err)
	}
	return &controlv1.GetModelResponse{Model: projectModel(view)}, nil
}
func (handler *Handler) ListModels(ctx context.Context, request *controlv1.ListModelsRequest) (*controlv1.ListModelsResponse, error) {
	principal, err := handler.principal(ctx)
	if err != nil {
		return nil, err
	}
	number, size := pageRequest(request.GetPage())
	page, err := handler.application.ListModels(ctx, principal, application.ModelQuery{Collection: request.GetCollection(), PageNumber: number, PageSize: size})
	if err != nil {
		return nil, catalogError(err)
	}
	models := make([]*controlv1.Model, len(page.Models))
	for index, model := range page.Models {
		models[index] = projectModel(model)
	}
	return &controlv1.ListModelsResponse{Models: models, Page: projectPage(page.PageNumber, page.PageSize, page.TotalNumber, page.TotalPages)}, nil
}
func (handler *Handler) CreateReleaseTemplate(ctx context.Context, request *controlv1.CreateReleaseTemplateRequest) (*controlv1.CreateReleaseTemplateResponse, error) {
	principal, err := handler.principal(ctx)
	if err != nil {
		return nil, err
	}
	input, err := templateInput(request.GetReleaseTemplate())
	if err != nil {
		return nil, err
	}
	view, err := handler.application.CreateTemplate(ctx, principal, input)
	if err != nil {
		return nil, catalogError(err)
	}
	return &controlv1.CreateReleaseTemplateResponse{ReleaseTemplate: projectTemplate(view)}, nil
}
func (handler *Handler) GetReleaseTemplate(ctx context.Context, request *controlv1.GetReleaseTemplateRequest) (*controlv1.GetReleaseTemplateResponse, error) {
	principal, err := handler.principal(ctx)
	if err != nil {
		return nil, err
	}
	view, err := handler.application.GetTemplate(ctx, principal, request.GetCode(), request.GetVersion())
	if err != nil {
		return nil, catalogError(err)
	}
	return &controlv1.GetReleaseTemplateResponse{ReleaseTemplate: projectTemplate(view)}, nil
}
func (handler *Handler) ListReleaseTemplates(ctx context.Context, request *controlv1.ListReleaseTemplatesRequest) (*controlv1.ListReleaseTemplatesResponse, error) {
	principal, err := handler.principal(ctx)
	if err != nil {
		return nil, err
	}
	number, size := pageRequest(request.GetPage())
	page, err := handler.application.ListTemplates(ctx, principal, application.TemplateQuery{ModelCode: request.GetModelCode(), PageNumber: number, PageSize: size})
	if err != nil {
		return nil, catalogError(err)
	}
	templates := make([]*controlv1.ReleaseTemplate, len(page.Templates))
	for index, template := range page.Templates {
		templates[index] = projectTemplate(template)
	}
	return &controlv1.ListReleaseTemplatesResponse{ReleaseTemplates: templates, Page: projectPage(page.PageNumber, page.PageSize, page.TotalNumber, page.TotalPages)}, nil
}

func (handler *Handler) principal(ctx context.Context) (application.Principal, error) {
	subject, err := handler.principals.Subject(ctx)
	if err != nil || strings.TrimSpace(subject) == "" {
		return application.Principal{}, status.Error(codes.Unauthenticated, "authenticated actor is required")
	}
	displayName, err := handler.principals.DisplayName(ctx)
	if err != nil {
		return application.Principal{}, status.Error(codes.PermissionDenied, "actor display name could not be resolved")
	}
	roles, err := handler.principals.Roles(ctx)
	if err != nil {
		return application.Principal{}, status.Error(codes.PermissionDenied, "actor roles could not be resolved")
	}
	return application.Principal{Subject: subject, DisplayName: displayName, Roles: roles}, nil
}

func collectionInput(source *controlv1.Collection) (application.CollectionInput, error) {
	if source == nil {
		return application.CollectionInput{}, status.Error(codes.InvalidArgument, "collection is required")
	}
	fields := make([]catalog.FieldDefinition, len(source.Fields))
	for index, field := range source.Fields {
		if field == nil {
			return application.CollectionInput{}, status.Error(codes.InvalidArgument, "collection field is required")
		}
		fieldType, ok := fieldTypeFromProto(field.Type)
		if !ok {
			return application.CollectionInput{}, status.Error(codes.InvalidArgument, "collection field type is invalid")
		}
		rules := make([]catalog.ValidationRule, len(field.ValidationRules))
		for ruleIndex, rule := range field.ValidationRules {
			if rule == nil {
				return application.CollectionInput{}, status.Error(codes.InvalidArgument, "validation rule is required")
			}
			rules[ruleIndex] = catalog.ValidationRule{Kind: catalog.ValidationRuleKind(rule.Kind), Params: cloneMap(rule.Params), Message: rule.Message}
		}
		fields[index] = catalog.FieldDefinition{Name: field.Name, DisplayName: field.DisplayName, Type: fieldType, Required: field.IsRequired, Sensitive: field.Sensitive, DefaultValue: cloneString(field.DefaultValue), Description: field.Description, DisplayOrder: field.DisplayOrder, ValidationRules: rules}
	}
	return application.CollectionInput{Name: source.Name, Description: source.Description, Fields: fields, KeyFields: append([]string(nil), source.KeyFields...), SDKDeliveryEnabled: source.SdkDeliveryEnabled, SchemaVersion: source.SchemaVersion, Status: application.Status(source.Status)}, nil
}

func subscriptionInput(source *controlv1.Subscription) (application.SubscriptionInput, error) {
	if source == nil {
		return application.SubscriptionInput{}, status.Error(codes.InvalidArgument, "subscription is required")
	}
	return application.SubscriptionInput{ID: source.Id, ConsumerID: source.ConsumerId, Collection: source.Collection, IndexName: source.IndexName, IndexFields: append([]string(nil), source.IndexFields...), Cardinality: application.Cardinality(source.Cardinality), Enabled: source.Enabled}, nil
}

func modelInput(source *controlv1.Model) (application.ModelInput, error) {
	if source == nil {
		return application.ModelInput{}, status.Error(codes.InvalidArgument, "model is required")
	}
	return application.ModelInput{Code: source.Code, Name: source.Name, Collection: source.Collection, Definition: append([]byte(nil), source.DefinitionJson...), Enabled: source.Enabled}, nil
}

func templateInput(source *controlv1.ReleaseTemplate) (application.TemplateInput, error) {
	if source == nil {
		return application.TemplateInput{}, status.Error(codes.InvalidArgument, "release template is required")
	}
	steps := make([]map[string]any, len(source.Steps))
	for index, step := range source.Steps {
		if step == nil {
			return application.TemplateInput{}, status.Error(codes.InvalidArgument, "release template step is required")
		}
		params := make(map[string]any, len(step.Params))
		for key, raw := range step.Params {
			var decoded any
			if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
				params[key] = decoded
			} else {
				params[key] = raw
			}
		}
		steps[index] = map[string]any{"code": step.Code, "type": step.Type, "requiredRoles": append([]string(nil), step.RequiredRoles...), "params": params}
	}
	document, err := json.Marshal(map[string]any{"steps": steps})
	if err != nil {
		return application.TemplateInput{}, status.Error(codes.InvalidArgument, "release template steps are invalid")
	}
	finalEffect := release.FinalEffect("")
	switch source.FinalEffect {
	case commonv1.FinalEffect_FINAL_EFFECT_BASE_FINAL:
		finalEffect = release.FinalEffectBase
	case commonv1.FinalEffect_FINAL_EFFECT_OVERLAY_FINAL:
		finalEffect = release.FinalEffectOverlay
	}
	return application.TemplateInput{Code: source.Code, Name: source.Name, ModelCode: source.ModelCode, ReleaseTypeCode: source.ReleaseTypeCode, FinalEffect: finalEffect, SchedulingAllowed: source.SchedulingAllowed, MaxScheduleWindowSeconds: source.MaxScheduleWindowSeconds, Document: document, AllowedRoles: append([]string(nil), source.AllowedRoles...), Enabled: source.Enabled}, nil
}

func projectCollection(view application.CollectionView) *controlv1.Collection {
	fields := make([]*controlv1.FieldDefinition, len(view.Fields))
	for index, field := range view.Fields {
		rules := make([]*configv1.ValidationRule, len(field.ValidationRules))
		for ruleIndex, rule := range field.ValidationRules {
			rules[ruleIndex] = &configv1.ValidationRule{Kind: string(rule.Kind), Params: cloneMap(rule.Params), Message: rule.Message}
		}
		fields[index] = &controlv1.FieldDefinition{Name: field.Name, DisplayName: field.DisplayName, Type: fieldTypeToProto(field.Type), IsRequired: field.Required, Sensitive: field.Sensitive, DefaultValue: cloneString(field.DefaultValue), Description: field.Description, DisplayOrder: field.DisplayOrder, ValidationRules: rules}
	}
	revision := int64(view.ConfigRevision)
	if view.ConfigRevision > math.MaxInt64 {
		revision = math.MaxInt64
	}
	return &controlv1.Collection{Name: view.Name, Description: view.Description, Fields: fields, KeyFields: append([]string(nil), view.KeyFields...), SdkDeliveryEnabled: view.SDKDeliveryEnabled, SchemaVersion: view.SchemaVersion, Status: string(view.Status), ConfigRevision: revision, Audit: projectAudit(view.Audit)}
}

func projectSubscription(view application.SubscriptionView) *controlv1.Subscription {
	revision := int64(view.ConfigRevision)
	if view.ConfigRevision > math.MaxInt64 {
		revision = math.MaxInt64
	}
	return &controlv1.Subscription{Id: view.ID, ConsumerId: view.ConsumerID, Collection: view.Collection, IndexName: view.IndexName, IndexFields: append([]string(nil), view.IndexFields...), Cardinality: string(view.Cardinality), Enabled: view.Enabled, ConfigRevision: revision}
}

func projectModel(view application.ModelView) *controlv1.Model {
	revision := int64(view.ConfigRevision)
	if view.ConfigRevision > math.MaxInt64 {
		revision = math.MaxInt64
	}
	return &controlv1.Model{Code: view.Code, Name: view.Name, Collection: view.Collection, DefinitionJson: append([]byte(nil), view.Definition...), Enabled: view.Enabled, ConfigRevision: revision}
}

func projectTemplate(view application.TemplateView) *controlv1.ReleaseTemplate {
	var root struct {
		Steps []struct {
			Code          string         `json:"code"`
			Type          string         `json:"type"`
			RequiredRoles []string       `json:"requiredRoles"`
			Params        map[string]any `json:"params"`
		} `json:"steps"`
	}
	_ = json.Unmarshal(view.Document, &root)
	steps := make([]*controlv1.ReleaseStepDefinition, len(root.Steps))
	for index, step := range root.Steps {
		params := make(map[string]string, len(step.Params))
		for key, value := range step.Params {
			if text, ok := value.(string); ok {
				params[key] = text
			} else if encoded, err := json.Marshal(value); err == nil {
				params[key] = string(encoded)
			}
		}
		steps[index] = &controlv1.ReleaseStepDefinition{Code: step.Code, Type: step.Type, Sequence: int32(index), RequiredRoles: append([]string(nil), step.RequiredRoles...), Params: params}
	}
	finalEffect := commonv1.FinalEffect_FINAL_EFFECT_UNSPECIFIED
	if view.FinalEffect == release.FinalEffectBase {
		finalEffect = commonv1.FinalEffect_FINAL_EFFECT_BASE_FINAL
	} else if view.FinalEffect == release.FinalEffectOverlay {
		finalEffect = commonv1.FinalEffect_FINAL_EFFECT_OVERLAY_FINAL
	}
	return &controlv1.ReleaseTemplate{Code: view.Code, Name: view.Name, ModelCode: view.ModelCode, ReleaseTypeCode: view.ReleaseTypeCode, Version: view.Version, FinalEffect: finalEffect, SchedulingAllowed: view.SchedulingAllowed, MaxScheduleWindowSeconds: view.MaxScheduleWindowSeconds, Steps: steps, AllowedRoles: append([]string(nil), view.AllowedRoles...), Enabled: view.Enabled}
}

func projectAudit(stamp application.AuditStamp) *commonv1.AuditStamp {
	return &commonv1.AuditStamp{CreatedAt: timestamppb.New(stamp.CreatedAt), CreatedBy: stamp.CreatedBy, UpdatedAt: timestamppb.New(stamp.UpdatedAt), UpdatedBy: stamp.UpdatedBy}
}

func projectPage(number, size int, total int64, pages int) *commonv1.PageResponse {
	return &commonv1.PageResponse{Number: int32(number), Size: int32(size), TotalNumber: total, TotalPages: int64(pages)}
}

func pageRequest(page *commonv1.PageRequest) (int, int) {
	if page == nil {
		return 0, 0
	}
	return int(page.GetNumber()), int(page.GetSize())
}

func fieldTypeFromProto(value commonv1.FieldType) (catalog.FieldType, bool) {
	switch value {
	case commonv1.FieldType_FIELD_TYPE_STRING:
		return catalog.FieldTypeString, true
	case commonv1.FieldType_FIELD_TYPE_INT64:
		return catalog.FieldTypeInt64, true
	case commonv1.FieldType_FIELD_TYPE_FLOAT64:
		return catalog.FieldTypeFloat64, true
	case commonv1.FieldType_FIELD_TYPE_BOOL:
		return catalog.FieldTypeBool, true
	case commonv1.FieldType_FIELD_TYPE_TIMESTAMP:
		return catalog.FieldTypeTimestamp, true
	case commonv1.FieldType_FIELD_TYPE_JSON:
		return catalog.FieldTypeJSON, true
	default:
		return "", false
	}
}

func fieldTypeToProto(value catalog.FieldType) commonv1.FieldType {
	values := map[catalog.FieldType]commonv1.FieldType{catalog.FieldTypeString: commonv1.FieldType_FIELD_TYPE_STRING, catalog.FieldTypeInt64: commonv1.FieldType_FIELD_TYPE_INT64, catalog.FieldTypeFloat64: commonv1.FieldType_FIELD_TYPE_FLOAT64, catalog.FieldTypeBool: commonv1.FieldType_FIELD_TYPE_BOOL, catalog.FieldTypeTimestamp: commonv1.FieldType_FIELD_TYPE_TIMESTAMP, catalog.FieldTypeJSON: commonv1.FieldType_FIELD_TYPE_JSON}
	return values[value]
}

func catalogError(err error) error {
	switch {
	case errors.Is(err, application.ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, application.ErrForbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, application.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, application.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, application.ErrAborted):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, application.ErrFailedPrecondition):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, "catalog operation failed")
	}
}

func cloneString(source *string) *string {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}

func cloneMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

var _ controlv1.CatalogAdminService = (*Handler)(nil)

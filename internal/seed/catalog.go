package seed

import (
	"context"
	"errors"
	"fmt"

	"github.com/asherzj/financial_configuration_center/internal/catalog/application"
	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
)

const (
	demoCollection = "payment_routes"
	demoModel      = "payment_routes_admin"
	demoTemplate   = "payment_routes_direct"
	demoConsumer   = "payment_service"
)

type Catalog interface {
	GetCollection(context.Context, application.Principal, string) (application.CollectionView, error)
	CreateCollection(context.Context, application.Principal, application.CollectionInput) (application.CollectionView, error)
	GetModel(context.Context, application.Principal, string) (application.ModelView, error)
	CreateModel(context.Context, application.Principal, application.ModelInput) (application.ModelView, error)
	ListSubscriptions(context.Context, application.Principal, application.SubscriptionQuery) (application.SubscriptionPage, error)
	CreateSubscription(context.Context, application.Principal, application.SubscriptionInput) (application.SubscriptionView, error)
	ListTemplates(context.Context, application.Principal, application.TemplateQuery) (application.TemplatePage, error)
	CreateTemplate(context.Context, application.Principal, application.TemplateInput) (application.TemplateView, error)
}

func CatalogDemo(ctx context.Context, service Catalog) error {
	if service == nil {
		return errors.New("seed catalog service is required")
	}
	principal := application.Principal{Subject: "finconfig-seed", DisplayName: "FinConfig Seed", Roles: []string{application.ConfigAdminRole}}
	if _, err := service.GetCollection(ctx, principal, demoCollection); errors.Is(err, application.ErrNotFound) {
		if _, err := service.CreateCollection(ctx, principal, demoCollectionInput()); err != nil && !errors.Is(err, application.ErrAlreadyExists) {
			return fmt.Errorf("seed collection: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect seed collection: %w", err)
	}
	if _, err := service.GetModel(ctx, principal, demoModel); errors.Is(err, application.ErrNotFound) {
		if _, err := service.CreateModel(ctx, principal, demoModelInput()); err != nil && !errors.Is(err, application.ErrAlreadyExists) {
			return fmt.Errorf("seed model: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect seed model: %w", err)
	}
	subscriptions, err := service.ListSubscriptions(ctx, principal, application.SubscriptionQuery{ConsumerID: demoConsumer, Collection: demoCollection, PageNumber: 1, PageSize: 100})
	if err != nil {
		return fmt.Errorf("inspect seed subscription: %w", err)
	}
	foundSubscription := false
	for _, item := range subscriptions.Subscriptions {
		foundSubscription = foundSubscription || item.IndexName == "by_code"
	}
	if !foundSubscription {
		if _, err := service.CreateSubscription(ctx, principal, application.SubscriptionInput{ConsumerID: demoConsumer, Collection: demoCollection, IndexName: "by_code", IndexFields: []string{"code"}, Cardinality: application.CardinalityOneToOne, Enabled: true}); err != nil && !errors.Is(err, application.ErrAlreadyExists) {
			return fmt.Errorf("seed subscription: %w", err)
		}
	}
	templates, err := service.ListTemplates(ctx, principal, application.TemplateQuery{ModelCode: demoModel, PageNumber: 1, PageSize: 100})
	if err != nil {
		return fmt.Errorf("inspect seed template: %w", err)
	}
	foundTemplate := false
	for _, item := range templates.Templates {
		foundTemplate = foundTemplate || item.Code == demoTemplate
	}
	if !foundTemplate {
		if _, err := service.CreateTemplate(ctx, principal, demoTemplateInput()); err != nil && !errors.Is(err, application.ErrAlreadyExists) {
			return fmt.Errorf("seed release template: %w", err)
		}
	}
	return nil
}

func demoCollectionInput() application.CollectionInput {
	return application.CollectionInput{
		Name: demoCollection, Description: "Payment route policy demo", SDKDeliveryEnabled: true, SchemaVersion: 1, Status: application.StatusEnabled,
		KeyFields: []string{"code"},
		Fields: []catalog.FieldDefinition{
			{Name: "code", DisplayName: "Route code", Type: catalog.FieldTypeString, Required: true, Description: "Stable payment route code", DisplayOrder: 0},
			{Name: "channel", DisplayName: "Channel", Type: catalog.FieldTypeString, Required: true, Description: "Payment network", DisplayOrder: 1},
			{Name: "priority", DisplayName: "Priority", Type: catalog.FieldTypeInt64, Required: true, Description: "Lower values are preferred", DisplayOrder: 2},
			{Name: "enabled", DisplayName: "Enabled", Type: catalog.FieldTypeBool, Required: true, Description: "Whether the route is eligible", DisplayOrder: 3},
			{Name: "credential", DisplayName: "Credential", Type: catalog.FieldTypeString, Sensitive: true, Description: "Masked provider credential", DisplayOrder: 4},
		},
	}
}

func demoModelInput() application.ModelInput {
	return application.ModelInput{Code: demoModel, Name: "Payment routes", Collection: demoCollection, Enabled: true, Definition: []byte(`{
  "fields": [
    {"name":"code","type":"STRING","required":true,"sensitive":false,"editable":false,"queryable":true,"uiControl":"INPUT","allowedFilterOperators":["EXACT","IN"],"validationRules":[]},
    {"name":"channel","type":"STRING","required":true,"sensitive":false,"editable":true,"queryable":true,"uiControl":"SELECT","allowedFilterOperators":["EXACT","IN"],"optionSource":{"kind":"STATIC","staticOptions":[{"code":"MASTERCARD","label":"Mastercard","disabled":false},{"code":"VISA","label":"Visa","disabled":false}]},"validationRules":[]},
    {"name":"priority","type":"INT64","required":true,"sensitive":false,"editable":true,"queryable":true,"uiControl":"NUMBER","allowedFilterOperators":["EXACT","CLOSED_RANGE"],"validationRules":[]},
    {"name":"enabled","type":"BOOL","required":true,"sensitive":false,"editable":true,"queryable":true,"uiControl":"BOOLEAN","allowedFilterOperators":["EXACT"],"validationRules":[]},
    {"name":"credential","type":"STRING","required":false,"sensitive":true,"editable":true,"queryable":false,"uiControl":"INPUT","allowedFilterOperators":[],"validationRules":[]}
  ],
  "projectionFields": ["code","channel","priority","enabled","credential"],
  "keyFields": ["code"],
  "defaultPageSize": 20,
  "maxPageSize": 100,
  "releaseTypes": [{"code":"direct","name":"Direct release","templateCode":"payment_routes_direct","enabled":true}],
  "autoFillRules": []
}`)}
}

func demoTemplateInput() application.TemplateInput {
	return application.TemplateInput{
		Code: demoTemplate, Name: "Reviewed direct release", ModelCode: demoModel, ReleaseTypeCode: "direct", FinalEffect: release.FinalEffectBase,
		AllowedRoles: []string{"RELEASE_CREATOR", "RELEASE_APPROVER", "RELEASE_OPERATOR"}, Enabled: true,
		Document: []byte(`{"steps":[{"code":"review","type":"MANUAL_REVIEW","requiredRoles":["RELEASE_APPROVER"],"params":{"selfApprovalPolicy":"DENY_PRODUCTION"}},{"code":"apply","type":"BASE_APPLY","requiredRoles":["RELEASE_OPERATOR"],"params":{"cleanupScopeOverlay":true}},{"code":"complete","type":"COMPLETE","requiredRoles":[],"params":{}}]}`),
	}
}

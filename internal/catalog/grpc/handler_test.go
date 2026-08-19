package grpc_test

import (
	"context"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/catalog/application"
	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	cataloggrpc "github.com/asherzj/financial_configuration_center/internal/catalog/grpc"
	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	controlv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/control/v1"
)

func TestCatalogHandlerMapsCollectionCASAndSubscriptionFilters(t *testing.T) {
	t.Parallel()
	app := &applicationStub{
		collection:    application.CollectionView{Name: "routes", Fields: []catalog.FieldDefinition{{Name: "code", DisplayName: "Code", Type: catalog.FieldTypeString, Required: true}}, KeyFields: []string{"code"}, SchemaVersion: 1, Status: application.StatusEnabled, ConfigRevision: 8},
		subscriptions: application.SubscriptionPage{Subscriptions: []application.SubscriptionView{{SubscriptionInput: application.SubscriptionInput{ID: "subscription", ConsumerID: "checkout", Collection: "routes", IndexName: "by-code", IndexFields: []string{"code"}, Cardinality: application.CardinalityOneToOne, Enabled: true}, ConfigRevision: 9}}, PageNumber: 1, PageSize: 20, TotalNumber: 1, TotalPages: 1},
	}
	handler, err := cataloggrpc.New(app, principalResolver{})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := handler.UpdateCollection(context.Background(), &controlv1.UpdateCollectionRequest{
		ExpectedCollectionRevision: 7,
		Collection:                 &controlv1.Collection{Name: "routes", Fields: []*controlv1.FieldDefinition{{Name: "code", DisplayName: "Code", Type: commonv1.FieldType_FIELD_TYPE_STRING, IsRequired: true}}, KeyFields: []string{"code"}, SchemaVersion: 1, Status: "ENABLED"},
	})
	if err != nil || updated.Collection.ConfigRevision != 8 || app.expectedRevision != 7 || app.principal.Subject != "admin" || app.input.Fields[0].Type != catalog.FieldTypeString {
		t.Fatalf("updated=%+v principal=%+v input=%+v expected=%d err=%v", updated, app.principal, app.input, app.expectedRevision, err)
	}
	consumer := "checkout"
	listed, err := handler.ListSubscriptions(context.Background(), &controlv1.ListSubscriptionsRequest{ConsumerId: &consumer, Page: &commonv1.PageRequest{Number: int32Pointer(1), Size: int32Pointer(20)}})
	if err != nil || len(listed.Subscriptions) != 1 || listed.Subscriptions[0].ConfigRevision != 9 || app.subscriptionQuery.ConsumerID != "checkout" {
		t.Fatalf("listed=%+v query=%+v err=%v", listed, app.subscriptionQuery, err)
	}
}

type applicationStub struct {
	collection        application.CollectionView
	subscriptions     application.SubscriptionPage
	principal         application.Principal
	input             application.CollectionInput
	expectedRevision  catalog.ConfigRevision
	subscriptionQuery application.SubscriptionQuery
}

func (stub *applicationStub) CreateCollection(_ context.Context, principal application.Principal, input application.CollectionInput) (application.CollectionView, error) {
	stub.principal, stub.input = principal, input
	return stub.collection, nil
}
func (stub *applicationStub) UpdateCollection(_ context.Context, principal application.Principal, expected catalog.ConfigRevision, input application.CollectionInput) (application.CollectionView, error) {
	stub.principal, stub.expectedRevision, stub.input = principal, expected, input
	return stub.collection, nil
}
func (stub *applicationStub) GetCollection(context.Context, application.Principal, string) (application.CollectionView, error) {
	return stub.collection, nil
}
func (stub *applicationStub) ListCollections(context.Context, application.Principal, application.PageQuery) (application.CollectionPage, error) {
	return application.CollectionPage{}, nil
}
func (stub *applicationStub) CreateSubscription(context.Context, application.Principal, application.SubscriptionInput) (application.SubscriptionView, error) {
	return application.SubscriptionView{}, nil
}
func (stub *applicationStub) UpdateSubscription(context.Context, application.Principal, catalog.ConfigRevision, application.SubscriptionInput) (application.SubscriptionView, error) {
	return application.SubscriptionView{}, nil
}
func (stub *applicationStub) ListSubscriptions(_ context.Context, _ application.Principal, query application.SubscriptionQuery) (application.SubscriptionPage, error) {
	stub.subscriptionQuery = query
	return stub.subscriptions, nil
}
func (*applicationStub) PreviewModel(context.Context, application.Principal, application.ModelInput) (application.ModelPreview, error) {
	return application.ModelPreview{}, nil
}
func (*applicationStub) CreateModel(context.Context, application.Principal, application.ModelInput) (application.ModelView, error) {
	return application.ModelView{}, nil
}
func (*applicationStub) UpdateModel(context.Context, application.Principal, catalog.ConfigRevision, application.ModelInput) (application.ModelView, error) {
	return application.ModelView{}, nil
}
func (*applicationStub) GetModel(context.Context, application.Principal, string) (application.ModelView, error) {
	return application.ModelView{}, nil
}
func (*applicationStub) ListModels(context.Context, application.Principal, application.ModelQuery) (application.ModelPage, error) {
	return application.ModelPage{}, nil
}
func (*applicationStub) CreateTemplate(context.Context, application.Principal, application.TemplateInput) (application.TemplateView, error) {
	return application.TemplateView{}, nil
}
func (*applicationStub) GetTemplate(context.Context, application.Principal, string, int64) (application.TemplateView, error) {
	return application.TemplateView{}, nil
}
func (*applicationStub) ListTemplates(context.Context, application.Principal, application.TemplateQuery) (application.TemplatePage, error) {
	return application.TemplatePage{}, nil
}

type principalResolver struct{}

func (principalResolver) Subject(context.Context) (string, error)     { return "admin", nil }
func (principalResolver) DisplayName(context.Context) (string, error) { return "Admin", nil }
func (principalResolver) Roles(context.Context) ([]string, error) {
	return []string{application.ConfigAdminRole}, nil
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }
func int32Pointer(value int32) *int32   { return &value }

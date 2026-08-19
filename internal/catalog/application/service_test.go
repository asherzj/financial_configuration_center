package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/catalog/application"
	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
)

func TestCollectionCommandsCompileAuthorizeAndPreserveCAS(t *testing.T) {
	t.Parallel()
	repository := &repositoryStub{}
	service, err := application.NewService(repository, fixedClock{time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	principal := application.Principal{Subject: "admin", DisplayName: "Admin", Roles: []string{application.ConfigAdminRole}}
	request := application.CollectionInput{
		Name: "routes", Description: "Routes", Fields: []catalog.FieldDefinition{{Name: "code", DisplayName: "Code", Type: catalog.FieldTypeString, Required: true}},
		KeyFields: []string{"code"}, SDKDeliveryEnabled: true, SchemaVersion: 1, Status: application.StatusEnabled,
	}
	created, err := service.CreateCollection(context.Background(), principal, request)
	if err != nil || repository.created.Definition.Name() != "routes" || repository.created.Actor.Subject != "admin" || created.Name != "routes" {
		t.Fatalf("created=%+v mutation=%+v err=%v", created, repository.created, err)
	}
	request.Description = "Updated"
	if _, err := service.UpdateCollection(context.Background(), principal, 0, request); !errors.Is(err, application.ErrInvalid) {
		t.Fatalf("zero expected revision = %v", err)
	}
	if _, err := service.UpdateCollection(context.Background(), principal, 7, request); err != nil || repository.updated.ExpectedRevision != 7 {
		t.Fatalf("update mutation=%+v err=%v", repository.updated, err)
	}
	if _, err := service.CreateCollection(context.Background(), application.Principal{Subject: "viewer", Roles: []string{application.ConfigViewerRole}}, request); !errors.Is(err, application.ErrForbidden) {
		t.Fatalf("viewer create = %v", err)
	}
}

func TestSubscriptionCommandsValidateIndexAndBoundLists(t *testing.T) {
	t.Parallel()
	repository := &repositoryStub{}
	service, _ := application.NewService(repository, fixedClock{time.Now()})
	principal := application.Principal{Subject: "admin", Roles: []string{application.ConfigAdminRole}}
	input := application.SubscriptionInput{ConsumerID: "consumer", Collection: "routes", IndexName: "by-code", IndexFields: []string{"code"}, Cardinality: application.CardinalityOneToOne, Enabled: true}
	if _, err := service.CreateSubscription(context.Background(), principal, input); err != nil || repository.createdSubscription.ConsumerID != "consumer" {
		t.Fatalf("create=%+v err=%v", repository.createdSubscription, err)
	}
	input.ID = "subscription"
	if _, err := service.UpdateSubscription(context.Background(), principal, 4, input); err != nil || repository.updatedSubscription.ExpectedRevision != 4 {
		t.Fatalf("update=%+v err=%v", repository.updatedSubscription, err)
	}
	if _, err := service.ListSubscriptions(context.Background(), principal, application.SubscriptionQuery{PageSize: 101}); !errors.Is(err, application.ErrInvalid) {
		t.Fatalf("unbounded list = %v", err)
	}
}

type repositoryStub struct {
	created             application.CollectionMutation
	updated             application.CollectionMutation
	createdSubscription application.SubscriptionMutation
	updatedSubscription application.SubscriptionMutation
}

func (stub *repositoryStub) CreateCollection(_ context.Context, mutation application.CollectionMutation) (application.CollectionView, error) {
	stub.created = mutation
	return application.CollectionView{Name: mutation.Definition.Name()}, nil
}
func (stub *repositoryStub) UpdateCollection(_ context.Context, mutation application.CollectionMutation) (application.CollectionView, error) {
	stub.updated = mutation
	return application.CollectionView{Name: mutation.Definition.Name()}, nil
}
func (*repositoryStub) GetCollection(context.Context, string) (application.CollectionView, error) {
	return application.CollectionView{}, nil
}
func (*repositoryStub) ListCollections(context.Context, application.PageQuery) (application.CollectionPage, error) {
	return application.CollectionPage{}, nil
}
func (stub *repositoryStub) CreateSubscription(_ context.Context, mutation application.SubscriptionMutation) (application.SubscriptionView, error) {
	stub.createdSubscription = mutation
	return application.SubscriptionView{SubscriptionInput: application.SubscriptionInput{ConsumerID: mutation.ConsumerID}}, nil
}
func (stub *repositoryStub) UpdateSubscription(_ context.Context, mutation application.SubscriptionMutation) (application.SubscriptionView, error) {
	stub.updatedSubscription = mutation
	return application.SubscriptionView{SubscriptionInput: application.SubscriptionInput{ID: mutation.ID}}, nil
}
func (*repositoryStub) ListSubscriptions(context.Context, application.SubscriptionQuery) (application.SubscriptionPage, error) {
	return application.SubscriptionPage{}, nil
}
func (*repositoryStub) PreviewModel(context.Context, application.ModelInput) (application.ModelPreview, error) {
	return application.ModelPreview{Valid: true}, nil
}
func (*repositoryStub) CreateModel(context.Context, application.ModelMutation) (application.ModelView, error) {
	return application.ModelView{}, nil
}
func (*repositoryStub) UpdateModel(context.Context, application.ModelMutation) (application.ModelView, error) {
	return application.ModelView{}, nil
}
func (*repositoryStub) GetModel(context.Context, string) (application.ModelView, error) {
	return application.ModelView{}, nil
}
func (*repositoryStub) ListModels(context.Context, application.ModelQuery) (application.ModelPage, error) {
	return application.ModelPage{}, nil
}
func (*repositoryStub) CreateTemplate(context.Context, application.TemplateMutation) (application.TemplateView, error) {
	return application.TemplateView{}, nil
}
func (*repositoryStub) GetTemplate(context.Context, string, int64) (application.TemplateView, error) {
	return application.TemplateView{}, nil
}
func (*repositoryStub) ListTemplates(context.Context, application.TemplateQuery) (application.TemplatePage, error) {
	return application.TemplatePage{}, nil
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

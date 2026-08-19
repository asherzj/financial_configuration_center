package seed

import (
	"context"
	"testing"

	"github.com/asherzj/financial_configuration_center/internal/catalog/application"
)

func TestCatalogDemoIsIdempotent(t *testing.T) {
	t.Parallel()
	stub := &catalogStub{}
	if err := CatalogDemo(context.Background(), stub); err != nil {
		t.Fatal(err)
	}
	if err := CatalogDemo(context.Background(), stub); err != nil {
		t.Fatal(err)
	}
	if stub.collections != 1 || stub.models != 1 || stub.subscriptions != 1 || stub.templates != 1 {
		t.Fatalf("create counts: collections=%d models=%d subscriptions=%d templates=%d", stub.collections, stub.models, stub.subscriptions, stub.templates)
	}
}

type catalogStub struct{ collections, models, subscriptions, templates int }

func (stub *catalogStub) GetCollection(context.Context, application.Principal, string) (application.CollectionView, error) {
	if stub.collections == 0 {
		return application.CollectionView{}, application.ErrNotFound
	}
	return application.CollectionView{Name: demoCollection}, nil
}
func (stub *catalogStub) CreateCollection(_ context.Context, _ application.Principal, _ application.CollectionInput) (application.CollectionView, error) {
	stub.collections++
	return application.CollectionView{Name: demoCollection}, nil
}
func (stub *catalogStub) GetModel(context.Context, application.Principal, string) (application.ModelView, error) {
	if stub.models == 0 {
		return application.ModelView{}, application.ErrNotFound
	}
	return application.ModelView{ModelInput: application.ModelInput{Code: demoModel}}, nil
}
func (stub *catalogStub) CreateModel(_ context.Context, _ application.Principal, _ application.ModelInput) (application.ModelView, error) {
	stub.models++
	return application.ModelView{}, nil
}
func (stub *catalogStub) ListSubscriptions(context.Context, application.Principal, application.SubscriptionQuery) (application.SubscriptionPage, error) {
	page := application.SubscriptionPage{}
	if stub.subscriptions > 0 {
		page.Subscriptions = []application.SubscriptionView{{SubscriptionInput: application.SubscriptionInput{IndexName: "by_code"}}}
	}
	return page, nil
}
func (stub *catalogStub) CreateSubscription(_ context.Context, _ application.Principal, _ application.SubscriptionInput) (application.SubscriptionView, error) {
	stub.subscriptions++
	return application.SubscriptionView{}, nil
}
func (stub *catalogStub) ListTemplates(context.Context, application.Principal, application.TemplateQuery) (application.TemplatePage, error) {
	page := application.TemplatePage{}
	if stub.templates > 0 {
		page.Templates = []application.TemplateView{{TemplateInput: application.TemplateInput{Code: demoTemplate}}}
	}
	return page, nil
}
func (stub *catalogStub) CreateTemplate(_ context.Context, _ application.Principal, _ application.TemplateInput) (application.TemplateView, error) {
	stub.templates++
	return application.TemplateView{}, nil
}

package audit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/asherzj/financial_configuration_center/internal/audit"
)

func TestAuditServiceRequiresViewerAndBoundsPages(t *testing.T) {
	t.Parallel()
	repository := &auditRepositoryStub{page: audit.Page{TotalNumber: 1}}
	service, err := audit.NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(context.Background(), audit.Principal{Subject: "viewer"}, audit.Query{}); !errors.Is(err, audit.ErrForbidden) {
		t.Fatalf("unauthorized list = %v", err)
	}
	page, err := service.List(context.Background(), audit.Principal{Subject: "viewer", Roles: []string{audit.AuditorRole}}, audit.Query{ResourceType: " RELEASE_ORDER "})
	if err != nil || repository.last.PageNumber != 1 || repository.last.PageSize != 20 || repository.last.ResourceType != "RELEASE_ORDER" || page.TotalNumber != 1 {
		t.Fatalf("list = %+v query=%+v err=%v", page, repository.last, err)
	}
	if _, err := service.List(context.Background(), audit.Principal{Subject: "viewer", Roles: []string{audit.AuditorRole}}, audit.Query{PageSize: 101}); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("unbounded list = %v", err)
	}
}

type auditRepositoryStub struct {
	page audit.Page
	last audit.Query
}

func (stub *auditRepositoryStub) List(_ context.Context, query audit.Query) (audit.Page, error) {
	stub.last = query
	return stub.page, nil
}

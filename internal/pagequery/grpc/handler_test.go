package grpc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	commonv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1"
	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	"github.com/asherzj/financial_configuration_center/internal/pagequery"
	pagegrpc "github.com/asherzj/financial_configuration_center/internal/pagequery/grpc"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestQueryPageMapsCompleteAllMetadata(t *testing.T) {
	t.Parallel()

	application := &stubQuerier{result: pagequery.Result{
		ModelCode: "model", ModelName: "Model", QueryType: pagequery.TypeAll,
		Rows: []pagequery.Row{{
			RecordKey: "key", RecordRevision: 8, Values: map[string]string{"code": "effective"},
			BasePresent: true, BaseValues: map[string]string{"code": "base"}, ChangedFields: []string{"code"},
		}},
		ProjectionFields: []string{"code"},
		InteractionFields: []pagequery.InteractionField{{
			Name: "code", DisplayName: "Code", Type: catalog.FieldTypeString, UIControl: catalog.UIControlSelect,
			Queryable: true, Editable: true, Required: true, Projected: true, KeyField: true,
			AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}, DefaultFilterOperator: catalog.FilterExact,
			AutoFill:        &catalog.AutoFillRule{Field: "code", Source: catalog.AutoFillUUID},
			ValidationRules: []catalog.ValidationRule{{Kind: catalog.ValidationRegex, Params: map[string]string{"pattern": "^[a-z]+$"}, Message: "lowercase only"}},
			Options:         []catalog.SelectOptionDefinition{{Code: "active", Label: "Active"}, {Code: "legacy", Label: "Legacy", Disabled: true}},
		}},
		ReleaseTypes: []pagequery.ReleaseType{{Code: "direct", Name: "Direct", TemplateCode: "base-final", Available: true}},
		PageNumber:   1, PageSize: 20, TotalNumber: 1, TotalPages: 1,
		Snapshot:      snapshot.Identity{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance", Generation: 1, PublishedAt: time.Now().UTC()},
		ModelRevision: 7, CollectionRevision: 8,
	}}
	handler, err := pagegrpc.New(application, allowPageQueryAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	response, err := handler.QueryPage(t.Context(), &configv1.QueryPageRequest{
		ModelCode: "model", Scope: &commonv1.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		QueryType: commonv1.QueryPageType_QUERY_PAGE_TYPE_ALL, PreviewBucket: int32Pointer(42),
		Conditions: []*configv1.FilterCondition{{Field: "code", Operator: commonv1.FilterOperator_FILTER_OPERATOR_EXACT, Value: &configv1.ScalarValue{Type: commonv1.FieldType_FIELD_TYPE_STRING, Canonical: "active"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Rows) != 1 || len(response.InteractionFields) != 1 || !response.InteractionFields[0].Projected || response.InteractionFields[0].DefaultFilterOperator != commonv1.FilterOperator_FILTER_OPERATOR_EXACT || len(response.ReleaseTypes) != 1 || response.ReleaseTypes[0].TemplateCode != "base-final" {
		t.Fatalf("QueryPage response = %+v", response)
	}
	if response.ModelRevision != 7 || response.CollectionRevision != 8 || response.Page.Size != 20 {
		t.Fatalf("QueryPage authority = %+v", response)
	}
	if len(response.InteractionFields[0].Options) != 2 || !response.InteractionFields[0].Options[1].Disabled {
		t.Fatalf("QueryPage options = %+v", response.InteractionFields[0].Options)
	}
	if response.InteractionFields[0].AutoFill == nil || response.InteractionFields[0].AutoFill.Source != "UUID" || len(response.InteractionFields[0].ValidationRules) != 1 || response.InteractionFields[0].ValidationRules[0].Params["pattern"] != "^[a-z]+$" {
		t.Fatalf("QueryPage interaction contract = %+v", response.InteractionFields[0])
	}
	if !response.Rows[0].BasePresent || response.Rows[0].BaseValues["code"] != "base" || len(response.Rows[0].ChangedFields) != 1 {
		t.Fatalf("QueryPage row diff = %+v", response.Rows[0])
	}
	if application.last.Region != "cn" || application.last.Stage != "blue" || application.last.PreviewBucket == nil || *application.last.PreviewBucket != 42 {
		t.Fatalf("QueryPage scope request = %+v", application.last)
	}
	if len(application.last.Conditions) != 1 || application.last.Conditions[0].Value == nil || application.last.Conditions[0].Value.Canonical != "active" {
		t.Fatalf("QueryPage conditions = %+v", application.last.Conditions)
	}
}

func TestQueryPageMapsManagedEnvironmentMismatchToFailedPrecondition(t *testing.T) {
	t.Parallel()
	handler, err := pagegrpc.New(&stubQuerier{err: pagequery.ErrManagedEnvironmentMismatch}, allowPageQueryAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.QueryPage(t.Context(), &configv1.QueryPageRequest{
		ModelCode: "model", Scope: &commonv1.Scope{Region: "cn", Environment: "staging"},
		QueryType: commonv1.QueryPageType_QUERY_PAGE_TYPE_ONLY_DATA,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("managed-environment mismatch code = %s, error = %v", status.Code(err), err)
	}
}

func TestQueryPageMapsMissingSnapshotToUnavailable(t *testing.T) {
	t.Parallel()
	handler, err := pagegrpc.New(&stubQuerier{err: pagequery.ErrSnapshotUnavailable}, allowPageQueryAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.QueryPage(t.Context(), &configv1.QueryPageRequest{
		ModelCode: "model", Scope: &commonv1.Scope{Region: "cn", Environment: "production"},
		QueryType: commonv1.QueryPageType_QUERY_PAGE_TYPE_ONLY_DATA,
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("missing snapshot code = %s, error = %v", status.Code(err), err)
	}
}

func TestQueryPageMapsMissingModelToNotFound(t *testing.T) {
	t.Parallel()
	handler, err := pagegrpc.New(&stubQuerier{err: pagequery.ErrNotFound}, allowPageQueryAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.QueryPage(t.Context(), &configv1.QueryPageRequest{
		ModelCode: "missing-model", Scope: &commonv1.Scope{Region: "cn", Environment: "production"},
		QueryType: commonv1.QueryPageType_QUERY_PAGE_TYPE_ONLY_DATA,
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("missing model code = %s, error = %v", status.Code(err), err)
	}
}

func TestQueryPageAuthorizesRoleAndScopeBeforeApplication(t *testing.T) {
	t.Parallel()
	denied := errors.New("page query denied")
	authorizer := &recordingPageQueryAuthorizer{err: denied}
	application := &stubQuerier{}
	handler, err := pagegrpc.New(application, authorizer, "production")
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.QueryPage(context.Background(), &configv1.QueryPageRequest{
		ModelCode: "model", Scope: &commonv1.Scope{Region: " cn ", Environment: " production ", Stage: " blue "},
	})
	if !errors.Is(err, denied) || application.calls != 0 || authorizer.scope != (platformauth.Scope{Region: "cn", Environment: "production", Stage: "blue"}) {
		t.Fatalf("error=%v calls=%d scope=%+v", err, application.calls, authorizer.scope)
	}
}

func TestQueryPageRejectsWildcardAndCrossEnvironmentBeforeAuthorization(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		scope *commonv1.Scope
		code  codes.Code
	}{
		"wildcard": {scope: &commonv1.Scope{Region: "cn", Environment: "production", Stage: "*"}, code: codes.InvalidArgument},
		"routing":  {scope: &commonv1.Scope{Region: "cn", Environment: "staging"}, code: codes.FailedPrecondition},
	} {
		authorizer := &recordingPageQueryAuthorizer{}
		application := &stubQuerier{}
		handler, err := pagegrpc.New(application, authorizer, "production")
		if err != nil {
			t.Fatal(err)
		}
		_, err = handler.QueryPage(context.Background(), &configv1.QueryPageRequest{ModelCode: "model", Scope: test.scope})
		if status.Code(err) != test.code || application.calls != 0 || authorizer.scope != (platformauth.Scope{}) {
			t.Fatalf("%s code=%v calls=%d authorized=%+v", name, status.Code(err), application.calls, authorizer.scope)
		}
	}
}

type stubQuerier struct {
	result pagequery.Result
	last   pagequery.Request
	err    error
	calls  int
}

func (querier *stubQuerier) Query(request pagequery.Request) (pagequery.Result, error) {
	querier.calls++
	querier.last = request
	return querier.result, querier.err
}

func int32Pointer(value int32) *int32 { return &value }

type allowPageQueryAuthorizer struct{}

func (allowPageQueryAuthorizer) AuthorizePageQuery(context.Context, platformauth.Scope) error {
	return nil
}

type recordingPageQueryAuthorizer struct {
	err   error
	scope platformauth.Scope
}

func (authorizer *recordingPageQueryAuthorizer) AuthorizePageQuery(_ context.Context, scope platformauth.Scope) error {
	authorizer.scope = scope
	return authorizer.err
}

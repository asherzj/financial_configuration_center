package grpc_test

import (
	"testing"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	"github.com/asherzj/financial_configuration_center/internal/pagequery"
	pagegrpc "github.com/asherzj/financial_configuration_center/internal/pagequery/grpc"
	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/config/v1"
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
	handler, err := pagegrpc.New(application)
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

type stubQuerier struct {
	result pagequery.Result
	last   pagequery.Request
}

func (querier *stubQuerier) Query(request pagequery.Request) (pagequery.Result, error) {
	querier.last = request
	return querier.result, nil
}

func int32Pointer(value int32) *int32 { return &value }

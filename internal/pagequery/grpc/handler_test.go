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

	application := stubQuerier{result: pagequery.Result{
		ModelCode: "model", ModelName: "Model", QueryType: pagequery.TypeAll,
		Rows:             []pagequery.Row{{RecordKey: "key", RecordRevision: 8, Values: map[string]string{"code": "a"}}},
		ProjectionFields: []string{"code"},
		InteractionFields: []pagequery.InteractionField{{
			Name: "code", DisplayName: "Code", Type: catalog.FieldTypeString, UIControl: catalog.UIControlInput,
			Queryable: true, Editable: true, Required: true, Projected: true, KeyField: true,
			AllowedFilterOperators: []catalog.FilterOperator{catalog.FilterExact}, DefaultFilterOperator: catalog.FilterExact,
		}},
		PageNumber: 1, PageSize: 20, TotalNumber: 1, TotalPages: 1,
		Snapshot:      snapshot.Identity{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance", Generation: 1, PublishedAt: time.Now().UTC()},
		ModelRevision: 7, CollectionRevision: 8,
	}}
	handler, err := pagegrpc.New(application)
	if err != nil {
		t.Fatal(err)
	}
	response, err := handler.QueryPage(t.Context(), &configv1.QueryPageRequest{
		ModelCode: "model", Scope: &commonv1.Scope{Environment: "production"}, QueryType: commonv1.QueryPageType_QUERY_PAGE_TYPE_ALL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Rows) != 1 || len(response.InteractionFields) != 1 || !response.InteractionFields[0].Projected || response.InteractionFields[0].DefaultFilterOperator != commonv1.FilterOperator_FILTER_OPERATOR_EXACT {
		t.Fatalf("QueryPage response = %+v", response)
	}
	if response.ModelRevision != 7 || response.CollectionRevision != 8 || response.Page.Size != 20 {
		t.Fatalf("QueryPage authority = %+v", response)
	}
}

type stubQuerier struct{ result pagequery.Result }

func (querier stubQuerier) Query(pagequery.Request) (pagequery.Result, error) {
	return querier.result, nil
}

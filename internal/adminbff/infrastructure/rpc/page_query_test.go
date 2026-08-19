package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	commonv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1/pagequeryservice"
	bffapp "github.com/asherzj/financial_configuration_center/internal/adminbff/application"
	"github.com/cloudwego/kitex/client/callopt"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPageQueryClientMapsCompleteRequestAndResponse(t *testing.T) {
	t.Parallel()

	publishedAt := time.Date(2026, 8, 19, 10, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	defaultValue := "active"
	unavailableReason := "DISABLED"
	response := &configv1.QueryPageResponse{
		ModelCode: "routes", ModelName: "Routes", QueryType: commonv1.QueryPageType_QUERY_PAGE_TYPE_ALL,
		Rows: []*configv1.PageRow{{
			RecordKey: "visa", RecordRevision: 9, Values: map[string]string{"status": "active"},
			MaskedFields: []string{"secret"}, BasePresent: true, BaseValues: map[string]string{"status": "pending"}, ChangedFields: []string{"status"},
		}},
		ProjectionFields: []string{"status"},
		InteractionFields: []*configv1.PageInteractionField{{
			Name: "status", DisplayName: "Status", Description: "Route status",
			Type: commonv1.FieldType_FIELD_TYPE_STRING, UiControl: commonv1.UiControlType_UI_CONTROL_TYPE_SELECT,
			Queryable: true, Editable: true, IsRequired: true, Projected: true,
			AllowedFilterOperators: []commonv1.FilterOperator{commonv1.FilterOperator_FILTER_OPERATOR_EXACT, commonv1.FilterOperator_FILTER_OPERATOR_IN},
			DefaultFilterOperator:  commonv1.FilterOperator_FILTER_OPERATOR_EXACT,
			DefaultValue:           &defaultValue, AutoFill: &configv1.AutoFillRule{Source: "CONSTANT", Value: "active"},
			ValidationRules: []*configv1.ValidationRule{{Kind: "ENUM", Params: map[string]string{"values": `["active"]`}, Message: "valid status"}},
			DisplayOrder:    2, Options: []*configv1.SelectOption{{Code: "active", Label: "Active"}},
		}},
		ReleaseTypes: []*configv1.ReleaseType{{Code: "direct", Name: "Direct", TemplateCode: "base-final", UnavailableReasonCode: &unavailableReason}},
		Page:         &commonv1.PageResponse{Number: 2, Size: 10, TotalNumber: 11, TotalPages: 2},
		Snapshot: &commonv1.SnapshotIdentity{
			ServerEpoch: "epoch", ServerInstanceId: "server", SnapshotInstance: "snapshot",
			SnapshotGeneration: 7, PublishedAt: timestamppb.New(publishedAt),
		},
		ModelRevision: 4, CollectionRevision: 8,
	}
	type contextKey struct{}
	client := &pageQueryServiceStub{query: func(ctx context.Context, request *configv1.QueryPageRequest) (*configv1.QueryPageResponse, error) {
		if ctx.Value(contextKey{}) != "trace" {
			t.Fatal("request context was not forwarded")
		}
		if request.ModelCode != "routes" || request.Scope.Region != "cn" || request.Scope.Environment != "production" || request.Scope.Stage != "gray" || request.QueryType != commonv1.QueryPageType_QUERY_PAGE_TYPE_ALL {
			t.Fatalf("request identity = %+v", request)
		}
		if request.Page == nil || request.Page.Number == nil || *request.Page.Number != 2 || request.Page.Size == nil || *request.Page.Size != 10 || request.PreviewBucket == nil || *request.PreviewBucket != 71 {
			t.Fatalf("request page = %+v preview=%v", request.Page, request.PreviewBucket)
		}
		if len(request.Conditions) != 1 || request.Conditions[0].Operator != commonv1.FilterOperator_FILTER_OPERATOR_EXACT || request.Conditions[0].Value == nil || request.Conditions[0].Value.Type != commonv1.FieldType_FIELD_TYPE_UNSPECIFIED || request.Conditions[0].Value.Canonical != "active" {
			t.Fatalf("request conditions = %+v", request.Conditions)
		}
		return response, nil
	}}
	adapter, err := NewPageQueryClient(client)
	if err != nil {
		t.Fatal(err)
	}
	pageNumber, pageSize, previewBucket := int32(2), int32(10), int32(71)
	result, err := adapter.QueryPage(context.WithValue(context.Background(), contextKey{}, "trace"), bffapp.QueryRequest{
		ModelCode: "routes", Region: "cn", Environment: "production", Stage: "gray",
		Type: bffapp.QueryTypeAll, Page: bffapp.PageSpec{Number: &pageNumber, Size: &pageSize}, PreviewBucket: &previewBucket,
		Conditions: []bffapp.FilterCondition{{Field: "status", Operator: bffapp.FilterExact, Value: &bffapp.ScalarValue{Canonical: "active"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ModelCode != "routes" || result.QueryType != bffapp.QueryTypeAll || result.Snapshot.PublishedAt != publishedAt.UTC() || result.ModelRevision != 4 || result.CollectionRevision != 8 {
		t.Fatalf("result identity = %+v", result)
	}
	if len(result.Rows) != 1 || result.Rows[0].RecordRevision != 9 || result.Rows[0].Values["status"] != "active" || len(result.InteractionFields) != 1 {
		t.Fatalf("result data = %+v", result)
	}
	field := result.InteractionFields[0]
	if field.Type != bffapp.FieldTypeString || field.UIControl != bffapp.UIControlSelect || field.DefaultFilterOperator != bffapp.FilterExact || field.AutoFill == nil || field.AutoFill.Source != bffapp.AutoFillConstant || field.ValidationRules[0].Kind != bffapp.ValidationEnum || field.Options[0].Code != "active" {
		t.Fatalf("interaction field = %+v", field)
	}
	response.Rows[0].Values["status"] = "mutated"
	response.InteractionFields[0].ValidationRules[0].Params["values"] = "mutated"
	response.ProjectionFields[0] = "mutated"
	if result.Rows[0].Values["status"] != "active" || result.InteractionFields[0].ValidationRules[0].Params["values"] != `["active"]` || result.ProjectionFields[0] != "status" {
		t.Fatalf("mapped result aliases transport response: %+v", result)
	}
}

func TestPageQueryClientRejectsInvalidLocalRequestBeforeRPC(t *testing.T) {
	t.Parallel()
	client := &pageQueryServiceStub{query: func(context.Context, *configv1.QueryPageRequest) (*configv1.QueryPageResponse, error) {
		t.Fatal("RPC must not be called")
		return nil, nil
	}}
	adapter, err := NewPageQueryClient(client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.QueryPage(context.Background(), bffapp.QueryRequest{Type: "UNKNOWN"})
	if !errors.Is(err, bffapp.ErrPageQueryInvalid) {
		t.Fatalf("error = %v", err)
	}
}

func TestPageQueryClientMapsKitexInvalidArgument(t *testing.T) {
	t.Parallel()
	adapter, err := NewPageQueryClient(&pageQueryServiceStub{query: func(context.Context, *configv1.QueryPageRequest) (*configv1.QueryPageResponse, error) {
		return nil, kitexstatus.Err(kitexcodes.InvalidArgument, "internal detail")
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.QueryPage(context.Background(), bffapp.QueryRequest{Type: bffapp.QueryTypeOnlyData})
	if !errors.Is(err, bffapp.ErrPageQueryInvalid) || err.Error() != bffapp.ErrPageQueryInvalid.Error() {
		t.Fatalf("error = %v", err)
	}
}

func TestPageQueryClientRejectsMalformedResponses(t *testing.T) {
	t.Parallel()
	valid := func() *configv1.QueryPageResponse {
		return &configv1.QueryPageResponse{
			ModelCode: "routes", QueryType: commonv1.QueryPageType_QUERY_PAGE_TYPE_ONLY_DATA,
			Page: &commonv1.PageResponse{Number: 1, Size: 20}, Snapshot: &commonv1.SnapshotIdentity{
				ServerEpoch: "epoch", ServerInstanceId: "server", SnapshotInstance: "snapshot", SnapshotGeneration: 1, PublishedAt: timestamppb.Now(),
			},
			ModelRevision: 1, CollectionRevision: 1,
		}
	}
	tests := map[string]func() *configv1.QueryPageResponse{
		"nil":           func() *configv1.QueryPageResponse { return nil },
		"wrong model":   func() *configv1.QueryPageResponse { response := valid(); response.ModelCode = "other"; return response },
		"missing page":  func() *configv1.QueryPageResponse { response := valid(); response.Page = nil; return response },
		"zero revision": func() *configv1.QueryPageResponse { response := valid(); response.ModelRevision = 0; return response },
		"wrong query type": func() *configv1.QueryPageResponse {
			response := valid()
			response.QueryType = commonv1.QueryPageType_QUERY_PAGE_TYPE_ALL
			return response
		},
		"invalid page": func() *configv1.QueryPageResponse {
			response := valid()
			response.Page.Size = -1
			return response
		},
		"nil row": func() *configv1.QueryPageResponse {
			response := valid()
			response.Rows = []*configv1.PageRow{nil}
			return response
		},
		"unknown field": func() *configv1.QueryPageResponse {
			response := valid()
			response.InteractionFields = []*configv1.PageInteractionField{{Type: 99}}
			return response
		},
		"invalid publish": func() *configv1.QueryPageResponse {
			response := valid()
			response.Snapshot.PublishedAt = &timestamppb.Timestamp{Seconds: 1 << 62}
			return response
		},
		"masked value exposed": func() *configv1.QueryPageResponse {
			response := valid()
			response.Rows = []*configv1.PageRow{{RecordKey: "visa", RecordRevision: 1, Values: map[string]string{"secret": "plaintext"}, MaskedFields: []string{"secret"}}}
			return response
		},
		"sensitive metadata value exposed": func() *configv1.QueryPageResponse {
			response := valid()
			response.Rows = []*configv1.PageRow{{RecordKey: "visa", RecordRevision: 1, BaseValues: map[string]string{"secret": "plaintext"}}}
			response.InteractionFields = []*configv1.PageInteractionField{{
				Name: "secret", Sensitive: true, Type: commonv1.FieldType_FIELD_TYPE_STRING, UiControl: commonv1.UiControlType_UI_CONTROL_TYPE_INPUT,
			}}
			return response
		},
	}
	for name, response := range tests {
		name, response := name, response
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			adapter, err := NewPageQueryClient(&pageQueryServiceStub{query: func(context.Context, *configv1.QueryPageRequest) (*configv1.QueryPageResponse, error) {
				return response(), nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.QueryPage(context.Background(), bffapp.QueryRequest{ModelCode: "routes", Type: bffapp.QueryTypeOnlyData})
			if !errors.Is(err, ErrInvalidPageQueryResponse) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestNewPageQueryClientRejectsTypedNil(t *testing.T) {
	t.Parallel()
	var client *pageQueryServiceStub
	if _, err := NewPageQueryClient(client); err == nil {
		t.Fatal("expected typed nil client rejection")
	}
}

type pageQueryServiceStub struct {
	query func(context.Context, *configv1.QueryPageRequest) (*configv1.QueryPageResponse, error)
}

func (stub *pageQueryServiceStub) QueryPage(ctx context.Context, request *configv1.QueryPageRequest, _ ...callopt.Option) (*configv1.QueryPageResponse, error) {
	return stub.query(ctx, request)
}

var _ pagequeryservice.Client = (*pageQueryServiceStub)(nil)

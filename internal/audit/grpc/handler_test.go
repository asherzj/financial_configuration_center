package grpc_test

import (
	"context"
	"testing"
	"time"

	commonv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/common/v1"
	controlv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/control/v1"
	"github.com/asherzj/financial_configuration_center/internal/audit"
	auditgrpc "github.com/asherzj/financial_configuration_center/internal/audit/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestAuditHandlerMapsFiltersPrincipalAndPayloadFreeRecords(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	queries := &queriesStub{page: audit.Page{
		Records: []audit.Record{{
			ID: 9, OccurredAt: now, PrincipalSubject: "alice", Action: "UPDATE",
			ResourceType: "COLLECTION", ResourceID: "routes", Region: "cn",
			Environment: "production", Result: "SUCCEEDED", TraceID: "trace-a",
		}},
		PageNumber: 1, PageSize: 20, TotalNumber: 1, TotalPages: 1,
	}}
	handler, err := auditgrpc.New(queries, principalResolver{})
	if err != nil {
		t.Fatal(err)
	}
	from := now.Add(-time.Hour)
	until := now.Add(time.Hour)
	response, err := handler.ListAuditRecords(context.Background(), &controlv1.ListAuditRecordsRequest{
		PrincipalSubject: stringPointer("alice"), ResourceType: stringPointer("COLLECTION"), ResourceId: stringPointer("routes"),
		From: timestamppb.New(from), Until: timestamppb.New(until),
		Page: &commonv1.PageRequest{Number: int32Pointer(1), Size: int32Pointer(20)},
	})
	if err != nil || len(response.Records) != 1 || response.Records[0].GetTraceId() != "trace-a" || response.Records[0].GetScope().GetEnvironment() != "production" {
		t.Fatalf("response = %+v, err=%v", response, err)
	}
	if queries.principal.Subject != "auditor" || len(queries.principal.Roles) != 1 || queries.query.PrincipalSubject != "alice" || queries.query.ResourceType != "COLLECTION" || queries.query.ResourceID != "routes" || queries.query.From == nil || !queries.query.From.Equal(from) || queries.query.Until == nil || !queries.query.Until.Equal(until) {
		t.Fatalf("principal=%+v query=%+v", queries.principal, queries.query)
	}
}

type queriesStub struct {
	page      audit.Page
	principal audit.Principal
	query     audit.Query
}

func (stub *queriesStub) List(_ context.Context, principal audit.Principal, query audit.Query) (audit.Page, error) {
	stub.principal, stub.query = principal, query
	return stub.page, nil
}

type principalResolver struct{}

func (principalResolver) Subject(context.Context) (string, error) { return "auditor", nil }
func (principalResolver) Roles(context.Context) ([]string, error) {
	return []string{audit.AuditorRole}, nil
}

func stringPointer(value string) *string { return &value }
func int32Pointer(value int32) *int32    { return &value }

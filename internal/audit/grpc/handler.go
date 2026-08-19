package grpc

import (
	"context"
	"errors"
	"strings"

	"github.com/asherzj/financial_configuration_center/internal/audit"
	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	controlv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/control/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Queries interface {
	List(context.Context, audit.Principal, audit.Query) (audit.Page, error)
}

type PrincipalResolver interface {
	Subject(context.Context) (string, error)
	Roles(context.Context) ([]string, error)
}

type Handler struct {
	queries    Queries
	principals PrincipalResolver
}

func New(queries Queries, principals PrincipalResolver) (*Handler, error) {
	if queries == nil || principals == nil {
		return nil, errors.New("new audit handler: queries and principal resolver are required")
	}
	return &Handler{queries: queries, principals: principals}, nil
}

func (handler *Handler) ListAuditRecords(ctx context.Context, request *controlv1.ListAuditRecordsRequest) (*controlv1.ListAuditRecordsResponse, error) {
	subject, err := handler.principals.Subject(ctx)
	if err != nil || strings.TrimSpace(subject) == "" {
		return nil, status.Error(codes.Unauthenticated, "authenticated actor is required")
	}
	roles, err := handler.principals.Roles(ctx)
	if err != nil {
		return nil, status.Error(codes.PermissionDenied, "actor roles could not be resolved")
	}
	query := audit.Query{}
	if request != nil {
		query.PrincipalSubject = request.GetPrincipalSubject()
		query.ResourceType = request.GetResourceType()
		query.ResourceID = request.GetResourceId()
		if request.From != nil {
			value := request.From.AsTime()
			query.From = &value
		}
		if request.Until != nil {
			value := request.Until.AsTime()
			query.Until = &value
		}
		if request.Page != nil {
			query.PageNumber, query.PageSize = int(request.Page.GetNumber()), int(request.Page.GetSize())
		}
	}
	page, err := handler.queries.List(ctx, audit.Principal{Subject: subject, Roles: roles}, query)
	if err != nil {
		switch {
		case errors.Is(err, audit.ErrInvalid):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, audit.ErrForbidden):
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Error(codes.Internal, "list audit records failed")
		}
	}
	records := make([]*controlv1.AuditRecord, len(page.Records))
	for index, record := range page.Records {
		records[index] = &controlv1.AuditRecord{
			Id: record.ID, OccurredAt: timestamppb.New(record.OccurredAt), PrincipalSubject: record.PrincipalSubject,
			Action: record.Action, ResourceType: record.ResourceType, ResourceId: record.ResourceID,
			Scope:  &commonv1.Scope{Region: record.Region, Environment: record.Environment, Stage: record.Stage},
			Result: record.Result, TraceId: record.TraceID,
		}
	}
	return &controlv1.ListAuditRecordsResponse{
		Records: records,
		Page:    &commonv1.PageResponse{Number: int32(page.PageNumber), Size: int32(page.PageSize), TotalNumber: page.TotalNumber, TotalPages: int64(page.TotalPages)},
	}, nil
}

var _ controlv1.AuditService = (*Handler)(nil)

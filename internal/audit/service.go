package audit

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	AuditorRole = "AUDITOR"
)

var (
	ErrInvalid   = errors.New("invalid audit query")
	ErrForbidden = errors.New("audit query is forbidden")
)

type Principal struct {
	Subject string
	Roles   []string
}

type Record struct {
	ID               int64
	OccurredAt       time.Time
	PrincipalSubject string
	Action           string
	ResourceType     string
	ResourceID       string
	Region           string
	Environment      string
	Stage            string
	Result           string
	TraceID          string
}

type Query struct {
	PrincipalSubject string
	ResourceType     string
	ResourceID       string
	From             *time.Time
	Until            *time.Time
	PageNumber       int
	PageSize         int
}

type Page struct {
	Records     []Record
	PageNumber  int
	PageSize    int
	TotalNumber int64
	TotalPages  int
}

type Repository interface {
	List(context.Context, Query) (Page, error)
}

type Service struct{ repository Repository }

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, errors.New("audit repository is required")
	}
	return &Service{repository: repository}, nil
}

func (service *Service) List(ctx context.Context, principal Principal, query Query) (Page, error) {
	if strings.TrimSpace(principal.Subject) == "" || !slices.Contains(principal.Roles, AuditorRole) {
		return Page{}, ErrForbidden
	}
	if query.PageNumber == 0 {
		query.PageNumber = 1
	}
	if query.PageSize == 0 {
		query.PageSize = 20
	}
	if query.PageNumber < 1 || query.PageSize < 1 || query.PageSize > 100 || query.From != nil && query.Until != nil && !query.From.Before(*query.Until) {
		return Page{}, fmt.Errorf("%w: page or time range is invalid", ErrInvalid)
	}
	query.PrincipalSubject = strings.TrimSpace(query.PrincipalSubject)
	query.ResourceType = strings.TrimSpace(query.ResourceType)
	query.ResourceID = strings.TrimSpace(query.ResourceID)
	return service.repository.List(ctx, query)
}

package application

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
)

const (
	ConfigViewerRole = "CONFIG_VIEWER"
	ConfigAdminRole  = "CONFIG_ADMIN"
)

var (
	ErrInvalid            = errors.New("invalid catalog request")
	ErrForbidden          = errors.New("catalog operation is forbidden")
	ErrNotFound           = errors.New("catalog resource was not found")
	ErrAlreadyExists      = errors.New("catalog resource already exists")
	ErrAborted            = errors.New("catalog revision conflict")
	ErrFailedPrecondition = errors.New("catalog precondition failed")
	identifierPattern     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,190}$`)
)

type Status string

const (
	StatusEnabled  Status = "ENABLED"
	StatusDisabled Status = "DISABLED"
)

type Cardinality string

const (
	CardinalityOneToOne  Cardinality = "ONE_TO_ONE"
	CardinalityOneToMany Cardinality = "ONE_TO_MANY"
)

type Principal struct {
	Subject     string
	DisplayName string
	Roles       []string
}

type AuditStamp struct {
	CreatedAt time.Time
	CreatedBy string
	UpdatedAt time.Time
	UpdatedBy string
}

type CollectionInput struct {
	Name               string
	Description        string
	Fields             []catalog.FieldDefinition
	KeyFields          []string
	SDKDeliveryEnabled bool
	SchemaVersion      int64
	Status             Status
}

type CollectionMutation struct {
	Definition       catalog.CollectionDefinition
	Status           Status
	ExpectedRevision catalog.ConfigRevision
	Actor            Principal
	At               time.Time
}

type CollectionView struct {
	Name               string
	Description        string
	Fields             []catalog.FieldDefinition
	KeyFields          []string
	SDKDeliveryEnabled bool
	SchemaVersion      int64
	Status             Status
	ConfigRevision     catalog.ConfigRevision
	Audit              AuditStamp
}

type PageQuery struct{ PageNumber, PageSize int }

type CollectionPage struct {
	Collections []CollectionView
	PageNumber  int
	PageSize    int
	TotalNumber int64
	TotalPages  int
}

type SubscriptionInput struct {
	ID          string
	ConsumerID  string
	Collection  string
	IndexName   string
	IndexFields []string
	Cardinality Cardinality
	Enabled     bool
}

type SubscriptionMutation struct {
	SubscriptionInput
	ExpectedRevision catalog.ConfigRevision
	Actor            Principal
	At               time.Time
}

type SubscriptionView struct {
	SubscriptionInput
	ConfigRevision catalog.ConfigRevision
	Audit          AuditStamp
}

type SubscriptionQuery struct {
	ConsumerID string
	Collection string
	PageNumber int
	PageSize   int
}

type SubscriptionPage struct {
	Subscriptions []SubscriptionView
	PageNumber    int
	PageSize      int
	TotalNumber   int64
	TotalPages    int
}

type Repository interface {
	CreateCollection(context.Context, CollectionMutation) (CollectionView, error)
	UpdateCollection(context.Context, CollectionMutation) (CollectionView, error)
	GetCollection(context.Context, string) (CollectionView, error)
	ListCollections(context.Context, PageQuery) (CollectionPage, error)
	CreateSubscription(context.Context, SubscriptionMutation) (SubscriptionView, error)
	UpdateSubscription(context.Context, SubscriptionMutation) (SubscriptionView, error)
	ListSubscriptions(context.Context, SubscriptionQuery) (SubscriptionPage, error)
}

type Clock interface{ Now() time.Time }

type Service struct {
	repository Repository
	clock      Clock
}

func NewService(repository Repository, clock Clock) (*Service, error) {
	if repository == nil || clock == nil {
		return nil, errors.New("new catalog service: repository and clock are required")
	}
	return &Service{repository: repository, clock: clock}, nil
}

func (service *Service) CreateCollection(ctx context.Context, principal Principal, input CollectionInput) (CollectionView, error) {
	if err := authorize(principal, true); err != nil {
		return CollectionView{}, err
	}
	definition, err := compileCollectionInput(input)
	if err != nil {
		return CollectionView{}, err
	}
	return service.repository.CreateCollection(ctx, CollectionMutation{Definition: definition, Status: input.Status, Actor: principal, At: service.clock.Now().UTC()})
}

func (service *Service) UpdateCollection(ctx context.Context, principal Principal, expected catalog.ConfigRevision, input CollectionInput) (CollectionView, error) {
	if err := authorize(principal, true); err != nil {
		return CollectionView{}, err
	}
	if expected == 0 {
		return CollectionView{}, fmt.Errorf("%w: expected collection revision is required", ErrInvalid)
	}
	definition, err := compileCollectionInput(input)
	if err != nil {
		return CollectionView{}, err
	}
	return service.repository.UpdateCollection(ctx, CollectionMutation{Definition: definition, Status: input.Status, ExpectedRevision: expected, Actor: principal, At: service.clock.Now().UTC()})
}

func (service *Service) GetCollection(ctx context.Context, principal Principal, name string) (CollectionView, error) {
	if err := authorize(principal, false); err != nil {
		return CollectionView{}, err
	}
	if !validIdentifier(name) {
		return CollectionView{}, fmt.Errorf("%w: collection name is invalid", ErrInvalid)
	}
	return service.repository.GetCollection(ctx, name)
}

func (service *Service) ListCollections(ctx context.Context, principal Principal, query PageQuery) (CollectionPage, error) {
	if err := authorize(principal, false); err != nil {
		return CollectionPage{}, err
	}
	bounded, err := boundPage(query.PageNumber, query.PageSize)
	if err != nil {
		return CollectionPage{}, err
	}
	return service.repository.ListCollections(ctx, bounded)
}

func (service *Service) CreateSubscription(ctx context.Context, principal Principal, input SubscriptionInput) (SubscriptionView, error) {
	if err := authorize(principal, true); err != nil {
		return SubscriptionView{}, err
	}
	input, err := validateSubscription(input, false)
	if err != nil {
		return SubscriptionView{}, err
	}
	return service.repository.CreateSubscription(ctx, SubscriptionMutation{SubscriptionInput: input, Actor: principal, At: service.clock.Now().UTC()})
}

func (service *Service) UpdateSubscription(ctx context.Context, principal Principal, expected catalog.ConfigRevision, input SubscriptionInput) (SubscriptionView, error) {
	if err := authorize(principal, true); err != nil {
		return SubscriptionView{}, err
	}
	if expected == 0 {
		return SubscriptionView{}, fmt.Errorf("%w: expected subscription revision is required", ErrInvalid)
	}
	input, err := validateSubscription(input, true)
	if err != nil {
		return SubscriptionView{}, err
	}
	return service.repository.UpdateSubscription(ctx, SubscriptionMutation{SubscriptionInput: input, ExpectedRevision: expected, Actor: principal, At: service.clock.Now().UTC()})
}

func (service *Service) ListSubscriptions(ctx context.Context, principal Principal, query SubscriptionQuery) (SubscriptionPage, error) {
	if err := authorize(principal, false); err != nil {
		return SubscriptionPage{}, err
	}
	if query.ConsumerID != "" && !validIdentifier(query.ConsumerID) || query.Collection != "" && !validIdentifier(query.Collection) {
		return SubscriptionPage{}, fmt.Errorf("%w: subscription filter is invalid", ErrInvalid)
	}
	page, err := boundPage(query.PageNumber, query.PageSize)
	if err != nil {
		return SubscriptionPage{}, err
	}
	query.PageNumber, query.PageSize = page.PageNumber, page.PageSize
	return service.repository.ListSubscriptions(ctx, query)
}

func compileCollectionInput(input CollectionInput) (catalog.CollectionDefinition, error) {
	if !validIdentifier(input.Name) || input.Status != StatusEnabled && input.Status != StatusDisabled {
		return catalog.CollectionDefinition{}, fmt.Errorf("%w: collection identity or status is invalid", ErrInvalid)
	}
	definition, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: input.Name, Description: input.Description, Fields: input.Fields, KeyFields: input.KeyFields,
		SDKDeliveryEnabled: input.SDKDeliveryEnabled, SchemaVersion: input.SchemaVersion,
	})
	if err != nil {
		return catalog.CollectionDefinition{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return definition, nil
}

func validateSubscription(input SubscriptionInput, requireID bool) (SubscriptionInput, error) {
	if requireID && strings.TrimSpace(input.ID) == "" || !validIdentifier(input.ConsumerID) || !validIdentifier(input.Collection) || !validIdentifier(input.IndexName) || len(input.IndexFields) == 0 || input.Cardinality != CardinalityOneToOne && input.Cardinality != CardinalityOneToMany {
		return SubscriptionInput{}, fmt.Errorf("%w: subscription identity, fields, or cardinality is invalid", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(input.IndexFields))
	for _, field := range input.IndexFields {
		if !validIdentifier(field) {
			return SubscriptionInput{}, fmt.Errorf("%w: subscription index field is invalid", ErrInvalid)
		}
		if _, duplicate := seen[field]; duplicate {
			return SubscriptionInput{}, fmt.Errorf("%w: subscription index field is duplicated", ErrInvalid)
		}
		seen[field] = struct{}{}
	}
	input.IndexFields = slices.Clone(input.IndexFields)
	return input, nil
}

func authorize(principal Principal, write bool) error {
	if strings.TrimSpace(principal.Subject) == "" {
		return ErrForbidden
	}
	if write && !slices.Contains(principal.Roles, ConfigAdminRole) || !write && !slices.Contains(principal.Roles, ConfigAdminRole) && !slices.Contains(principal.Roles, ConfigViewerRole) {
		return ErrForbidden
	}
	return nil
}

func boundPage(number, size int) (PageQuery, error) {
	if number == 0 {
		number = 1
	}
	if size == 0 {
		size = 20
	}
	if number < 1 || size < 1 || size > 100 {
		return PageQuery{}, fmt.Errorf("%w: page must be positive and size at most 100", ErrInvalid)
	}
	return PageQuery{PageNumber: number, PageSize: size}, nil
}

func validIdentifier(value string) bool {
	return value == strings.TrimSpace(value) && identifierPattern.MatchString(value)
}

package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
)

var (
	ErrInvalid = errors.New("invalid release")
	ErrAborted = errors.New("release authority is stale")
)

type EntityRevision uint64

type OrderStatus string

const (
	OrderInProgress OrderStatus = "IN_PROGRESS"
	OrderSucceeded  OrderStatus = "SUCCEEDED"
)

type ItemStatus string

const (
	ItemPending ItemStatus = "PENDING"
	ItemApplied ItemStatus = "APPLIED"
)

type StepType string

const (
	StepBaseApply StepType = "BASE_APPLY"
	StepComplete  StepType = "COMPLETE"
)

type StepStatus string

const (
	StepPending  StepStatus = "PENDING"
	StepExecuted StepStatus = "EXECUTED"
)

type ChangeAction string

const ChangeAdd ChangeAction = "ADD"

type Scope struct {
	Region      string
	Environment string
	Stage       string
}

type BaseFinalItemSpec struct {
	ID                         string
	After                      catalog.ConfigurationRecord
	ExpectedRecordRevision     catalog.ConfigRevision
	ExpectedCollectionRevision catalog.ConfigRevision
}

type BaseFinalOrderSpec struct {
	ID              string
	ReleaseNumber   string
	IdempotencyKey  string
	ModelCode       string
	TemplateCode    string
	TemplateVersion uint64
	ReleaseTypeCode string
	RequestDigest   string
	Scope           Scope
	CreatedBy       string
	CreatedAt       time.Time
	Items           []BaseFinalItemSpec
}

type Item struct {
	ID                         string
	Action                     ChangeAction
	Collection                 string
	RecordKey                  string
	BaseBefore                 *catalog.ConfigurationRecord
	EffectiveBefore            *catalog.ConfigurationRecord
	After                      *catalog.ConfigurationRecord
	ExpectedRecordRevision     catalog.ConfigRevision
	ExpectedCollectionRevision catalog.ConfigRevision
	Status                     ItemStatus
	ActiveConflictKey          string
}

type StepState struct {
	Type       StepType
	Status     StepStatus
	ExecutedAt *time.Time
	ExecutedBy string
}

type BaseAuthority struct {
	CollectionRevision catalog.ConfigRevision
	Records            map[string]*catalog.ConfigurationRecord
}

type BaseChange struct {
	Action ChangeAction
	Before *catalog.ConfigurationRecord
	After  catalog.ConfigurationRecord
}

type BaseEffect struct {
	Collection       string
	Environment      string
	PreviousRevision catalog.ConfigRevision
	Changes          []BaseChange
	ExecutedAt       time.Time
	ExecutedBy       string
}

// Order is the only aggregate root allowed to decide release state changes.
type Order struct {
	id              string
	releaseNumber   string
	idempotencyKey  string
	modelCode       string
	templateCode    string
	templateVersion uint64
	releaseTypeCode string
	requestDigest   string
	scope           Scope
	createdBy       string
	createdAt       time.Time
	updatedBy       string
	updatedAt       time.Time
	completedAt     *time.Time
	status          OrderStatus
	revision        EntityRevision
	currentStep     int
	steps           []StepState
	items           []Item
}

func NewBaseFinalOrder(spec BaseFinalOrderSpec) (*Order, error) {
	spec.Scope.Region = strings.TrimSpace(spec.Scope.Region)
	spec.Scope.Environment = strings.TrimSpace(spec.Scope.Environment)
	spec.Scope.Stage = strings.TrimSpace(spec.Scope.Stage)
	if strings.TrimSpace(spec.ID) == "" || strings.TrimSpace(spec.ReleaseNumber) == "" || strings.TrimSpace(spec.IdempotencyKey) == "" || strings.TrimSpace(spec.ModelCode) == "" || strings.TrimSpace(spec.TemplateCode) == "" || spec.TemplateVersion == 0 || strings.TrimSpace(spec.ReleaseTypeCode) == "" || len(spec.RequestDigest) != 64 {
		return nil, fmt.Errorf("%w: order identity is required", ErrInvalid)
	}
	if spec.Scope.Region == "" || spec.Scope.Environment == "" {
		return nil, fmt.Errorf("%w: region and environment are required", ErrInvalid)
	}
	if strings.TrimSpace(spec.CreatedBy) == "" || spec.CreatedAt.IsZero() {
		return nil, fmt.Errorf("%w: creator and creation time are required", ErrInvalid)
	}
	if len(spec.Items) == 0 || len(spec.Items) > 500 {
		return nil, fmt.Errorf("%w: item count must be 1..500", ErrInvalid)
	}

	items := make([]Item, len(spec.Items))
	seen := make(map[string]struct{}, len(spec.Items))
	collection := spec.Items[0].After.Collection
	for index, item := range spec.Items {
		if strings.TrimSpace(item.ID) == "" || item.After.Collection == "" || item.After.RecordKey == "" {
			return nil, fmt.Errorf("%w: item %d identity is required", ErrInvalid, index)
		}
		if item.After.Collection != collection {
			return nil, fmt.Errorf("%w: all items must target the model collection", ErrInvalid)
		}
		if item.After.Environment != spec.Scope.Environment {
			return nil, fmt.Errorf("%w: item %d environment differs from scope", ErrInvalid, index)
		}
		if item.ExpectedRecordRevision != 0 {
			return nil, fmt.Errorf("%w: ADD item expected record revision must be zero", ErrInvalid)
		}
		if item.ExpectedCollectionRevision == 0 {
			return nil, fmt.Errorf("%w: expected collection revision is required", ErrInvalid)
		}
		identity := item.After.Collection + "\x00" + item.After.RecordKey
		if _, duplicate := seen[identity]; duplicate {
			return nil, fmt.Errorf("%w: duplicate item record key", ErrInvalid)
		}
		seen[identity] = struct{}{}
		after := cloneRecord(item.After)
		items[index] = Item{
			ID:                         item.ID,
			Action:                     ChangeAdd,
			Collection:                 after.Collection,
			RecordKey:                  after.RecordKey,
			After:                      &after,
			ExpectedRecordRevision:     item.ExpectedRecordRevision,
			ExpectedCollectionRevision: item.ExpectedCollectionRevision,
			Status:                     ItemPending,
			ActiveConflictKey:          baseConflictKey(after.Collection, spec.Scope.Environment, after.RecordKey),
		}
	}
	createdAt := spec.CreatedAt.UTC()
	return &Order{
		id:              spec.ID,
		releaseNumber:   spec.ReleaseNumber,
		idempotencyKey:  spec.IdempotencyKey,
		modelCode:       spec.ModelCode,
		templateCode:    spec.TemplateCode,
		templateVersion: spec.TemplateVersion,
		releaseTypeCode: spec.ReleaseTypeCode,
		requestDigest:   spec.RequestDigest,
		scope:           spec.Scope,
		createdBy:       spec.CreatedBy,
		createdAt:       createdAt,
		updatedBy:       spec.CreatedBy,
		updatedAt:       createdAt,
		status:          OrderInProgress,
		revision:        1,
		steps: []StepState{
			{Type: StepBaseApply, Status: StepPending},
			{Type: StepComplete, Status: StepPending},
		},
		items: items,
	}, nil
}

func (order *Order) ExecuteBase(authority BaseAuthority, actor string, at time.Time) (BaseEffect, error) {
	if err := order.requireCurrent(StepBaseApply, StepPending); err != nil {
		return BaseEffect{}, err
	}
	if strings.TrimSpace(actor) == "" || at.IsZero() {
		return BaseEffect{}, fmt.Errorf("%w: actor and execution time are required", ErrInvalid)
	}
	changes := make([]BaseChange, len(order.items))
	for index, item := range order.items {
		if authority.CollectionRevision != item.ExpectedCollectionRevision {
			return BaseEffect{}, fmt.Errorf("%w: collection revision is %d, expected %d", ErrAborted, authority.CollectionRevision, item.ExpectedCollectionRevision)
		}
		if existing := authority.Records[item.RecordKey]; existing != nil {
			return BaseEffect{}, fmt.Errorf("%w: ADD target %q already exists at revision %d", ErrAborted, item.RecordKey, existing.ConfigRevision)
		}
		changes[index] = BaseChange{Action: ChangeAdd, After: cloneRecord(*item.After)}
	}

	at = at.UTC()
	order.steps[order.currentStep].Status = StepExecuted
	order.steps[order.currentStep].ExecutedAt = timePointer(at)
	order.steps[order.currentStep].ExecutedBy = actor
	order.bump(actor, at)
	return BaseEffect{
		Collection:       order.items[0].Collection,
		Environment:      order.scope.Environment,
		PreviousRevision: authority.CollectionRevision,
		Changes:          changes,
		ExecutedAt:       at,
		ExecutedBy:       actor,
	}, nil
}

func (order *Order) Advance(expected EntityRevision, actor string, at time.Time) error {
	if order.revision != expected {
		return fmt.Errorf("%w: order revision is %d, expected %d", ErrAborted, order.revision, expected)
	}
	if order.status != OrderInProgress || order.steps[order.currentStep].Status != StepExecuted || order.currentStep+1 >= len(order.steps) {
		return fmt.Errorf("%w: current step cannot advance", ErrInvalid)
	}
	order.currentStep++
	order.bump(actor, at.UTC())
	return nil
}

func (order *Order) Complete(expected EntityRevision, actor string, at time.Time) error {
	if order.revision != expected {
		return fmt.Errorf("%w: order revision is %d, expected %d", ErrAborted, order.revision, expected)
	}
	if err := order.requireCurrent(StepComplete, StepPending); err != nil {
		return err
	}
	if order.currentStep == 0 || order.steps[order.currentStep-1].Status != StepExecuted {
		return fmt.Errorf("%w: prior step is incomplete", ErrInvalid)
	}
	at = at.UTC()
	order.steps[order.currentStep].Status = StepExecuted
	order.steps[order.currentStep].ExecutedAt = timePointer(at)
	order.steps[order.currentStep].ExecutedBy = actor
	for index := range order.items {
		order.items[index].Status = ItemApplied
		order.items[index].ActiveConflictKey = ""
	}
	order.status = OrderSucceeded
	order.completedAt = timePointer(at)
	order.bump(actor, at)
	return nil
}

func (order *Order) ID() string { return order.id }

func (order *Order) ModelCode() string { return order.modelCode }

type OrderState struct {
	ID              string
	ReleaseNumber   string
	IdempotencyKey  string
	RequestDigest   string
	ModelCode       string
	TemplateCode    string
	TemplateVersion uint64
	ReleaseTypeCode string
	Scope           Scope
	CreatedBy       string
	CreatedAt       time.Time
	UpdatedBy       string
	UpdatedAt       time.Time
	CompletedAt     *time.Time
	Status          OrderStatus
	Revision        EntityRevision
	CurrentStep     int
	Steps           []StepState
	Items           []Item
}

func (order *Order) State() OrderState {
	state := OrderState{
		ID: order.id, ReleaseNumber: order.releaseNumber, IdempotencyKey: order.idempotencyKey,
		RequestDigest: order.requestDigest, ModelCode: order.modelCode, TemplateCode: order.templateCode,
		TemplateVersion: order.templateVersion, ReleaseTypeCode: order.releaseTypeCode, Scope: order.scope,
		CreatedBy: order.createdBy, CreatedAt: order.createdAt, UpdatedBy: order.updatedBy, UpdatedAt: order.updatedAt,
		Status: order.status, Revision: order.revision, CurrentStep: order.currentStep,
		Steps: make([]StepState, len(order.steps)), Items: order.Items(),
	}
	for index, step := range order.steps {
		state.Steps[index] = cloneStep(step)
	}
	if order.completedAt != nil {
		state.CompletedAt = timePointer(*order.completedAt)
	}
	return state
}

func RestoreOrder(state OrderState) (*Order, error) {
	if state.ID == "" || state.ModelCode == "" || state.TemplateCode == "" || state.TemplateVersion == 0 || state.ReleaseTypeCode == "" || len(state.RequestDigest) != 64 || state.Revision == 0 {
		return nil, fmt.Errorf("%w: persisted order identity is incomplete", ErrInvalid)
	}
	if len(state.Steps) == 0 || state.CurrentStep < 0 || state.CurrentStep >= len(state.Steps) || len(state.Items) == 0 {
		return nil, fmt.Errorf("%w: persisted order children are incomplete", ErrInvalid)
	}
	order := &Order{
		id: state.ID, releaseNumber: state.ReleaseNumber, idempotencyKey: state.IdempotencyKey,
		requestDigest: state.RequestDigest, modelCode: state.ModelCode, templateCode: state.TemplateCode,
		templateVersion: state.TemplateVersion, releaseTypeCode: state.ReleaseTypeCode, scope: state.Scope,
		createdBy: state.CreatedBy, createdAt: state.CreatedAt.UTC(), updatedBy: state.UpdatedBy, updatedAt: state.UpdatedAt.UTC(),
		status: state.Status, revision: state.Revision, currentStep: state.CurrentStep,
		steps: make([]StepState, len(state.Steps)), items: make([]Item, len(state.Items)),
	}
	for index, step := range state.Steps {
		order.steps[index] = cloneStep(step)
	}
	for index, item := range state.Items {
		order.items[index] = cloneItem(item)
	}
	if state.CompletedAt != nil {
		completed := state.CompletedAt.UTC()
		order.completedAt = &completed
	}
	return order, nil
}

func (order *Order) Status() OrderStatus { return order.status }

func (order *Order) Revision() EntityRevision { return order.revision }

func (order *Order) Scope() Scope { return order.scope }

func (order *Order) CurrentStep() StepState { return cloneStep(order.steps[order.currentStep]) }

func (order *Order) Items() []Item {
	items := make([]Item, len(order.items))
	for index, item := range order.items {
		items[index] = cloneItem(item)
	}
	return items
}

// Clone returns an independent aggregate copy for transaction adapters and
// tests. Mutating the clone cannot alter the source aggregate.
func (order *Order) Clone() *Order {
	cloned := *order
	cloned.steps = make([]StepState, len(order.steps))
	for index, step := range order.steps {
		cloned.steps[index] = cloneStep(step)
	}
	cloned.items = make([]Item, len(order.items))
	for index, item := range order.items {
		cloned.items[index] = cloneItem(item)
	}
	if order.completedAt != nil {
		cloned.completedAt = timePointer(*order.completedAt)
	}
	return &cloned
}

func (order *Order) requireCurrent(stepType StepType, status StepStatus) error {
	if order.status != OrderInProgress || order.steps[order.currentStep].Type != stepType || order.steps[order.currentStep].Status != status {
		return fmt.Errorf("%w: order is not at %s/%s", ErrInvalid, stepType, status)
	}
	return nil
}

func (order *Order) bump(actor string, at time.Time) {
	order.revision++
	order.updatedBy = actor
	order.updatedAt = at
}

func baseConflictKey(collection, environment, recordKey string) string {
	encoded, _ := json.Marshal([]string{"BASE", collection, environment, recordKey})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func cloneItem(item Item) Item {
	if item.BaseBefore != nil {
		cloned := cloneRecord(*item.BaseBefore)
		item.BaseBefore = &cloned
	}
	if item.EffectiveBefore != nil {
		cloned := cloneRecord(*item.EffectiveBefore)
		item.EffectiveBefore = &cloned
	}
	if item.After != nil {
		cloned := cloneRecord(*item.After)
		item.After = &cloned
	}
	return item
}

func cloneRecord(record catalog.ConfigurationRecord) catalog.ConfigurationRecord {
	source := record.Data
	record.Data = make(map[string]string, len(source))
	for key, value := range source {
		record.Data[key] = value
	}
	return record
}

func cloneStep(step StepState) StepState {
	if step.ExecutedAt != nil {
		step.ExecutedAt = timePointer(*step.ExecutedAt)
	}
	return step
}

func timePointer(value time.Time) *time.Time { return &value }

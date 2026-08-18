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
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
)

var (
	ErrInvalid              = errors.New("invalid release")
	ErrAborted              = errors.New("release authority is stale")
	ErrIdempotencyKeyReused = errors.New("idempotency key was reused for a different request")
	ErrActiveConflict       = errors.New("another release is active for the target")
	ErrForbidden            = errors.New("release action is forbidden")
)

type EntityRevision uint64

type OrderStatus string

const (
	OrderInProgress OrderStatus = "IN_PROGRESS"
	OrderSucceeded  OrderStatus = "SUCCEEDED"
	OrderRolledBack OrderStatus = "ROLLED_BACK"
	OrderRejected   OrderStatus = "REJECTED"
)

type ItemStatus string

const (
	ItemPending    ItemStatus = "PENDING"
	ItemApplied    ItemStatus = "APPLIED"
	ItemRolledBack ItemStatus = "ROLLED_BACK"
)

type StepType string

const (
	StepManualReview   StepType = "MANUAL_REVIEW"
	StepOverlayApply   StepType = "OVERLAY_APPLY"
	StepPercentRollout StepType = "PERCENT_ROLLOUT"
	StepBaseApply      StepType = "BASE_APPLY"
	StepCompare        StepType = "COMPARE"
	StepComplete       StepType = "COMPLETE"
)

type StepStatus string

const (
	StepPending    StepStatus = "PENDING"
	StepExecuting  StepStatus = "EXECUTING"
	StepExecuted   StepStatus = "EXECUTED"
	StepApproved   StepStatus = "APPROVED"
	StepRejected   StepStatus = "REJECTED"
	StepRolledBack StepStatus = "ROLLED_BACK"
)

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "PENDING"
	ApprovalApproved ApprovalStatus = "APPROVED"
	ApprovalRejected ApprovalStatus = "REJECTED"
)

type Principal struct {
	Subject string
	Roles   []string
}

type ApprovalState struct {
	Status      ApprovalStatus `json:"status"`
	RequestedAt time.Time      `json:"requestedAt"`
	RequestedBy string         `json:"requestedBy"`
	DecidedAt   *time.Time     `json:"decidedAt,omitempty"`
	DecidedBy   string         `json:"decidedBy,omitempty"`
	Comment     string         `json:"comment,omitempty"`
}

type ChangeAction string

const (
	ChangeAdd    ChangeAction = "ADD"
	ChangeModify ChangeAction = "MODIFY"
	ChangeDelete ChangeAction = "DELETE"
)

type Scope struct {
	Region      string `json:"region"`
	Environment string `json:"environment"`
	Stage       string `json:"stage"`
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
	Template        CompiledTemplate
}

type OverlayFinalItemSpec struct {
	ID                         string
	Action                     ChangeAction
	BaseBefore                 *catalog.ConfigurationRecord
	EffectiveBefore            *catalog.ConfigurationRecord
	After                      *catalog.ConfigurationRecord
	ExpectedRecordRevision     catalog.ConfigRevision
	ExpectedCollectionRevision catalog.ConfigRevision
}

type OverlayFinalOrderSpec struct {
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
	Items           []OverlayFinalItemSpec
	Template        CompiledTemplate
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
	Code               string
	Type               StepType
	Status             StepStatus
	RequiredRoles      []string
	SelfApprovalPolicy SelfApprovalPolicy
	RolloutRanges      []overlay.BucketRange
	Approval           *ApprovalState
	Effect             *StepEffectEnvelope
	ExecutedAt         *time.Time
	ExecutedBy         string
	RolledBackAt       *time.Time
	RolledBackBy       string
}

type OverlayAuthority struct {
	CollectionRevision catalog.ConfigRevision
	BaseRecords        map[string]*catalog.ConfigurationRecord
	Rules              map[string]*overlay.Rule
}

type OverlayRuleChange struct {
	RecordKey    string        `json:"recordKey"`
	PreviousRule *overlay.Rule `json:"previousRule,omitempty"`
	NewRule      *overlay.Rule `json:"newRule,omitempty"`
}

type OverlayEffect struct {
	EffectVersion    int32                  `json:"effectVersion"`
	Collection       string                 `json:"collection"`
	Scope            Scope                  `json:"scope"`
	PreviousRevision catalog.ConfigRevision `json:"previousRevision"`
	AppliedRevision  catalog.ConfigRevision `json:"appliedRevision"`
	Changes          []OverlayRuleChange    `json:"changes"`
	ExecutedAt       time.Time              `json:"executedAt"`
	ExecutedBy       string                 `json:"executedBy"`
}

type StepEffectEnvelope struct {
	EffectVersion int32          `json:"effectVersion"`
	Overlay       *OverlayEffect `json:"overlay,omitempty"`
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
	definitions := spec.Template.Steps()
	if len(definitions) == 0 {
		definitions = []StepDefinition{{Code: "base-apply", Type: StepBaseApply}, {Code: "complete", Type: StepComplete}}
	}
	steps := make([]StepState, len(definitions))
	for index, definition := range definitions {
		steps[index] = StepState{
			Code: definition.Code, Type: definition.Type, Status: StepPending,
			RequiredRoles: append([]string(nil), definition.RequiredRoles...),
		}
		if definition.ManualReview != nil {
			steps[index].SelfApprovalPolicy = definition.ManualReview.SelfApprovalPolicy
		}
		if definition.PercentRollout != nil {
			steps[index].RolloutRanges = append([]overlay.BucketRange(nil), definition.PercentRollout.Ranges...)
		}
	}
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
		steps:           steps,
		items:           items,
	}, nil
}

func NewOverlayFinalOrder(spec OverlayFinalOrderSpec) (*Order, error) {
	spec.Scope.Region = strings.TrimSpace(spec.Scope.Region)
	spec.Scope.Environment = strings.TrimSpace(spec.Scope.Environment)
	spec.Scope.Stage = strings.TrimSpace(spec.Scope.Stage)
	if strings.TrimSpace(spec.ID) == "" || strings.TrimSpace(spec.ReleaseNumber) == "" || strings.TrimSpace(spec.IdempotencyKey) == "" || strings.TrimSpace(spec.ModelCode) == "" || strings.TrimSpace(spec.TemplateCode) == "" || spec.TemplateVersion == 0 || strings.TrimSpace(spec.ReleaseTypeCode) == "" || len(spec.RequestDigest) != 64 {
		return nil, fmt.Errorf("%w: order identity is required", ErrInvalid)
	}
	if spec.Scope.Region == "" || spec.Scope.Environment == "" || spec.Scope.Stage == "" {
		return nil, fmt.Errorf("%w: full scope is required for OVERLAY_FINAL", ErrInvalid)
	}
	if strings.TrimSpace(spec.CreatedBy) == "" || spec.CreatedAt.IsZero() || len(spec.Items) == 0 || len(spec.Items) > 500 {
		return nil, fmt.Errorf("%w: creator, creation time, and 1..500 items are required", ErrInvalid)
	}
	if spec.Template.FinalEffect() != FinalEffectOverlay {
		return nil, fmt.Errorf("%w: OVERLAY_FINAL template is required", ErrInvalid)
	}

	items := make([]Item, len(spec.Items))
	seen := make(map[string]struct{}, len(spec.Items))
	collection := ""
	for index, item := range spec.Items {
		target := item.After
		if target == nil {
			target = item.EffectiveBefore
		}
		if target == nil || strings.TrimSpace(item.ID) == "" || target.Collection == "" || target.RecordKey == "" {
			return nil, fmt.Errorf("%w: item %d identity is required", ErrInvalid, index)
		}
		if collection == "" {
			collection = target.Collection
		}
		if target.Collection != collection || target.Environment != spec.Scope.Environment {
			return nil, fmt.Errorf("%w: item %d target differs from collection or scope", ErrInvalid, index)
		}
		if !validOverlayItemShape(item) {
			return nil, fmt.Errorf("%w: item %d before/after shape is invalid for %s", ErrInvalid, index, item.Action)
		}
		for _, record := range []*catalog.ConfigurationRecord{item.BaseBefore, item.EffectiveBefore, item.After} {
			if record != nil && (record.Collection != collection || record.Environment != spec.Scope.Environment || record.RecordKey != target.RecordKey || record.Data == nil) {
				return nil, fmt.Errorf("%w: item %d record identity differs from target", ErrInvalid, index)
			}
		}
		identity := collection + "\x00" + target.RecordKey
		if _, duplicate := seen[identity]; duplicate {
			return nil, fmt.Errorf("%w: duplicate item record key", ErrInvalid)
		}
		seen[identity] = struct{}{}
		items[index] = Item{
			ID:                         item.ID,
			Action:                     item.Action,
			Collection:                 collection,
			RecordKey:                  target.RecordKey,
			BaseBefore:                 cloneRecordPointer(item.BaseBefore),
			EffectiveBefore:            cloneRecordPointer(item.EffectiveBefore),
			After:                      cloneRecordPointer(item.After),
			ExpectedRecordRevision:     item.ExpectedRecordRevision,
			ExpectedCollectionRevision: item.ExpectedCollectionRevision,
			Status:                     ItemPending,
			ActiveConflictKey:          overlayConflictKey(collection, spec.Scope, target.RecordKey),
		}
	}

	definitions := spec.Template.Steps()
	steps := make([]StepState, len(definitions))
	for index, definition := range definitions {
		steps[index] = StepState{Code: definition.Code, Type: definition.Type, Status: StepPending, RequiredRoles: append([]string(nil), definition.RequiredRoles...)}
		if definition.ManualReview != nil {
			steps[index].SelfApprovalPolicy = definition.ManualReview.SelfApprovalPolicy
		}
	}
	createdAt := spec.CreatedAt.UTC()
	return &Order{
		id: spec.ID, releaseNumber: spec.ReleaseNumber, idempotencyKey: spec.IdempotencyKey,
		modelCode: spec.ModelCode, templateCode: spec.TemplateCode, templateVersion: spec.TemplateVersion,
		releaseTypeCode: spec.ReleaseTypeCode, requestDigest: spec.RequestDigest, scope: spec.Scope,
		createdBy: spec.CreatedBy, createdAt: createdAt, updatedBy: spec.CreatedBy, updatedAt: createdAt,
		status: OrderInProgress, revision: 1, steps: steps, items: items,
	}, nil
}

func validOverlayItemShape(item OverlayFinalItemSpec) bool {
	if item.ExpectedCollectionRevision == 0 {
		return false
	}
	switch item.Action {
	case ChangeAdd:
		return item.BaseBefore == nil && item.EffectiveBefore == nil && item.After != nil && item.ExpectedRecordRevision == 0
	case ChangeModify:
		return item.BaseBefore != nil && item.EffectiveBefore != nil && item.After != nil && item.ExpectedRecordRevision > 0
	case ChangeDelete:
		return item.BaseBefore != nil && item.EffectiveBefore != nil && item.After == nil && item.ExpectedRecordRevision > 0
	default:
		return false
	}
}

func (order *Order) ExecuteManualReview(expected EntityRevision, actor string, at time.Time) error {
	if order.revision != expected {
		return fmt.Errorf("%w: order revision is %d, expected %d", ErrAborted, order.revision, expected)
	}
	if err := order.requireCurrent(StepManualReview, StepPending); err != nil {
		return err
	}
	if strings.TrimSpace(actor) == "" || at.IsZero() {
		return fmt.Errorf("%w: actor and execution time are required", ErrInvalid)
	}
	at = at.UTC()
	step := &order.steps[order.currentStep]
	step.Status = StepExecuting
	step.Approval = &ApprovalState{Status: ApprovalPending, RequestedAt: at, RequestedBy: actor}
	order.bump(actor, at)
	return nil
}

func (order *Order) ApproveManualReview(expected EntityRevision, principal Principal, comment string, at time.Time) error {
	if err := order.authorizeManualDecision(expected, principal, at); err != nil {
		return err
	}
	at = at.UTC()
	step := &order.steps[order.currentStep]
	step.Status = StepApproved
	step.Approval.Status = ApprovalApproved
	step.Approval.DecidedAt = timePointer(at)
	step.Approval.DecidedBy = principal.Subject
	step.Approval.Comment = comment
	order.bump(principal.Subject, at)
	return nil
}

func (order *Order) RejectManualReview(expected EntityRevision, principal Principal, comment string, at time.Time) error {
	if strings.TrimSpace(comment) == "" {
		return fmt.Errorf("%w: rejection comment is required", ErrInvalid)
	}
	if err := order.authorizeManualDecision(expected, principal, at); err != nil {
		return err
	}
	at = at.UTC()
	step := &order.steps[order.currentStep]
	step.Status = StepRejected
	step.Approval.Status = ApprovalRejected
	step.Approval.DecidedAt = timePointer(at)
	step.Approval.DecidedBy = principal.Subject
	step.Approval.Comment = comment
	for index := range order.items {
		order.items[index].ActiveConflictKey = ""
	}
	order.status = OrderRejected
	order.completedAt = timePointer(at)
	order.bump(principal.Subject, at)
	return nil
}

func (order *Order) authorizeManualDecision(expected EntityRevision, principal Principal, at time.Time) error {
	if order.revision != expected {
		return fmt.Errorf("%w: order revision is %d, expected %d", ErrAborted, order.revision, expected)
	}
	if err := order.requireCurrent(StepManualReview, StepExecuting); err != nil {
		return err
	}
	principal.Subject = strings.TrimSpace(principal.Subject)
	if principal.Subject == "" || at.IsZero() {
		return fmt.Errorf("%w: principal and decision time are required", ErrInvalid)
	}
	step := order.steps[order.currentStep]
	if !hasRequiredRole(principal.Roles, step.RequiredRoles) {
		return fmt.Errorf("%w: principal lacks a required approval role", ErrForbidden)
	}
	if step.SelfApprovalPolicy == SelfApprovalDenyProduction && order.scope.Environment == "production" && principal.Subject == order.createdBy {
		return fmt.Errorf("%w: production release creator cannot self-approve", ErrForbidden)
	}
	return nil
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

func (order *Order) ExecuteOverlay(authority OverlayAuthority, newRevision catalog.ConfigRevision, actor string, at time.Time) (OverlayEffect, error) {
	if err := order.requireCurrent(StepOverlayApply, StepPending); err != nil {
		return OverlayEffect{}, err
	}
	if strings.TrimSpace(actor) == "" || at.IsZero() || newRevision <= authority.CollectionRevision {
		return OverlayEffect{}, fmt.Errorf("%w: actor, execution time, and config revision are required", ErrInvalid)
	}
	executionAt := at.UTC()
	changes := make([]OverlayRuleChange, len(order.items))
	for index, item := range order.items {
		if authority.CollectionRevision != item.ExpectedCollectionRevision {
			return OverlayEffect{}, fmt.Errorf("%w: collection revision is %d, expected %d", ErrAborted, authority.CollectionRevision, item.ExpectedCollectionRevision)
		}
		base := authority.BaseRecords[item.RecordKey]
		if item.ExpectedRecordRevision == 0 {
			if base != nil {
				return OverlayEffect{}, fmt.Errorf("%w: base record %q now exists", ErrAborted, item.RecordKey)
			}
		} else if base == nil || base.ConfigRevision != item.ExpectedRecordRevision {
			return OverlayEffect{}, fmt.Errorf("%w: base record %q revision changed", ErrAborted, item.RecordKey)
		}
		previous := cloneOverlayRulePointer(authority.Rules[item.RecordKey])
		if previous != nil && (previous.Collection != item.Collection || previous.RecordKey != item.RecordKey || previous.Scope != overlayScope(order.scope)) {
			return OverlayEffect{}, fmt.Errorf("%w: current overlay rule identity is inconsistent", ErrInvalid)
		}

		var next *overlay.Rule
		if base != nil || item.After != nil {
			ruleID := item.ID
			createdAt := executionAt
			createdBy := actor
			if previous != nil {
				ruleID = previous.ID
				createdAt = previous.CreatedAt
				createdBy = previous.CreatedBy
			}
			activation := newRevision
			compiled, err := overlay.CompileRule(overlay.RuleSpec{
				ID: ruleID, Collection: item.Collection, Scope: overlayScope(order.scope), RecordKey: item.RecordKey,
				Base: base, Desired: item.After, ConfigRevision: newRevision, ReleaseOrderID: order.id,
				ActivatedRevision: &activation, ActivatedAt: &executionAt,
				CreatedAt: createdAt, CreatedBy: createdBy, UpdatedAt: executionAt, UpdatedBy: actor,
			})
			if err != nil {
				return OverlayEffect{}, fmt.Errorf("%w: compile item %q: %v", ErrInvalid, item.RecordKey, err)
			}
			next = &compiled
		}
		changes[index] = OverlayRuleChange{RecordKey: item.RecordKey, PreviousRule: previous, NewRule: cloneOverlayRulePointer(next)}
	}

	at = executionAt
	effect := OverlayEffect{
		EffectVersion: 1, Collection: order.items[0].Collection, Scope: order.scope,
		PreviousRevision: authority.CollectionRevision, AppliedRevision: newRevision, Changes: changes, ExecutedAt: at, ExecutedBy: actor,
	}
	step := &order.steps[order.currentStep]
	step.Status = StepExecuted
	step.ExecutedAt = timePointer(at)
	step.ExecutedBy = actor
	step.Effect = &StepEffectEnvelope{EffectVersion: 1, Overlay: cloneOverlayEffectPointer(&effect)}
	order.bump(actor, at)
	return *cloneOverlayEffectPointer(&effect), nil
}

func (order *Order) RollbackOverlay(expected EntityRevision, currentCollectionRevision, newRevision catalog.ConfigRevision, actor string, at time.Time) (OverlayEffect, error) {
	if order.revision != expected {
		return OverlayEffect{}, fmt.Errorf("%w: order revision is %d, expected %d", ErrAborted, order.revision, expected)
	}
	if order.status != OrderInProgress || strings.TrimSpace(actor) == "" || at.IsZero() || currentCollectionRevision == 0 || newRevision <= currentCollectionRevision {
		return OverlayEffect{}, fmt.Errorf("%w: order cannot be rolled back", ErrInvalid)
	}
	effectIndex := -1
	for index := len(order.steps) - 1; index >= 0; index-- {
		if order.steps[index].Effect != nil && order.steps[index].Effect.Overlay != nil {
			effectIndex = index
			break
		}
	}
	if effectIndex < 0 {
		return OverlayEffect{}, fmt.Errorf("%w: order has no applied overlay effect", ErrInvalid)
	}
	original := order.steps[effectIndex].Effect.Overlay
	if currentCollectionRevision != original.AppliedRevision {
		return OverlayEffect{}, fmt.Errorf("%w: collection revision is %d, expected applied revision %d", ErrAborted, currentCollectionRevision, original.AppliedRevision)
	}
	changes := make([]OverlayRuleChange, len(original.Changes))
	for index := range original.Changes {
		source := original.Changes[len(original.Changes)-1-index]
		changes[index] = OverlayRuleChange{
			RecordKey: source.RecordKey, PreviousRule: cloneOverlayRulePointer(source.NewRule), NewRule: cloneOverlayRulePointer(source.PreviousRule),
		}
	}
	at = at.UTC()
	compensation := OverlayEffect{
		EffectVersion: 1, Collection: original.Collection, Scope: original.Scope,
		PreviousRevision: currentCollectionRevision, AppliedRevision: newRevision, Changes: changes, ExecutedAt: at, ExecutedBy: actor,
	}
	step := &order.steps[effectIndex]
	step.Status = StepRolledBack
	step.RolledBackAt = timePointer(at)
	step.RolledBackBy = actor
	for index := range order.items {
		order.items[index].Status = ItemRolledBack
		order.items[index].ActiveConflictKey = ""
	}
	order.currentStep = effectIndex
	order.status = OrderRolledBack
	order.completedAt = timePointer(at)
	order.bump(actor, at)
	return *cloneOverlayEffectPointer(&compensation), nil
}

func (order *Order) Advance(expected EntityRevision, actor string, at time.Time) error {
	if order.revision != expected {
		return fmt.Errorf("%w: order revision is %d, expected %d", ErrAborted, order.revision, expected)
	}
	stepStatus := order.steps[order.currentStep].Status
	if order.status != OrderInProgress || (stepStatus != StepExecuted && stepStatus != StepApproved) || order.currentStep+1 >= len(order.steps) {
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

func overlayConflictKey(collection string, scope Scope, recordKey string) string {
	encoded, _ := json.Marshal([]string{"OVERLAY", collection, scope.Region, scope.Environment, scope.Stage, recordKey})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func overlayScope(scope Scope) overlay.Scope {
	return overlay.Scope{Region: scope.Region, Environment: scope.Environment, Stage: scope.Stage}
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

func cloneRecordPointer(record *catalog.ConfigurationRecord) *catalog.ConfigurationRecord {
	if record == nil {
		return nil
	}
	cloned := cloneRecord(*record)
	return &cloned
}

func cloneOverlayRulePointer(rule *overlay.Rule) *overlay.Rule {
	if rule == nil {
		return nil
	}
	cloned := *rule
	cloned.Content = make(map[string]string, len(rule.Content))
	for key, value := range rule.Content {
		cloned.Content[key] = value
	}
	cloned.RolloutRanges = append([]overlay.BucketRange(nil), rule.RolloutRanges...)
	if rule.ActivatedRevision != nil {
		value := *rule.ActivatedRevision
		cloned.ActivatedRevision = &value
	}
	if rule.EffectiveFrom != nil {
		value := *rule.EffectiveFrom
		cloned.EffectiveFrom = &value
	}
	if rule.EffectiveUntil != nil {
		value := *rule.EffectiveUntil
		cloned.EffectiveUntil = &value
	}
	if rule.ActivatedAt != nil {
		value := *rule.ActivatedAt
		cloned.ActivatedAt = &value
	}
	if rule.ExpiredRevision != nil {
		value := *rule.ExpiredRevision
		cloned.ExpiredRevision = &value
	}
	if rule.ExpiredAt != nil {
		value := *rule.ExpiredAt
		cloned.ExpiredAt = &value
	}
	return &cloned
}

func cloneOverlayEffectPointer(effect *OverlayEffect) *OverlayEffect {
	if effect == nil {
		return nil
	}
	cloned := *effect
	cloned.Changes = make([]OverlayRuleChange, len(effect.Changes))
	for index, change := range effect.Changes {
		cloned.Changes[index] = OverlayRuleChange{
			RecordKey: change.RecordKey, PreviousRule: cloneOverlayRulePointer(change.PreviousRule), NewRule: cloneOverlayRulePointer(change.NewRule),
		}
	}
	return &cloned
}

func cloneStep(step StepState) StepState {
	step.RequiredRoles = append([]string(nil), step.RequiredRoles...)
	step.RolloutRanges = append([]overlay.BucketRange(nil), step.RolloutRanges...)
	if step.Approval != nil {
		approval := *step.Approval
		if approval.DecidedAt != nil {
			approval.DecidedAt = timePointer(*approval.DecidedAt)
		}
		step.Approval = &approval
	}
	if step.ExecutedAt != nil {
		step.ExecutedAt = timePointer(*step.ExecutedAt)
	}
	if step.RolledBackAt != nil {
		step.RolledBackAt = timePointer(*step.RolledBackAt)
	}
	if step.Effect != nil {
		effect := *step.Effect
		effect.Overlay = cloneOverlayEffectPointer(step.Effect.Overlay)
		step.Effect = &effect
	}
	return step
}

func hasRequiredRole(actual, required []string) bool {
	roles := make(map[string]struct{}, len(actual))
	for _, role := range actual {
		roles[role] = struct{}{}
	}
	for _, role := range required {
		if _, exists := roles[role]; exists {
			return true
		}
	}
	return false
}

func timePointer(value time.Time) *time.Time { return &value }

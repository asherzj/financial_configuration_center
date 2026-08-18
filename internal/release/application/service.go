package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
)

type Clock interface {
	Now() time.Time
}

type IDs interface {
	NewID() string
	NewReleaseNumber(time.Time) string
}

// UnitOfWork owns the transaction boundary for Release. Its transaction port
// exposes release effects, never generic record CRUD.
type UnitOfWork interface {
	WithinTransaction(context.Context, func(Transaction) error) error
}

var (
	// ErrRetryableTransaction is returned by persistence adapters for a whole-
	// transaction deadlock or lock timeout. Release commands are safe to retry
	// because their create/action request identities are persisted atomically.
	ErrRetryableTransaction = errors.New("retryable release transaction")
	// ErrCreateRequestRace asks CreateBaseFinal to start a fresh transaction and
	// read the winner of a concurrent create-idempotency insert.
	ErrCreateRequestRace = errors.New("concurrent create request won")
)

type Transaction interface {
	LoadCatalog(context.Context, string, string) (CatalogBundle, error)
	LoadBaseAuthority(context.Context, string, string, []string) (release.BaseAuthority, error)
	LoadOverlayRules(context.Context, string, release.Scope, []string) ([]overlay.Rule, error)
	FindCreateResult(context.Context, string, string) (StoredRequestResult, bool, error)
	InsertOrder(context.Context, *release.Order) error
	LoadOrderForUpdate(context.Context, string) (*release.Order, error)
	FindActionResult(context.Context, string, string) (StoredRequestResult, bool, error)
	AllocateConfigRevision(context.Context) (catalog.ConfigRevision, error)
	ApplyBaseEffect(context.Context, string, release.BaseEffect) error
	ApplyOverlayEffect(context.Context, string, release.OverlayEffect) error
	ApplyPercentEffect(context.Context, string, release.PercentEffect) error
	SaveOrder(context.Context, *release.Order) error
	RecordAction(context.Context, ActionRecord) error
	InsertActionResult(context.Context, string, string, string, OrderView, time.Time) error
}

type CatalogBundle struct {
	Definition catalog.CollectionDefinition
	Model      catalog.CompiledModel
	Template   TemplateRef
}

type TemplateRef struct {
	Code            string
	Version         uint64
	ReleaseTypeCode string
	Definition      release.CompiledTemplate
}

type AddDraft struct {
	Data                       map[string]string
	ExpectedRecordRevision     catalog.ConfigRevision
	ExpectedCollectionRevision catalog.ConfigRevision
}

type ReleaseDraft struct {
	Action                     release.ChangeAction
	BaseBefore                 map[string]string
	EffectiveBefore            map[string]string
	After                      map[string]string
	ExpectedRecordRevision     catalog.ConfigRevision
	ExpectedCollectionRevision catalog.ConfigRevision
}

type CreateReleaseCommand struct {
	IdempotencyKey  string
	ModelCode       string
	ReleaseTypeCode string
	Scope           release.Scope
	Actor           string
	Items           []ReleaseDraft
}

type canonicalReleaseDraft struct {
	action                     release.ChangeAction
	baseBefore                 *catalog.ConfigurationRecord
	effectiveBefore            *catalog.ConfigurationRecord
	after                      *catalog.ConfigurationRecord
	expectedRecordRevision     catalog.ConfigRevision
	expectedCollectionRevision catalog.ConfigRevision
}

type CreateBaseFinalCommand struct {
	IdempotencyKey  string
	ModelCode       string
	ReleaseTypeCode string
	Scope           release.Scope
	Actor           string
	Items           []AddDraft
}

type Action string

const (
	ActionExecute  Action = "EXECUTE"
	ActionAdvance  Action = "ADVANCE"
	ActionApprove  Action = "APPROVE"
	ActionReject   Action = "REJECT"
	ActionRollback Action = "ROLLBACK"
)

type ActCommand struct {
	OrderID             string
	ActionRequestID     string
	ExpectedRevision    release.EntityRevision
	ExpectedCurrentStep string
	Action              Action
	Actor               string
	Roles               []string
	Comment             string
}

type OrderView struct {
	ID                string                 `json:"id"`
	Status            release.OrderStatus    `json:"status"`
	CurrentStepCode   string                 `json:"currentStepCode"`
	CurrentStep       release.StepType       `json:"currentStep"`
	CurrentStepStatus release.StepStatus     `json:"currentStepStatus"`
	Revision          release.EntityRevision `json:"revision"`
	CanExecute        bool                   `json:"canExecute"`
	CanAdvance        bool                   `json:"canAdvance"`
	CanApprove        bool                   `json:"canApprove"`
	CanReject         bool                   `json:"canReject"`
	CanRollback       bool                   `json:"canRollback"`
	Steps             []StepView             `json:"steps"`
}

type StepView struct {
	Code   string             `json:"code"`
	Type   release.StepType   `json:"type"`
	Status release.StepStatus `json:"status"`
}

type StoredRequestResult struct {
	RequestDigest string
	Result        OrderView
}

type ActionRecord struct {
	OrderID  string
	StepCode string
	Action   Action
	Actor    string
	Comment  string
	Scope    release.Scope
	At       time.Time
}

// Service is the only application entry point that can cause base
// ConfigurationRecord writes.
type Service struct {
	unitOfWork UnitOfWork
	ids        IDs
	clock      Clock
}

func NewService(unitOfWork UnitOfWork, ids IDs, clock Clock) *Service {
	return &Service{unitOfWork: unitOfWork, ids: ids, clock: clock}
}

func (service *Service) CreateBaseFinal(ctx context.Context, command CreateBaseFinalCommand) (OrderView, error) {
	if err := service.ready(); err != nil {
		return OrderView{}, err
	}
	if strings.TrimSpace(command.IdempotencyKey) == "" || strings.TrimSpace(command.ModelCode) == "" || strings.TrimSpace(command.Actor) == "" {
		return OrderView{}, fmt.Errorf("%w: idempotency key, model, and actor are required", release.ErrInvalid)
	}
	if len(command.Items) == 0 || len(command.Items) > 500 {
		return OrderView{}, fmt.Errorf("%w: item count must be 1..500", release.ErrInvalid)
	}
	if strings.TrimSpace(command.ReleaseTypeCode) == "" {
		command.ReleaseTypeCode = "direct"
	}

	var created *release.Order
	var replayed *OrderView
	err := service.withinIdempotentTransaction(ctx, func(transaction Transaction) error {
		bundle, err := transaction.LoadCatalog(ctx, command.ModelCode, command.ReleaseTypeCode)
		if err != nil {
			return fmt.Errorf("load release catalog: %w", err)
		}
		if bundle.Model.Collection() != bundle.Definition.Name() {
			return errors.New("release catalog is internally inconsistent")
		}

		records := make([]catalog.ConfigurationRecord, len(command.Items))
		recordKeys := make([]string, len(command.Items))
		for index, draft := range command.Items {
			record, err := bundle.Definition.NewRecord(command.Scope.Environment, draft.Data)
			if err != nil {
				return fmt.Errorf("canonicalize release item %d: %w", index, err)
			}
			records[index] = record
			recordKeys[index] = record.RecordKey
		}
		requestDigest, err := normalizedCreateDigest(command, records)
		if err != nil {
			return err
		}
		stored, found, err := transaction.FindCreateResult(ctx, command.Actor, command.IdempotencyKey)
		if err != nil {
			return fmt.Errorf("find create request: %w", err)
		}
		if found {
			if stored.RequestDigest != requestDigest {
				return fmt.Errorf("%w: create request", release.ErrIdempotencyKeyReused)
			}
			result := stored.Result
			replayed = &result
			return nil
		}
		authority, err := transaction.LoadBaseAuthority(ctx, bundle.Definition.Name(), strings.TrimSpace(command.Scope.Environment), recordKeys)
		if err != nil {
			return fmt.Errorf("load base authority: %w", err)
		}

		if bundle.Template.Code == "" || bundle.Template.Version == 0 || bundle.Template.ReleaseTypeCode == "" {
			return errors.New("release catalog has no active BASE_FINAL template")
		}
		orderID := service.ids.NewID()
		itemSpecs := make([]release.BaseFinalItemSpec, len(command.Items))
		for index, draft := range command.Items {
			if authority.CollectionRevision != draft.ExpectedCollectionRevision {
				return fmt.Errorf("%w: collection revision is %d, expected %d", release.ErrAborted, authority.CollectionRevision, draft.ExpectedCollectionRevision)
			}
			if existing := authority.Records[records[index].RecordKey]; existing != nil {
				return fmt.Errorf("%w: ADD target %q already exists", release.ErrAborted, records[index].RecordKey)
			}
			if draft.ExpectedRecordRevision != 0 {
				return fmt.Errorf("%w: ADD expected record revision must be zero", release.ErrInvalid)
			}
			itemSpecs[index] = release.BaseFinalItemSpec{
				ID:                         service.ids.NewID(),
				After:                      records[index],
				ExpectedRecordRevision:     draft.ExpectedRecordRevision,
				ExpectedCollectionRevision: draft.ExpectedCollectionRevision,
			}
		}
		now := service.clock.Now().UTC()
		order, err := release.NewBaseFinalOrder(release.BaseFinalOrderSpec{
			ID:             orderID,
			ReleaseNumber:  service.ids.NewReleaseNumber(now),
			IdempotencyKey: command.IdempotencyKey,
			ModelCode:      command.ModelCode,
			TemplateCode:   bundle.Template.Code, TemplateVersion: bundle.Template.Version,
			ReleaseTypeCode: bundle.Template.ReleaseTypeCode, RequestDigest: requestDigest,
			Scope:     command.Scope,
			CreatedBy: command.Actor,
			CreatedAt: now,
			Items:     itemSpecs,
			Template:  bundle.Template.Definition,
		})
		if err != nil {
			return err
		}
		if err := transaction.InsertOrder(ctx, order); err != nil {
			return fmt.Errorf("insert release order: %w", err)
		}
		created = order
		return nil
	})
	if err != nil {
		return OrderView{}, err
	}
	if replayed != nil {
		return *replayed, nil
	}
	return project(created), nil
}

func (service *Service) CreateRelease(ctx context.Context, command CreateReleaseCommand) (OrderView, error) {
	if err := service.ready(); err != nil {
		return OrderView{}, err
	}
	if strings.TrimSpace(command.IdempotencyKey) == "" || strings.TrimSpace(command.ModelCode) == "" || strings.TrimSpace(command.ReleaseTypeCode) == "" || strings.TrimSpace(command.Actor) == "" || len(command.Items) == 0 || len(command.Items) > 500 {
		return OrderView{}, fmt.Errorf("%w: idempotency key, model, release type, actor, and 1..500 items are required", release.ErrInvalid)
	}

	var created *release.Order
	var replayed *OrderView
	err := service.withinIdempotentTransaction(ctx, func(transaction Transaction) error {
		bundle, err := transaction.LoadCatalog(ctx, command.ModelCode, command.ReleaseTypeCode)
		if err != nil {
			return fmt.Errorf("load release catalog: %w", err)
		}
		finalEffect := bundle.Template.Definition.FinalEffect()
		if finalEffect != release.FinalEffectBase && finalEffect != release.FinalEffectOverlay {
			return fmt.Errorf("%w: release template final effect is invalid", release.ErrInvalid)
		}
		if finalEffect == release.FinalEffectOverlay && (strings.TrimSpace(command.Scope.Region) == "" || strings.TrimSpace(command.Scope.Environment) == "" || strings.TrimSpace(command.Scope.Stage) == "") {
			return fmt.Errorf("%w: OVERLAY_FINAL requires a full scope", release.ErrInvalid)
		}

		canonical := make([]canonicalReleaseDraft, len(command.Items))
		keys := make([]string, len(command.Items))
		for index, draft := range command.Items {
			baseBefore, err := canonicalOptionalRecord(bundle.Definition, command.Scope.Environment, draft.BaseBefore)
			if err != nil {
				return fmt.Errorf("canonicalize item %d base before: %w", index, err)
			}
			effectiveBefore, err := canonicalOptionalRecord(bundle.Definition, command.Scope.Environment, draft.EffectiveBefore)
			if err != nil {
				return fmt.Errorf("canonicalize item %d effective before: %w", index, err)
			}
			after, err := canonicalOptionalRecord(bundle.Definition, command.Scope.Environment, draft.After)
			if err != nil {
				return fmt.Errorf("canonicalize item %d after: %w", index, err)
			}
			target := after
			if target == nil {
				target = effectiveBefore
			}
			if target == nil {
				target = baseBefore
			}
			if target == nil {
				return fmt.Errorf("%w: item %d has no target state", release.ErrInvalid, index)
			}
			for _, record := range []*catalog.ConfigurationRecord{baseBefore, effectiveBefore, after} {
				if record != nil && record.RecordKey != target.RecordKey {
					return fmt.Errorf("%w: item %d record keys differ", release.ErrInvalid, index)
				}
			}
			keys[index] = target.RecordKey
			canonical[index] = canonicalReleaseDraft{
				action: draft.Action, baseBefore: baseBefore, effectiveBefore: effectiveBefore, after: after,
				expectedRecordRevision: draft.ExpectedRecordRevision, expectedCollectionRevision: draft.ExpectedCollectionRevision,
			}
		}
		requestDigest, err := normalizedReleaseDigest(command, canonical)
		if err != nil {
			return err
		}
		stored, found, err := transaction.FindCreateResult(ctx, command.Actor, command.IdempotencyKey)
		if err != nil {
			return fmt.Errorf("find create request: %w", err)
		}
		if found {
			if stored.RequestDigest != requestDigest {
				return fmt.Errorf("%w: create request", release.ErrIdempotencyKeyReused)
			}
			result := stored.Result
			replayed = &result
			return nil
		}

		authority, err := transaction.LoadBaseAuthority(ctx, bundle.Definition.Name(), command.Scope.Environment, keys)
		if err != nil {
			return fmt.Errorf("load base authority: %w", err)
		}
		if finalEffect == release.FinalEffectBase {
			orderID := service.ids.NewID()
			items := make([]release.BaseFinalItemSpec, len(canonical))
			for index, draft := range canonical {
				if draft.action != release.ChangeAdd || draft.baseBefore != nil || draft.effectiveBefore != nil || draft.after == nil || draft.expectedRecordRevision != 0 {
					return fmt.Errorf("%w: BASE_FINAL item %d must be ADD without before images", release.ErrInvalid, index)
				}
				if authority.CollectionRevision != draft.expectedCollectionRevision || authority.Records[keys[index]] != nil {
					return fmt.Errorf("%w: BASE_FINAL item %d authority is stale", release.ErrAborted, index)
				}
				items[index] = release.BaseFinalItemSpec{
					ID: service.ids.NewID(), After: *draft.after,
					ExpectedRecordRevision: draft.expectedRecordRevision, ExpectedCollectionRevision: draft.expectedCollectionRevision,
				}
			}
			now := service.clock.Now().UTC()
			order, err := release.NewBaseFinalOrder(release.BaseFinalOrderSpec{
				ID: orderID, ReleaseNumber: service.ids.NewReleaseNumber(now), IdempotencyKey: command.IdempotencyKey,
				ModelCode: command.ModelCode, TemplateCode: bundle.Template.Code, TemplateVersion: bundle.Template.Version,
				ReleaseTypeCode: bundle.Template.ReleaseTypeCode, RequestDigest: requestDigest, Scope: command.Scope,
				CreatedBy: command.Actor, CreatedAt: now, Items: items, Template: bundle.Template.Definition,
			})
			if err != nil {
				return err
			}
			if err := transaction.InsertOrder(ctx, order); err != nil {
				return fmt.Errorf("insert release order: %w", err)
			}
			created = order
			return nil
		}
		rules, err := transaction.LoadOverlayRules(ctx, bundle.Definition.Name(), command.Scope, keys)
		if err != nil {
			return fmt.Errorf("load overlay rules: %w", err)
		}
		baseRecords := make([]catalog.ConfigurationRecord, 0, len(authority.Records))
		for _, record := range authority.Records {
			if record != nil {
				baseRecords = append(baseRecords, *record)
			}
		}
		effectiveRecords, err := overlay.Evaluate(overlay.Query{
			Collection: bundle.Definition.Name(),
			Scope:      overlay.Scope{Region: command.Scope.Region, Environment: command.Scope.Environment, Stage: command.Scope.Stage},
		}, baseRecords, rules)
		if err != nil {
			return fmt.Errorf("evaluate current scope: %w", err)
		}
		effectiveByKey := make(map[string]*catalog.ConfigurationRecord, len(effectiveRecords))
		for index := range effectiveRecords {
			record := effectiveRecords[index]
			effectiveByKey[record.RecordKey] = &record
		}

		itemSpecs := make([]release.OverlayFinalItemSpec, len(canonical))
		for index, draft := range canonical {
			actualBase := authority.Records[keys[index]]
			actualEffective := effectiveByKey[keys[index]]
			if authority.CollectionRevision != draft.expectedCollectionRevision || !sameRecordData(actualBase, draft.baseBefore) || !sameRecordData(actualEffective, draft.effectiveBefore) {
				return fmt.Errorf("%w: item %d page authority is stale", release.ErrAborted, index)
			}
			actualRecordRevision := catalog.ConfigRevision(0)
			if actualBase != nil {
				actualRecordRevision = actualBase.ConfigRevision
			}
			if actualRecordRevision != draft.expectedRecordRevision {
				return fmt.Errorf("%w: item %d base revision is %d, expected %d", release.ErrAborted, index, actualRecordRevision, draft.expectedRecordRevision)
			}
			if !validEffectiveTransition(draft.action, actualBase, actualEffective, draft.after) {
				return fmt.Errorf("%w: item %d transition is invalid for %s", release.ErrInvalid, index, draft.action)
			}
			itemSpecs[index] = release.OverlayFinalItemSpec{
				ID: service.ids.NewID(), Action: draft.action, BaseBefore: actualBase, EffectiveBefore: actualEffective, After: draft.after,
				ExpectedRecordRevision: draft.expectedRecordRevision, ExpectedCollectionRevision: draft.expectedCollectionRevision,
			}
		}
		now := service.clock.Now().UTC()
		order, err := release.NewOverlayFinalOrder(release.OverlayFinalOrderSpec{
			ID: service.ids.NewID(), ReleaseNumber: service.ids.NewReleaseNumber(now), IdempotencyKey: command.IdempotencyKey,
			ModelCode: command.ModelCode, TemplateCode: bundle.Template.Code, TemplateVersion: bundle.Template.Version,
			ReleaseTypeCode: bundle.Template.ReleaseTypeCode, RequestDigest: requestDigest, Scope: command.Scope,
			CreatedBy: command.Actor, CreatedAt: now, Items: itemSpecs, Template: bundle.Template.Definition,
		})
		if err != nil {
			return err
		}
		if err := transaction.InsertOrder(ctx, order); err != nil {
			return fmt.Errorf("insert release order: %w", err)
		}
		created = order
		return nil
	})
	if err != nil {
		return OrderView{}, err
	}
	if replayed != nil {
		return *replayed, nil
	}
	return project(created), nil
}

func canonicalOptionalRecord(definition catalog.CollectionDefinition, environment string, data map[string]string) (*catalog.ConfigurationRecord, error) {
	if data == nil {
		return nil, nil
	}
	record, err := definition.NewRecord(environment, data)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func sameRecordData(actual, submitted *catalog.ConfigurationRecord) bool {
	if actual == nil || submitted == nil {
		return actual == nil && submitted == nil
	}
	return actual.RecordKey == submitted.RecordKey && maps.Equal(actual.Data, submitted.Data)
}

func validEffectiveTransition(action release.ChangeAction, base, effective, after *catalog.ConfigurationRecord) bool {
	switch action {
	case release.ChangeAdd:
		return base == nil && effective == nil && after != nil
	case release.ChangeModify:
		return base != nil && effective != nil && after != nil
	case release.ChangeDelete:
		return base != nil && effective != nil && after == nil
	default:
		return false
	}
}

func normalizedReleaseDigest(command CreateReleaseCommand, drafts []canonicalReleaseDraft) (string, error) {
	type digestItem struct {
		Action                     release.ChangeAction         `json:"action"`
		BaseBefore                 *catalog.ConfigurationRecord `json:"baseBefore"`
		EffectiveBefore            *catalog.ConfigurationRecord `json:"effectiveBefore"`
		After                      *catalog.ConfigurationRecord `json:"after"`
		ExpectedRecordRevision     catalog.ConfigRevision       `json:"expectedRecordRevision"`
		ExpectedCollectionRevision catalog.ConfigRevision       `json:"expectedCollectionRevision"`
	}
	items := make([]digestItem, len(drafts))
	for index, draft := range drafts {
		items[index] = digestItem{
			Action: draft.action, BaseBefore: draft.baseBefore, EffectiveBefore: draft.effectiveBefore, After: draft.after,
			ExpectedRecordRevision: draft.expectedRecordRevision, ExpectedCollectionRevision: draft.expectedCollectionRevision,
		}
	}
	payload := struct {
		Model       string        `json:"model"`
		ReleaseType string        `json:"releaseType"`
		Scope       release.Scope `json:"scope"`
		Items       any           `json:"items"`
	}{Model: command.ModelCode, ReleaseType: command.ReleaseTypeCode, Scope: command.Scope, Items: items}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("normalize create release request: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func normalizedCreateDigest(command CreateBaseFinalCommand, records []catalog.ConfigurationRecord) (string, error) {
	type digestItem struct {
		RecordKey                  string                 `json:"recordKey"`
		Data                       map[string]string      `json:"data"`
		ExpectedRecordRevision     catalog.ConfigRevision `json:"expectedRecordRevision"`
		ExpectedCollectionRevision catalog.ConfigRevision `json:"expectedCollectionRevision"`
	}
	items := make([]digestItem, len(records))
	for index, record := range records {
		items[index] = digestItem{RecordKey: record.RecordKey, Data: record.Data, ExpectedRecordRevision: command.Items[index].ExpectedRecordRevision, ExpectedCollectionRevision: command.Items[index].ExpectedCollectionRevision}
	}
	payload := struct {
		Model       string        `json:"model"`
		ReleaseType string        `json:"releaseType"`
		Scope       release.Scope `json:"scope"`
		Items       []digestItem  `json:"items"`
	}{Model: command.ModelCode, ReleaseType: command.ReleaseTypeCode, Scope: command.Scope, Items: items}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("normalize create release request: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func (service *Service) Act(ctx context.Context, command ActCommand) (OrderView, error) {
	if err := service.ready(); err != nil {
		return OrderView{}, err
	}
	if strings.TrimSpace(command.OrderID) == "" || strings.TrimSpace(command.ActionRequestID) == "" || strings.TrimSpace(command.Actor) == "" || command.ExpectedCurrentStep == "" {
		return OrderView{}, fmt.Errorf("%w: order, action request, and actor are required", release.ErrInvalid)
	}
	requestDigest, err := normalizedActionDigest(command)
	if err != nil {
		return OrderView{}, err
	}
	var result OrderView
	err = service.withinIdempotentTransaction(ctx, func(transaction Transaction) error {
		order, err := transaction.LoadOrderForUpdate(ctx, command.OrderID)
		if err != nil {
			return fmt.Errorf("load release order: %w", err)
		}
		stored, found, err := transaction.FindActionResult(ctx, command.OrderID, command.ActionRequestID)
		if err != nil {
			return fmt.Errorf("find action request: %w", err)
		}
		if found {
			if stored.RequestDigest != requestDigest {
				return fmt.Errorf("%w: action request", release.ErrIdempotencyKeyReused)
			}
			result = stored.Result
			return nil
		}
		if order.Revision() != command.ExpectedRevision {
			return fmt.Errorf("%w: order revision is %d, expected %d", release.ErrAborted, order.Revision(), command.ExpectedRevision)
		}
		if order.CurrentStep().Code != command.ExpectedCurrentStep {
			return fmt.Errorf("%w: current step is %s, expected %s", release.ErrAborted, order.CurrentStep().Code, command.ExpectedCurrentStep)
		}
		stepBefore := order.CurrentStep()
		now := service.clock.Now().UTC()
		switch command.Action {
		case ActionAdvance:
			if err := order.Advance(command.ExpectedRevision, command.Actor, now); err != nil {
				return err
			}
		case ActionExecute:
			switch order.CurrentStep().Type {
			case release.StepManualReview:
				if err := order.ExecuteManualReview(command.ExpectedRevision, command.Actor, now); err != nil {
					return err
				}
			case release.StepBaseApply:
				authority, err := loadBaseApplyAuthority(ctx, transaction, order)
				if err != nil {
					return err
				}
				revision, err := transaction.AllocateConfigRevision(ctx)
				if err != nil {
					return fmt.Errorf("allocate config revision: %w", err)
				}
				effect, err := order.ExecuteBase(authority, revision, command.Actor, now)
				if err != nil {
					return err
				}
				if err := transaction.ApplyBaseEffect(ctx, order.ID(), effect); err != nil {
					return fmt.Errorf("apply base effect: %w", err)
				}
			case release.StepOverlayApply:
				authority, err := loadOverlayAuthority(ctx, transaction, order)
				if err != nil {
					return err
				}
				revision, err := transaction.AllocateConfigRevision(ctx)
				if err != nil {
					return fmt.Errorf("allocate config revision: %w", err)
				}
				effect, err := order.ExecuteOverlay(authority, revision, command.Actor, now)
				if err != nil {
					return err
				}
				if err := transaction.ApplyOverlayEffect(ctx, order.ID(), effect); err != nil {
					return fmt.Errorf("apply overlay effect: %w", err)
				}
			case release.StepPercentRollout:
				authority, err := loadOverlayAuthority(ctx, transaction, order)
				if err != nil {
					return err
				}
				revision, err := transaction.AllocateConfigRevision(ctx)
				if err != nil {
					return fmt.Errorf("allocate config revision: %w", err)
				}
				effect, err := order.ExecutePercentRollout(authority, revision, command.Actor, now)
				if err != nil {
					return err
				}
				if err := transaction.ApplyPercentEffect(ctx, order.ID(), effect); err != nil {
					return fmt.Errorf("apply percentage effect: %w", err)
				}
			case release.StepComplete:
				if err := order.Complete(command.ExpectedRevision, command.Actor, now); err != nil {
					return err
				}
			default:
				return fmt.Errorf("%w: unsupported step %q", release.ErrInvalid, order.CurrentStep().Type)
			}
		case ActionApprove:
			if err := order.ApproveManualReview(command.ExpectedRevision, release.Principal{Subject: command.Actor, Roles: command.Roles}, command.Comment, now); err != nil {
				return err
			}
		case ActionReject:
			if err := order.RejectManualReview(command.ExpectedRevision, release.Principal{Subject: command.Actor, Roles: command.Roles}, command.Comment, now); err != nil {
				return err
			}
		case ActionRollback:
			switch order.CurrentStep().Type {
			case release.StepOverlayApply:
				authority, err := loadOverlayAuthority(ctx, transaction, order)
				if err != nil {
					return err
				}
				revision, err := transaction.AllocateConfigRevision(ctx)
				if err != nil {
					return fmt.Errorf("allocate config revision: %w", err)
				}
				effect, err := order.RollbackOverlay(command.ExpectedRevision, authority.CollectionRevision, revision, command.Actor, now)
				if err != nil {
					return err
				}
				if err := transaction.ApplyOverlayEffect(ctx, order.ID(), effect); err != nil {
					return fmt.Errorf("apply overlay compensation: %w", err)
				}
			case release.StepBaseApply:
				authority, err := loadBaseApplyAuthority(ctx, transaction, order)
				if err != nil {
					return err
				}
				revision, err := transaction.AllocateConfigRevision(ctx)
				if err != nil {
					return fmt.Errorf("allocate config revision: %w", err)
				}
				effect, err := order.RollbackBase(command.ExpectedRevision, authority, revision, command.Actor, now)
				if err != nil {
					return err
				}
				if err := transaction.ApplyBaseEffect(ctx, order.ID(), effect); err != nil {
					return fmt.Errorf("apply base compensation: %w", err)
				}
			default:
				return fmt.Errorf("%w: current step cannot be rolled back", release.ErrInvalid)
			}
		default:
			return fmt.Errorf("%w: unsupported action %q", release.ErrInvalid, command.Action)
		}
		if err := transaction.SaveOrder(ctx, order); err != nil {
			return fmt.Errorf("save release order: %w", err)
		}
		if err := transaction.RecordAction(ctx, ActionRecord{OrderID: order.ID(), StepCode: stepBefore.Code, Action: command.Action, Actor: command.Actor, Comment: command.Comment, Scope: order.Scope(), At: now}); err != nil {
			return fmt.Errorf("record release action: %w", err)
		}
		result = project(order)
		if err := transaction.InsertActionResult(ctx, command.OrderID, command.ActionRequestID, requestDigest, result, now); err != nil {
			return fmt.Errorf("insert action request: %w", err)
		}
		return nil
	})
	if err != nil {
		return OrderView{}, err
	}
	return result, nil
}

func loadOverlayAuthority(ctx context.Context, transaction Transaction, order *release.Order) (release.OverlayAuthority, error) {
	items := order.Items()
	keys := make([]string, len(items))
	for index, item := range items {
		keys[index] = item.RecordKey
	}
	base, err := transaction.LoadBaseAuthority(ctx, items[0].Collection, order.Scope().Environment, keys)
	if err != nil {
		return release.OverlayAuthority{}, fmt.Errorf("load base authority: %w", err)
	}
	rules, err := transaction.LoadOverlayRules(ctx, items[0].Collection, order.Scope(), keys)
	if err != nil {
		return release.OverlayAuthority{}, fmt.Errorf("load overlay rules: %w", err)
	}
	exact := make(map[string]*overlay.Rule, len(keys))
	for index := range rules {
		rule := rules[index]
		if rule.Scope.Stage == order.Scope().Stage {
			exact[rule.RecordKey] = &rule
		}
	}
	return release.OverlayAuthority{CollectionRevision: base.CollectionRevision, BaseRecords: base.Records, Rules: exact}, nil
}

func loadBaseApplyAuthority(ctx context.Context, transaction Transaction, order *release.Order) (release.BaseAuthority, error) {
	items := order.Items()
	keys := make([]string, len(items))
	for index, item := range items {
		keys[index] = item.RecordKey
	}
	authority, err := transaction.LoadBaseAuthority(ctx, items[0].Collection, order.Scope().Environment, keys)
	if err != nil {
		return release.BaseAuthority{}, fmt.Errorf("load base authority: %w", err)
	}
	rules, err := transaction.LoadOverlayRules(ctx, items[0].Collection, order.Scope(), keys)
	if err != nil {
		return release.BaseAuthority{}, fmt.Errorf("load base cleanup rules: %w", err)
	}
	authority.Rules = make(map[string]*overlay.Rule, len(keys))
	for index := range rules {
		rule := rules[index]
		if rule.Scope.Stage == order.Scope().Stage {
			authority.Rules[rule.RecordKey] = &rule
		}
	}
	return authority, nil
}

func normalizedActionDigest(command ActCommand) (string, error) {
	roles := append([]string(nil), command.Roles...)
	sort.Strings(roles)
	payload := struct {
		OrderID             string                 `json:"orderId"`
		ExpectedRevision    release.EntityRevision `json:"expectedRevision"`
		ExpectedCurrentStep string                 `json:"expectedCurrentStep"`
		Action              Action                 `json:"action"`
		Actor               string                 `json:"actor"`
		Roles               []string               `json:"roles"`
		Comment             string                 `json:"comment"`
	}{command.OrderID, command.ExpectedRevision, command.ExpectedCurrentStep, command.Action, command.Actor, roles, command.Comment}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("normalize release action request: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func (service *Service) withinIdempotentTransaction(ctx context.Context, work func(Transaction) error) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = service.unitOfWork.WithinTransaction(ctx, work)
		if !errors.Is(err, ErrRetryableTransaction) && !errors.Is(err, ErrCreateRequestRace) {
			return err
		}
		if attempt == 2 {
			break
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func (service *Service) ready() error {
	if service == nil || service.unitOfWork == nil || service.ids == nil || service.clock == nil {
		return errors.New("release service dependencies are incomplete")
	}
	return nil
}

func project(order *release.Order) OrderView {
	step := order.CurrentStep()
	state := order.State()
	steps := make([]StepView, len(state.Steps))
	for index, stateStep := range state.Steps {
		steps[index] = StepView{Code: stateStep.Code, Type: stateStep.Type, Status: stateStep.Status}
	}
	view := OrderView{ID: order.ID(), Status: order.Status(), CurrentStepCode: step.Code, CurrentStep: step.Type, CurrentStepStatus: step.Status, Revision: order.Revision(), Steps: steps}
	applyCapabilities(&view)
	return view
}

func applyCapabilities(view *OrderView) {
	if view.Status != release.OrderInProgress {
		return
	}
	switch {
	case view.CurrentStep == release.StepManualReview && view.CurrentStepStatus == release.StepPending:
		view.CanExecute = true
	case view.CurrentStep == release.StepManualReview && view.CurrentStepStatus == release.StepExecuting:
		view.CanApprove = true
		view.CanReject = true
	case view.CurrentStepStatus == release.StepApproved || view.CurrentStepStatus == release.StepExecuted:
		view.CanAdvance = view.CurrentStep != release.StepComplete
		view.CanRollback = (view.CurrentStep == release.StepOverlayApply || view.CurrentStep == release.StepBaseApply) && view.CurrentStepStatus == release.StepExecuted
	case (view.CurrentStep == release.StepBaseApply || view.CurrentStep == release.StepOverlayApply || view.CurrentStep == release.StepPercentRollout || view.CurrentStep == release.StepComplete) && view.CurrentStepStatus == release.StepPending:
		view.CanExecute = true
	}
}

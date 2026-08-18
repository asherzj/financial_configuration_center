package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
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

type Transaction interface {
	LoadCatalog(context.Context, string) (CatalogBundle, error)
	LoadBaseAuthority(context.Context, string, string, []string) (release.BaseAuthority, error)
	InsertOrder(context.Context, *release.Order) error
	LoadOrderForUpdate(context.Context, string) (*release.Order, error)
	AllocateConfigRevision(context.Context) (catalog.ConfigRevision, error)
	ApplyBaseEffect(context.Context, string, release.BaseEffect, catalog.ConfigRevision) error
	SaveOrder(context.Context, *release.Order) error
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
}

type AddDraft struct {
	Data                       map[string]string
	ExpectedRecordRevision     catalog.ConfigRevision
	ExpectedCollectionRevision catalog.ConfigRevision
}

type CreateBaseFinalCommand struct {
	IdempotencyKey string
	ModelCode      string
	Scope          release.Scope
	Actor          string
	Items          []AddDraft
}

type Action string

const (
	ActionExecute Action = "EXECUTE"
	ActionAdvance Action = "ADVANCE"
)

type ActCommand struct {
	OrderID          string
	ActionRequestID  string
	ExpectedRevision release.EntityRevision
	Action           Action
	Actor            string
}

type OrderView struct {
	ID          string
	Status      release.OrderStatus
	CurrentStep release.StepType
	Revision    release.EntityRevision
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

	var created *release.Order
	err := service.unitOfWork.WithinTransaction(ctx, func(transaction Transaction) error {
		bundle, err := transaction.LoadCatalog(ctx, command.ModelCode)
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
		requestDigest, err := normalizedCreateDigest(command, records)
		if err != nil {
			return err
		}
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
	return project(created), nil
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
		Model string        `json:"model"`
		Scope release.Scope `json:"scope"`
		Items []digestItem  `json:"items"`
	}{Model: command.ModelCode, Scope: command.Scope, Items: items}
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
	if strings.TrimSpace(command.OrderID) == "" || strings.TrimSpace(command.ActionRequestID) == "" || strings.TrimSpace(command.Actor) == "" {
		return OrderView{}, fmt.Errorf("%w: order, action request, and actor are required", release.ErrInvalid)
	}
	var result *release.Order
	err := service.unitOfWork.WithinTransaction(ctx, func(transaction Transaction) error {
		order, err := transaction.LoadOrderForUpdate(ctx, command.OrderID)
		if err != nil {
			return fmt.Errorf("load release order: %w", err)
		}
		if order.Revision() != command.ExpectedRevision {
			return fmt.Errorf("%w: order revision is %d, expected %d", release.ErrAborted, order.Revision(), command.ExpectedRevision)
		}
		now := service.clock.Now().UTC()
		switch command.Action {
		case ActionAdvance:
			if err := order.Advance(command.ExpectedRevision, command.Actor, now); err != nil {
				return err
			}
		case ActionExecute:
			switch order.CurrentStep().Type {
			case release.StepBaseApply:
				items := order.Items()
				keys := make([]string, len(items))
				for index, item := range items {
					keys[index] = item.RecordKey
				}
				authority, err := transaction.LoadBaseAuthority(ctx, items[0].Collection, order.Scope().Environment, keys)
				if err != nil {
					return fmt.Errorf("load base authority: %w", err)
				}
				effect, err := order.ExecuteBase(authority, command.Actor, now)
				if err != nil {
					return err
				}
				revision, err := transaction.AllocateConfigRevision(ctx)
				if err != nil {
					return fmt.Errorf("allocate config revision: %w", err)
				}
				if err := transaction.ApplyBaseEffect(ctx, order.ID(), effect, revision); err != nil {
					return fmt.Errorf("apply base effect: %w", err)
				}
			case release.StepComplete:
				if err := order.Complete(command.ExpectedRevision, command.Actor, now); err != nil {
					return err
				}
			default:
				return fmt.Errorf("%w: unsupported step %q", release.ErrInvalid, order.CurrentStep().Type)
			}
		default:
			return fmt.Errorf("%w: unsupported action %q", release.ErrInvalid, command.Action)
		}
		if err := transaction.SaveOrder(ctx, order); err != nil {
			return fmt.Errorf("save release order: %w", err)
		}
		result = order
		return nil
	})
	if err != nil {
		return OrderView{}, err
	}
	return project(result), nil
}

func (service *Service) ready() error {
	if service == nil || service.unitOfWork == nil || service.ids == nil || service.clock == nil {
		return errors.New("release service dependencies are incomplete")
	}
	return nil
}

func project(order *release.Order) OrderView {
	return OrderView{
		ID:          order.ID(),
		Status:      order.Status(),
		CurrentStep: order.CurrentStep().Type,
		Revision:    order.Revision(),
	}
}

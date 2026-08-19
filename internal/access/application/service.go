package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
)

var (
	ErrInvalid             = errors.New("invalid sensitive access request")
	ErrForbidden           = errors.New("sensitive access forbidden")
	ErrAborted             = errors.New("sensitive access authority is stale")
	ErrNotFound            = errors.New("sensitive field value not found")
	ErrFailedPrecondition  = errors.New("sensitive access precondition failed")
	ErrSnapshotUnavailable = errors.New("snapshot authority is unavailable")
)

const SensitiveViewerRole = "SENSITIVE_VIEWER"

type Clock interface{ Now() time.Time }

type SnapshotAuthorityReader interface {
	ReadSnapshotAuthority(context.Context, SnapshotAuthorityQuery) (SnapshotAuthority, error)
}

type SnapshotAuthorityQuery struct {
	ModelCode     string
	Scope         Scope
	PreviewBucket *int32
}

type SnapshotAuthority struct {
	Found              bool
	Environment        string
	ServerEpoch        string
	SnapshotInstance   string
	SnapshotGeneration uint64
	ModelRevision      catalog.ConfigRevision
	CollectionRevision catalog.ConfigRevision
}

type UnitOfWork interface {
	WithinTransaction(context.Context, func(Transaction) error) error
}

type Transaction interface {
	LoadCatalog(context.Context, string) (CatalogAuthority, error)
	LoadRecordAuthority(context.Context, string, Scope, string) (RecordAuthority, error)
	InsertRevealAudit(context.Context, AuditEntry) error
}

type CatalogAuthority struct {
	Definition catalog.CollectionDefinition
	Model      catalog.CompiledModel
}

type RecordAuthority struct {
	CollectionRevision catalog.ConfigRevision
	BaseRecords        []catalog.ConfigurationRecord
	Rules              []overlay.Rule
}

type Scope struct {
	Region      string
	Environment string
	Stage       string
}

type Principal struct {
	Subject     string
	DisplayName string
	Roles       []string
}

type RevealCommand struct {
	ModelCode                  string
	Scope                      Scope
	RecordKey                  string
	FieldName                  string
	ExpectedRecordRevision     catalog.ConfigRevision
	ExpectedCollectionRevision catalog.ConfigRevision
	ExpectedModelRevision      catalog.ConfigRevision
	ExpectedServerEpoch        string
	ExpectedSnapshotInstance   string
	ExpectedSnapshotGeneration uint64
	Reason                     string
	PreviewBucket              *int32
	RequestID                  string
	TraceID                    string
	Principal                  Principal
}

type RevealResult struct {
	Value     string
	ExpiresAt time.Time
}

type AuditEntry struct {
	OccurredAt   time.Time
	Principal    Principal
	Action       string
	ResourceType string
	ResourceID   string
	Scope        Scope
	Result       string
	RequestID    string
	TraceID      string
	Metadata     map[string]any
}

type Service struct {
	unitOfWork UnitOfWork
	snapshots  SnapshotAuthorityReader
	clock      Clock
}

func NewService(unitOfWork UnitOfWork, snapshots SnapshotAuthorityReader, clock Clock) *Service {
	return &Service{unitOfWork: unitOfWork, snapshots: snapshots, clock: clock}
}

func (service *Service) Reveal(ctx context.Context, command RevealCommand) (RevealResult, error) {
	if service == nil || service.unitOfWork == nil || service.snapshots == nil || isNilSnapshotAuthorityReader(service.snapshots) || service.clock == nil {
		return RevealResult{}, errors.New("sensitive access service dependencies are required")
	}
	if err := validateCommand(command); err != nil {
		return RevealResult{}, err
	}
	if !slices.Contains(command.Principal.Roles, SensitiveViewerRole) {
		return RevealResult{}, ErrForbidden
	}
	authority, err := service.snapshots.ReadSnapshotAuthority(ctx, SnapshotAuthorityQuery{
		ModelCode: command.ModelCode, Scope: command.Scope, PreviewBucket: command.PreviewBucket,
	})
	if errors.Is(err, ErrSnapshotUnavailable) {
		return RevealResult{}, ErrFailedPrecondition
	}
	if err != nil {
		return RevealResult{}, fmt.Errorf("load sensitive access snapshot authority: %w", err)
	}
	if !authority.Found || authority.Environment != command.Scope.Environment || authority.ServerEpoch != command.ExpectedServerEpoch || authority.SnapshotInstance != command.ExpectedSnapshotInstance || authority.SnapshotGeneration != command.ExpectedSnapshotGeneration {
		return RevealResult{}, ErrAborted
	}
	if authority.ModelRevision != command.ExpectedModelRevision {
		return RevealResult{}, ErrAborted
	}
	if authority.CollectionRevision != command.ExpectedCollectionRevision {
		return RevealResult{}, ErrAborted
	}

	now := service.clock.Now().UTC()
	var revealed string
	err = service.unitOfWork.WithinTransaction(ctx, func(transaction Transaction) error {
		catalogAuthority, err := transaction.LoadCatalog(ctx, command.ModelCode)
		if err != nil {
			return fmt.Errorf("load sensitive access catalog: %w", err)
		}
		if catalogAuthority.Model.ConfigRevision() != command.ExpectedModelRevision || catalogAuthority.Model.Collection() != catalogAuthority.Definition.Name() {
			return ErrAborted
		}
		field, exists := catalogAuthority.Definition.Field(command.FieldName)
		if !exists || !field.Sensitive || !modelExposesSensitiveField(catalogAuthority.Model, command.FieldName) {
			return ErrFailedPrecondition
		}
		authority, err := transaction.LoadRecordAuthority(ctx, catalogAuthority.Definition.Name(), command.Scope, command.RecordKey)
		if err != nil {
			return fmt.Errorf("load sensitive record authority: %w", err)
		}
		if authority.CollectionRevision != command.ExpectedCollectionRevision {
			return ErrAborted
		}
		effective, err := overlay.Evaluate(overlay.Query{
			Collection:    catalogAuthority.Definition.Name(),
			Scope:         overlay.Scope{Region: command.Scope.Region, Environment: command.Scope.Environment, Stage: command.Scope.Stage},
			PreviewBucket: command.PreviewBucket,
		}, authority.BaseRecords, authority.Rules)
		if err != nil {
			return fmt.Errorf("evaluate sensitive record: %w", err)
		}
		var record *catalog.ConfigurationRecord
		for index := range effective {
			if effective[index].RecordKey == command.RecordKey {
				record = &effective[index]
				break
			}
		}
		if record == nil {
			return ErrNotFound
		}
		if record.ConfigRevision != command.ExpectedRecordRevision {
			return ErrAborted
		}
		value, exists := record.Data[command.FieldName]
		if !exists {
			return ErrNotFound
		}
		resourceHash := sha256.Sum256([]byte(catalogAuthority.Definition.Name() + "\x00" + command.RecordKey))
		if err := transaction.InsertRevealAudit(ctx, AuditEntry{
			OccurredAt: now, Principal: command.Principal, Action: "SENSITIVE_FIELD_REVEALED",
			ResourceType: "CONFIGURATION_RECORD", ResourceID: hex.EncodeToString(resourceHash[:]),
			Scope: command.Scope, Result: "SUCCEEDED", RequestID: command.RequestID, TraceID: command.TraceID,
			Metadata: map[string]any{
				"modelCode": command.ModelCode, "collection": catalogAuthority.Definition.Name(), "recordKey": command.RecordKey,
				"fieldName": command.FieldName, "reason": command.Reason, "recordRevision": command.ExpectedRecordRevision,
				"collectionRevision": command.ExpectedCollectionRevision, "modelRevision": command.ExpectedModelRevision,
				"serverEpoch": command.ExpectedServerEpoch, "snapshotInstance": command.ExpectedSnapshotInstance,
				"snapshotGeneration": command.ExpectedSnapshotGeneration,
			},
		}); err != nil {
			return fmt.Errorf("insert sensitive reveal audit: %w", err)
		}
		revealed = value
		return nil
	})
	if err != nil {
		return RevealResult{}, err
	}
	return RevealResult{Value: revealed, ExpiresAt: now.Add(60 * time.Second)}, nil
}

func isNilSnapshotAuthorityReader(reader SnapshotAuthorityReader) bool {
	value := reflect.ValueOf(reader)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func validateCommand(command RevealCommand) error {
	if strings.TrimSpace(command.ModelCode) == "" || strings.TrimSpace(command.Scope.Region) == "" || strings.TrimSpace(command.Scope.Environment) == "" || strings.TrimSpace(command.RecordKey) == "" || strings.TrimSpace(command.FieldName) == "" || strings.TrimSpace(command.Reason) == "" || strings.TrimSpace(command.RequestID) == "" || strings.TrimSpace(command.Principal.Subject) == "" {
		return ErrInvalid
	}
	if command.ExpectedRecordRevision == 0 || command.ExpectedCollectionRevision == 0 || command.ExpectedModelRevision == 0 || command.ExpectedServerEpoch == "" || command.ExpectedSnapshotInstance == "" || command.ExpectedSnapshotGeneration == 0 {
		return ErrInvalid
	}
	if command.PreviewBucket != nil && (*command.PreviewBucket < 0 || *command.PreviewBucket > 99) {
		return ErrInvalid
	}
	return nil
}

func modelExposesSensitiveField(model catalog.CompiledModel, name string) bool {
	if !slices.Contains(model.ProjectionFields(), name) {
		return false
	}
	for _, field := range model.Fields() {
		if field.Name == name {
			return field.Sensitive
		}
	}
	return false
}

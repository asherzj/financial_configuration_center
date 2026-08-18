package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	access "github.com/asherzj/financial_configuration_center/internal/access/application"
	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
	platformmysql "github.com/asherzj/financial_configuration_center/internal/platform/mysql"
	"gorm.io/gorm"
)

type Store struct{ database *platformmysql.Database }

func New(database *platformmysql.Database) (*Store, error) {
	if database == nil {
		return nil, errors.New("new sensitive access MySQL store: database is required")
	}
	return &Store{database: database}, nil
}

func (store *Store) WithinTransaction(ctx context.Context, work func(access.Transaction) error) error {
	if work == nil {
		return errors.New("sensitive access transaction callback is required")
	}
	return store.database.WithinTransaction(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead}, func(db *gorm.DB) error {
		return work(&transaction{db: db, definitions: make(map[string]catalog.CollectionDefinition)})
	})
}

type transaction struct {
	db          *gorm.DB
	definitions map[string]catalog.CollectionDefinition
}

func (transaction *transaction) LoadCatalog(ctx context.Context, modelCode string) (access.CatalogAuthority, error) {
	type row struct {
		CollectionName        string
		CollectionDescription string
		Fields                []byte
		KeyFields             []byte
		SDKDeliveryEnabled    bool
		SchemaVersion         uint64
		CollectionStatus      string
		ModelCode             string
		ModelName             string
		ModelDefinition       []byte
		ModelEnabled          bool
		ModelRevision         uint64
	}
	var loaded row
	result := transaction.db.WithContext(ctx).Raw(`
		SELECT c.name AS collection_name, c.description AS collection_description,
			c.fields, c.key_fields, c.sdk_delivery_enabled, c.schema_version, c.status AS collection_status,
			m.code AS model_code, m.name AS model_name, m.definition AS model_definition,
			m.enabled AS model_enabled, m.config_revision AS model_revision
		FROM configuration_models m
		JOIN configuration_collections c ON c.name = m.collection_name
		WHERE m.code = ? FOR SHARE
	`, modelCode).Scan(&loaded)
	if result.Error != nil {
		return access.CatalogAuthority{}, result.Error
	}
	if result.RowsAffected != 1 || loaded.CollectionStatus != "ENABLED" || !loaded.ModelEnabled {
		return access.CatalogAuthority{}, fmt.Errorf("enabled model %q was not found", modelCode)
	}
	var fields []catalog.FieldDefinition
	var keyFields []string
	if err := json.Unmarshal(loaded.Fields, &fields); err != nil {
		return access.CatalogAuthority{}, fmt.Errorf("decode collection fields: %w", err)
	}
	if err := json.Unmarshal(loaded.KeyFields, &keyFields); err != nil {
		return access.CatalogAuthority{}, fmt.Errorf("decode collection key fields: %w", err)
	}
	definition, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: loaded.CollectionName, Description: loaded.CollectionDescription, Fields: fields, KeyFields: keyFields,
		SDKDeliveryEnabled: loaded.SDKDeliveryEnabled, SchemaVersion: int64(loaded.SchemaVersion),
	})
	if err != nil {
		return access.CatalogAuthority{}, fmt.Errorf("compile persisted collection: %w", err)
	}
	var modelSpec catalog.ModelSpec
	if err := json.Unmarshal(loaded.ModelDefinition, &modelSpec); err != nil {
		return access.CatalogAuthority{}, fmt.Errorf("decode model definition: %w", err)
	}
	modelSpec.Code, modelSpec.Name, modelSpec.Collection = loaded.ModelCode, loaded.ModelName, loaded.CollectionName
	modelSpec.ConfigRevision = catalog.ConfigRevision(loaded.ModelRevision)
	model, err := catalog.CompileModel(definition, modelSpec)
	if err != nil {
		return access.CatalogAuthority{}, fmt.Errorf("compile persisted model: %w", err)
	}
	transaction.definitions[definition.Name()] = definition
	return access.CatalogAuthority{Definition: definition, Model: model}, nil
}

func (transaction *transaction) LoadRecordAuthority(ctx context.Context, collection string, scope access.Scope, recordKey string) (access.RecordAuthority, error) {
	definition, exists := transaction.definitions[collection]
	if !exists {
		return access.RecordAuthority{}, errors.New("sensitive access catalog must be loaded first")
	}
	var revision uint64
	version := transaction.db.WithContext(ctx).Raw(`
		SELECT config_revision FROM configuration_versions
		WHERE collection_name = ? AND environment = ? FOR SHARE
	`, collection, scope.Environment).Scan(&revision)
	if version.Error != nil {
		return access.RecordAuthority{}, version.Error
	}
	if version.RowsAffected != 1 || revision == 0 {
		return access.RecordAuthority{}, fmt.Errorf("collection version %s/%s was not found", collection, scope.Environment)
	}
	authority := access.RecordAuthority{CollectionRevision: catalog.ConfigRevision(revision)}
	type recordRow struct {
		RecordKey      string
		Data           []byte
		ConfigRevision uint64
	}
	var record recordRow
	loadedRecord := transaction.db.WithContext(ctx).Raw(`
		SELECT record_key, data, config_revision FROM configuration_records
		WHERE collection_name = ? AND environment = ? AND record_key = ? FOR SHARE
	`, collection, scope.Environment, recordKey).Scan(&record)
	if loadedRecord.Error != nil {
		return access.RecordAuthority{}, loadedRecord.Error
	}
	if loadedRecord.RowsAffected == 1 {
		var data map[string]string
		if err := json.Unmarshal(record.Data, &data); err != nil {
			return access.RecordAuthority{}, fmt.Errorf("decode sensitive record: %w", err)
		}
		compiled, err := definition.NewRecord(scope.Environment, data)
		if err != nil || compiled.RecordKey != record.RecordKey {
			return access.RecordAuthority{}, errors.New("sensitive record is not canonical")
		}
		compiled.ConfigRevision = catalog.ConfigRevision(record.ConfigRevision)
		authority.BaseRecords = []catalog.ConfigurationRecord{compiled}
	}
	type overlayRow struct {
		ID, CollectionName, Region, Environment, Stage, RecordKey, Action, ReleaseOrderID string
		Content, RolloutRanges                                                            []byte
		ConfigRevision                                                                    uint64
		EffectiveFrom, EffectiveUntil, ActivatedAt, ExpiredAt                             *time.Time
		ActivatedRevision, ExpiredRevision                                                *uint64
		CreatedAt, UpdatedAt                                                              time.Time
		CreatedBy, UpdatedBy                                                              string
	}
	var rows []overlayRow
	if err := transaction.db.WithContext(ctx).Raw(`
		SELECT id, collection_name, region, environment, stage, record_key, action,
			content, rollout_ranges, config_revision, release_order_id,
			effective_from, effective_until, activated_revision, activated_at,
			expired_revision, expired_at, created_at, created_by, updated_at, updated_by
		FROM configuration_overlays
		WHERE collection_name = ? AND environment = ? AND region = ?
			AND stage IN ('', ?) AND record_key = ?
		ORDER BY stage FOR SHARE
	`, collection, scope.Environment, scope.Region, scope.Stage, recordKey).Scan(&rows).Error; err != nil {
		return access.RecordAuthority{}, err
	}
	authority.Rules = make([]overlay.Rule, len(rows))
	for index, row := range rows {
		var content map[string]string
		if len(row.Content) > 0 {
			if err := json.Unmarshal(row.Content, &content); err != nil {
				return access.RecordAuthority{}, fmt.Errorf("decode sensitive overlay content: %w", err)
			}
		}
		var ranges []overlay.BucketRange
		if err := json.Unmarshal(row.RolloutRanges, &ranges); err != nil {
			return access.RecordAuthority{}, fmt.Errorf("decode sensitive overlay ranges: %w", err)
		}
		rule := overlay.Rule{
			ID: row.ID, Collection: row.CollectionName, Scope: overlay.Scope{Region: row.Region, Environment: row.Environment, Stage: row.Stage},
			RecordKey: row.RecordKey, Action: overlay.Action(row.Action), Content: content, RolloutRanges: ranges,
			ConfigRevision: catalog.ConfigRevision(row.ConfigRevision), ReleaseOrderID: row.ReleaseOrderID,
			EffectiveFrom: row.EffectiveFrom, EffectiveUntil: row.EffectiveUntil, ActivatedAt: row.ActivatedAt, ExpiredAt: row.ExpiredAt,
			CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy, UpdatedAt: row.UpdatedAt, UpdatedBy: row.UpdatedBy,
		}
		if row.ActivatedRevision != nil {
			value := catalog.ConfigRevision(*row.ActivatedRevision)
			rule.ActivatedRevision = &value
		}
		if row.ExpiredRevision != nil {
			value := catalog.ConfigRevision(*row.ExpiredRevision)
			rule.ExpiredRevision = &value
		}
		authority.Rules[index] = rule
	}
	return authority, nil
}

func (transaction *transaction) InsertRevealAudit(ctx context.Context, entry access.AuditEntry) error {
	metadata, err := json.Marshal(entry.Metadata)
	if err != nil {
		return err
	}
	displayName := entry.Principal.DisplayName
	if displayName == "" {
		displayName = entry.Principal.Subject
	}
	return transaction.db.WithContext(ctx).Exec(`
		INSERT INTO audit_records (
			occurred_at, principal_subject, principal_display_name, action,
			resource_type, resource_id, region, environment, stage, result,
			before_data, after_data, metadata, request_id, trace_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, ?, ?, ?)
	`, entry.OccurredAt, entry.Principal.Subject, displayName, entry.Action,
		entry.ResourceType, entry.ResourceID, entry.Scope.Region, entry.Scope.Environment, entry.Scope.Stage,
		entry.Result, metadata, entry.RequestID, entry.TraceID).Error
}

package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/catalog/application"
	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	platformmysql "github.com/asherzj/financial_configuration_center/internal/platform/mysql"
	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

type Store struct{ database *platformmysql.Database }

func New(database *platformmysql.Database) (*Store, error) {
	if database == nil {
		return nil, errors.New("new catalog MySQL store: database is required")
	}
	return &Store{database: database}, nil
}

func (store *Store) CreateCollection(ctx context.Context, mutation application.CollectionMutation) (application.CollectionView, error) {
	var view application.CollectionView
	err := store.write(ctx, func(db *gorm.DB) error {
		revision, err := allocateRevision(ctx, db)
		if err != nil {
			return err
		}
		fields, _ := json.Marshal(mutation.Definition.Fields())
		keys, _ := json.Marshal(mutation.Definition.KeyFields())
		if err := db.WithContext(ctx).Exec(`
			INSERT INTO configuration_collections (
				name, description, fields, key_fields, sdk_delivery_enabled, schema_version,
				status, config_revision, created_at, created_by, updated_at, updated_by
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, mutation.Definition.Name(), mutation.Definition.Description(), fields, keys, mutation.Definition.SDKDeliveryEnabled(), mutation.Definition.SchemaVersion(), mutation.Status, revision, mutation.At, mutation.Actor.Subject, mutation.At, mutation.Actor.Subject).Error; err != nil {
			return err
		}
		if err := appendMetadataFacts(ctx, db, "ADD", "COLLECTION", mutation.Definition.Name(), mutation.Definition.Name(), revision, mutation.Actor, mutation.At); err != nil {
			return err
		}
		view = collectionView(mutation.Definition, mutation.Status, revision, application.AuditStamp{CreatedAt: mutation.At, CreatedBy: mutation.Actor.Subject, UpdatedAt: mutation.At, UpdatedBy: mutation.Actor.Subject})
		return nil
	})
	return view, classify(err)
}

func (store *Store) UpdateCollection(ctx context.Context, mutation application.CollectionMutation) (application.CollectionView, error) {
	var view application.CollectionView
	err := store.write(ctx, func(db *gorm.DB) error {
		row, found, err := loadCollectionRow(ctx, db, mutation.Definition.Name(), true)
		if err != nil {
			return err
		}
		if !found {
			return application.ErrNotFound
		}
		if catalog.ConfigRevision(row.ConfigRevision) != mutation.ExpectedRevision {
			return fmt.Errorf("%w: current revision is %d", application.ErrAborted, row.ConfigRevision)
		}
		current, err := compileCollectionRow(row)
		if err != nil {
			return err
		}
		var recordCount int64
		if err := db.WithContext(ctx).Raw(`SELECT COUNT(*) FROM configuration_records WHERE collection_name = ?`, row.Name).Scan(&recordCount).Error; err != nil {
			return err
		}
		if recordCount > 0 && destructiveCollectionChange(current, mutation.Definition) {
			return fmt.Errorf("%w: collection with records cannot receive a destructive schema change", application.ErrFailedPrecondition)
		}
		revision, err := allocateRevision(ctx, db)
		if err != nil {
			return err
		}
		fields, _ := json.Marshal(mutation.Definition.Fields())
		keys, _ := json.Marshal(mutation.Definition.KeyFields())
		result := db.WithContext(ctx).Exec(`
			UPDATE configuration_collections
			SET description = ?, fields = ?, key_fields = ?, sdk_delivery_enabled = ?, schema_version = ?,
				status = ?, config_revision = ?, updated_at = ?, updated_by = ?
			WHERE name = ? AND config_revision = ?
		`, mutation.Definition.Description(), fields, keys, mutation.Definition.SDKDeliveryEnabled(), mutation.Definition.SchemaVersion(), mutation.Status, revision, mutation.At, mutation.Actor.Subject, mutation.Definition.Name(), mutation.ExpectedRevision)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return application.ErrAborted
		}
		if err := appendMetadataFacts(ctx, db, "MODIFY", "COLLECTION", mutation.Definition.Name(), mutation.Definition.Name(), revision, mutation.Actor, mutation.At); err != nil {
			return err
		}
		view = collectionView(mutation.Definition, mutation.Status, revision, application.AuditStamp{CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy, UpdatedAt: mutation.At, UpdatedBy: mutation.Actor.Subject})
		return nil
	})
	return view, classify(err)
}

func (store *Store) GetCollection(ctx context.Context, name string) (application.CollectionView, error) {
	var view application.CollectionView
	err := store.read(ctx, func(db *gorm.DB) error {
		row, found, err := loadCollectionRow(ctx, db, name, false)
		if err != nil {
			return err
		}
		if !found {
			return application.ErrNotFound
		}
		definition, err := compileCollectionRow(row)
		if err != nil {
			return err
		}
		view = collectionView(definition, application.Status(row.Status), catalog.ConfigRevision(row.ConfigRevision), row.audit())
		return nil
	})
	return view, classify(err)
}

func (store *Store) ListCollections(ctx context.Context, query application.PageQuery) (application.CollectionPage, error) {
	page := application.CollectionPage{PageNumber: query.PageNumber, PageSize: query.PageSize}
	err := store.read(ctx, func(db *gorm.DB) error {
		if err := db.WithContext(ctx).Raw(`SELECT COUNT(*) FROM configuration_collections`).Scan(&page.TotalNumber).Error; err != nil {
			return err
		}
		var rows []collectionRow
		if err := db.WithContext(ctx).Raw(`
			SELECT name, description, fields, key_fields, sdk_delivery_enabled, schema_version, status,
				config_revision, created_at, created_by, updated_at, updated_by
			FROM configuration_collections ORDER BY name LIMIT ? OFFSET ?
		`, query.PageSize, (query.PageNumber-1)*query.PageSize).Scan(&rows).Error; err != nil {
			return err
		}
		page.Collections = make([]application.CollectionView, len(rows))
		for index, row := range rows {
			definition, err := compileCollectionRow(row)
			if err != nil {
				return err
			}
			page.Collections[index] = collectionView(definition, application.Status(row.Status), catalog.ConfigRevision(row.ConfigRevision), row.audit())
		}
		return nil
	})
	page.TotalPages = totalPages(page.TotalNumber, page.PageSize)
	return page, classify(err)
}

func (store *Store) CreateSubscription(ctx context.Context, mutation application.SubscriptionMutation) (application.SubscriptionView, error) {
	var view application.SubscriptionView
	err := store.write(ctx, func(db *gorm.DB) error {
		if err := validateSubscriptionCollection(ctx, db, mutation.SubscriptionInput); err != nil {
			return err
		}
		revision, err := allocateRevision(ctx, db)
		if err != nil {
			return err
		}
		fields, _ := json.Marshal(mutation.IndexFields)
		var id string
		if err := db.WithContext(ctx).Raw(`SELECT UUID()`).Scan(&id).Error; err != nil {
			return err
		}
		if err := db.WithContext(ctx).Exec(`
			INSERT INTO configuration_subscriptions (
				id, consumer_id, collection_name, index_name, index_fields, cardinality, enabled,
				config_revision, created_at, created_by, updated_at, updated_by
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, id, mutation.ConsumerID, mutation.Collection, mutation.IndexName, fields, mutation.Cardinality, mutation.Enabled, revision, mutation.At, mutation.Actor.Subject, mutation.At, mutation.Actor.Subject).Error; err != nil {
			return err
		}
		if err := appendMetadataFacts(ctx, db, "ADD", "SUBSCRIPTION", id, mutation.Collection, revision, mutation.Actor, mutation.At); err != nil {
			return err
		}
		view = subscriptionView(mutation.SubscriptionInput, id, revision, application.AuditStamp{CreatedAt: mutation.At, CreatedBy: mutation.Actor.Subject, UpdatedAt: mutation.At, UpdatedBy: mutation.Actor.Subject})
		return nil
	})
	return view, classify(err)
}

func (store *Store) UpdateSubscription(ctx context.Context, mutation application.SubscriptionMutation) (application.SubscriptionView, error) {
	var view application.SubscriptionView
	err := store.write(ctx, func(db *gorm.DB) error {
		row, found, err := loadSubscriptionRow(ctx, db, mutation.ID, true)
		if err != nil {
			return err
		}
		if !found {
			return application.ErrNotFound
		}
		if catalog.ConfigRevision(row.ConfigRevision) != mutation.ExpectedRevision {
			return fmt.Errorf("%w: current revision is %d", application.ErrAborted, row.ConfigRevision)
		}
		if err := validateSubscriptionCollection(ctx, db, mutation.SubscriptionInput); err != nil {
			return err
		}
		revision, err := allocateRevision(ctx, db)
		if err != nil {
			return err
		}
		fields, _ := json.Marshal(mutation.IndexFields)
		result := db.WithContext(ctx).Exec(`
			UPDATE configuration_subscriptions
			SET consumer_id = ?, collection_name = ?, index_name = ?, index_fields = ?, cardinality = ?,
				enabled = ?, config_revision = ?, updated_at = ?, updated_by = ?
			WHERE id = ? AND config_revision = ?
		`, mutation.ConsumerID, mutation.Collection, mutation.IndexName, fields, mutation.Cardinality, mutation.Enabled, revision, mutation.At, mutation.Actor.Subject, mutation.ID, mutation.ExpectedRevision)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return application.ErrAborted
		}
		if err := appendMetadataFacts(ctx, db, "MODIFY", "SUBSCRIPTION", mutation.ID, mutation.Collection, revision, mutation.Actor, mutation.At); err != nil {
			return err
		}
		view = subscriptionView(mutation.SubscriptionInput, mutation.ID, revision, application.AuditStamp{CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy, UpdatedAt: mutation.At, UpdatedBy: mutation.Actor.Subject})
		return nil
	})
	return view, classify(err)
}

func (store *Store) ListSubscriptions(ctx context.Context, query application.SubscriptionQuery) (application.SubscriptionPage, error) {
	page := application.SubscriptionPage{PageNumber: query.PageNumber, PageSize: query.PageSize}
	err := store.read(ctx, func(db *gorm.DB) error {
		args := []any{query.ConsumerID, query.ConsumerID, query.Collection, query.Collection}
		if err := db.WithContext(ctx).Raw(`SELECT COUNT(*) FROM configuration_subscriptions WHERE (? = '' OR consumer_id = ?) AND (? = '' OR collection_name = ?)`, args...).Scan(&page.TotalNumber).Error; err != nil {
			return err
		}
		var rows []subscriptionRow
		args = append(args, query.PageSize, (query.PageNumber-1)*query.PageSize)
		if err := db.WithContext(ctx).Raw(`
			SELECT id, consumer_id, collection_name, index_name, index_fields, cardinality, enabled,
				config_revision, created_at, created_by, updated_at, updated_by
			FROM configuration_subscriptions
			WHERE (? = '' OR consumer_id = ?) AND (? = '' OR collection_name = ?)
			ORDER BY consumer_id, collection_name, index_name LIMIT ? OFFSET ?
		`, args...).Scan(&rows).Error; err != nil {
			return err
		}
		page.Subscriptions = make([]application.SubscriptionView, len(rows))
		for index, row := range rows {
			fields := []string{}
			if err := json.Unmarshal(row.IndexFields, &fields); err != nil {
				return fmt.Errorf("decode subscription %s index fields: %w", row.ID, err)
			}
			page.Subscriptions[index] = subscriptionView(application.SubscriptionInput{ConsumerID: row.ConsumerID, Collection: row.CollectionName, IndexName: row.IndexName, IndexFields: fields, Cardinality: application.Cardinality(row.Cardinality), Enabled: row.Enabled}, row.ID, catalog.ConfigRevision(row.ConfigRevision), row.audit())
		}
		return nil
	})
	page.TotalPages = totalPages(page.TotalNumber, page.PageSize)
	return page, classify(err)
}

type collectionRow struct {
	Name               string
	Description        string
	Fields             []byte
	KeyFields          []byte
	SDKDeliveryEnabled bool
	SchemaVersion      uint64
	Status             string
	ConfigRevision     uint64
	CreatedAt          time.Time
	CreatedBy          string
	UpdatedAt          time.Time
	UpdatedBy          string
}

func (row collectionRow) audit() application.AuditStamp {
	return application.AuditStamp{CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy, UpdatedAt: row.UpdatedAt, UpdatedBy: row.UpdatedBy}
}

type subscriptionRow struct {
	ID             string
	ConsumerID     string
	CollectionName string
	IndexName      string
	IndexFields    []byte
	Cardinality    string
	Enabled        bool
	ConfigRevision uint64
	CreatedAt      time.Time
	CreatedBy      string
	UpdatedAt      time.Time
	UpdatedBy      string
}

func (row subscriptionRow) audit() application.AuditStamp {
	return application.AuditStamp{CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy, UpdatedAt: row.UpdatedAt, UpdatedBy: row.UpdatedBy}
}

func (store *Store) write(ctx context.Context, work func(*gorm.DB) error) error {
	return store.database.WithinTransaction(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, work)
}

func (store *Store) read(ctx context.Context, work func(*gorm.DB) error) error {
	return store.database.WithinTransaction(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, work)
}

func allocateRevision(ctx context.Context, db *gorm.DB) (catalog.ConfigRevision, error) {
	var current uint64
	result := db.WithContext(ctx).Raw(`SELECT current_revision FROM configuration_revision_counters WHERE counter_name = 'global' FOR UPDATE`).Scan(&current)
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected != 1 {
		return 0, errors.New("global config revision counter was not found")
	}
	next := current + 1
	result = db.WithContext(ctx).Exec(`UPDATE configuration_revision_counters SET current_revision = ?, updated_at = UTC_TIMESTAMP(6) WHERE counter_name = 'global' AND current_revision = ?`, next, current)
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected != 1 {
		return 0, application.ErrAborted
	}
	return catalog.ConfigRevision(next), nil
}

func appendMetadataFacts(ctx context.Context, db *gorm.DB, action, resourceType, resourceID, collection string, revision catalog.ConfigRevision, actor application.Principal, at time.Time) error {
	if err := db.WithContext(ctx).Exec(`
		INSERT INTO configuration_change_log (
			collection_name, kind, region, environment, stage, record_key, action,
			before_data, after_data, config_revision, release_order_id, created_at
		) VALUES (?, 'METADATA', '', '', '', '', ?, NULL, NULL, ?, NULL, ?)
	`, collection, action, revision, at).Error; err != nil {
		return err
	}
	if err := db.WithContext(ctx).Exec(`
		INSERT INTO audit_records (
			occurred_at, principal_subject, principal_display_name, action, resource_type, resource_id,
			region, environment, stage, result, before_data, after_data, metadata, request_id, trace_id
		) VALUES (?, ?, ?, ?, ?, ?, '', '', '', 'SUCCEEDED', NULL, NULL,
			JSON_OBJECT('configRevision', ?), UUID(), '')
	`, at, actor.Subject, actor.DisplayName, action, resourceType, resourceID, revision).Error; err != nil {
		return err
	}
	var environments []string
	if resourceType == "COLLECTION" && action == "ADD" {
		if err := db.WithContext(ctx).Raw(`SELECT DISTINCT environment FROM configuration_versions ORDER BY environment`).Scan(&environments).Error; err != nil {
			return err
		}
		for _, environment := range environments {
			if err := db.WithContext(ctx).Exec(`
				INSERT INTO configuration_versions (
					collection_name, environment, config_revision, base_digest, overlay_digest, release_order_id, updated_at
				) VALUES (?, ?, ?, SHA2('[]', 256), SHA2('[]', 256), NULL, ?)
			`, collection, environment, revision, at).Error; err != nil {
				return err
			}
		}
	} else {
		if err := db.WithContext(ctx).Raw(`SELECT environment FROM configuration_versions WHERE collection_name = ? ORDER BY environment`, collection).Scan(&environments).Error; err != nil {
			return err
		}
		if err := db.WithContext(ctx).Exec(`UPDATE configuration_versions SET config_revision = ?, updated_at = ? WHERE collection_name = ?`, revision, at, collection).Error; err != nil {
			return err
		}
	}
	for _, environment := range environments {
		payload, err := json.Marshal(map[string]any{"schemaVersion": 1, "collection": collection, "environment": environment, "configRevision": revision})
		if err != nil {
			return err
		}
		if err := db.WithContext(ctx).Exec(`
			INSERT INTO outbox_events (
				id, aggregate_type, aggregate_id, event_type, payload_version, payload, idempotency_key,
				status, lease_revision, attempts, next_attempt_at, created_at, updated_at
			) VALUES (UUID(), ?, ?, 'CONFIGURATION_CHANGED', 1, ?, SHA2(CONCAT('catalog:', ?, ':', ?, ':', ?, ':', ?), 256),
				'PENDING', 1, 0, ?, ?, ?)
		`, resourceType, resourceID, payload, action, resourceID, environment, revision, at, at, at).Error; err != nil {
			return err
		}
	}
	return nil
}

func loadCollectionRow(ctx context.Context, db *gorm.DB, name string, lock bool) (collectionRow, bool, error) {
	query := `
		SELECT name, description, fields, key_fields, sdk_delivery_enabled, schema_version, status,
			config_revision, created_at, created_by, updated_at, updated_by
		FROM configuration_collections WHERE name = ?`
	if lock {
		query += ` FOR UPDATE`
	}
	var row collectionRow
	result := db.WithContext(ctx).Raw(query, name).Scan(&row)
	return row, result.RowsAffected == 1, result.Error
}

func compileCollectionRow(row collectionRow) (catalog.CollectionDefinition, error) {
	var fields []catalog.FieldDefinition
	var keys []string
	if err := json.Unmarshal(row.Fields, &fields); err != nil {
		return catalog.CollectionDefinition{}, fmt.Errorf("decode collection %s fields: %w", row.Name, err)
	}
	if err := json.Unmarshal(row.KeyFields, &keys); err != nil {
		return catalog.CollectionDefinition{}, fmt.Errorf("decode collection %s keys: %w", row.Name, err)
	}
	return catalog.CompileCollection(catalog.CollectionSpec{Name: row.Name, Description: row.Description, Fields: fields, KeyFields: keys, SDKDeliveryEnabled: row.SDKDeliveryEnabled, SchemaVersion: int64(row.SchemaVersion)})
}

func collectionView(definition catalog.CollectionDefinition, status application.Status, revision catalog.ConfigRevision, stamp application.AuditStamp) application.CollectionView {
	return application.CollectionView{Name: definition.Name(), Description: definition.Description(), Fields: definition.Fields(), KeyFields: definition.KeyFields(), SDKDeliveryEnabled: definition.SDKDeliveryEnabled(), SchemaVersion: definition.SchemaVersion(), Status: status, ConfigRevision: revision, Audit: stamp}
}

func subscriptionView(input application.SubscriptionInput, id string, revision catalog.ConfigRevision, stamp application.AuditStamp) application.SubscriptionView {
	input.ID = id
	input.IndexFields = slices.Clone(input.IndexFields)
	return application.SubscriptionView{SubscriptionInput: input, ConfigRevision: revision, Audit: stamp}
}

func loadSubscriptionRow(ctx context.Context, db *gorm.DB, id string, lock bool) (subscriptionRow, bool, error) {
	query := `
		SELECT id, consumer_id, collection_name, index_name, index_fields, cardinality, enabled,
			config_revision, created_at, created_by, updated_at, updated_by
		FROM configuration_subscriptions WHERE id = ?`
	if lock {
		query += ` FOR UPDATE`
	}
	var row subscriptionRow
	result := db.WithContext(ctx).Raw(query, id).Scan(&row)
	return row, result.RowsAffected == 1, result.Error
}

func validateSubscriptionCollection(ctx context.Context, db *gorm.DB, input application.SubscriptionInput) error {
	row, found, err := loadCollectionRow(ctx, db, input.Collection, true)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: subscription collection does not exist", application.ErrFailedPrecondition)
	}
	definition, err := compileCollectionRow(row)
	if err != nil {
		return err
	}
	if input.Enabled && (application.Status(row.Status) != application.StatusEnabled || !definition.SDKDeliveryEnabled()) {
		return fmt.Errorf("%w: enabled subscription requires an enabled SDK-deliverable collection", application.ErrFailedPrecondition)
	}
	for _, name := range input.IndexFields {
		field, exists := definition.Field(name)
		if !exists || field.Sensitive {
			return fmt.Errorf("%w: index field %s is missing or sensitive", application.ErrFailedPrecondition, name)
		}
	}
	return nil
}

func destructiveCollectionChange(current, next catalog.CollectionDefinition) bool {
	if !slices.Equal(current.KeyFields(), next.KeyFields()) {
		return true
	}
	for _, old := range current.Fields() {
		updated, exists := next.Field(old.Name)
		if !exists || old.Type != updated.Type || old.Required != updated.Required || old.Sensitive != updated.Sensitive || !reflect.DeepEqual(old.ValidationRules, updated.ValidationRules) {
			return true
		}
	}
	for _, added := range next.Fields() {
		if _, exists := current.Field(added.Name); !exists && added.Required && added.DefaultValue == nil {
			return true
		}
	}
	return false
}

func totalPages(total int64, size int) int {
	if total == 0 {
		return 0
	}
	return int((total + int64(size) - 1) / int64(size))
}

func classify(err error) error {
	if err == nil {
		return nil
	}
	var mysqlError *drivermysql.MySQLError
	if !errors.As(err, &mysqlError) {
		return err
	}
	switch mysqlError.Number {
	case 1062:
		return fmt.Errorf("%w: catalog identity already exists", application.ErrAlreadyExists)
	case 1451, 1452:
		return fmt.Errorf("%w: catalog reference is invalid", application.ErrFailedPrecondition)
	case 1205, 1213:
		return fmt.Errorf("%w: catalog transaction conflicted", application.ErrAborted)
	default:
		if strings.Contains(mysqlError.Message, "check constraint") {
			return fmt.Errorf("%w: catalog persistence constraint failed", application.ErrInvalid)
		}
		return err
	}
}

var _ application.Repository = (*Store)(nil)

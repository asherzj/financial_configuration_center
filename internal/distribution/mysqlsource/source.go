package mysqlsource

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
	platformmysql "github.com/asherzj/financial_configuration_center/internal/platform/mysql"
	"gorm.io/gorm"
)

type Source struct{ database *platformmysql.Database }

func New(database *platformmysql.Database) (*Source, error) {
	if database == nil {
		return nil, errors.New("new distribution MySQL source: database is required")
	}
	return &Source{database: database}, nil
}

func (source *Source) LoadEnvironment(ctx context.Context, environment string) ([]snapshot.CollectionInput, error) {
	loaded, err := source.LoadEnvironmentPartial(ctx, environment)
	if err != nil {
		return nil, err
	}
	if len(loaded.Failures) > 0 {
		return nil, fmt.Errorf("load distribution environment %q: %s", environment, formatFailures(loaded.Failures))
	}
	return loaded.Inputs, nil
}

func (source *Source) LoadEnvironmentPartial(ctx context.Context, environment string) (snapshot.EnvironmentLoad, error) {
	loaded := snapshot.EnvironmentLoad{Failures: make(map[string]error)}
	err := source.database.WithinTransaction(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(db *gorm.DB) error {
		type collectionRow struct {
			Name               string
			Description        string
			Fields             []byte
			KeyFields          []byte
			SDKDeliveryEnabled bool
			SchemaVersion      uint64
			ConfigRevision     uint64
			ChangeCursor       uint64
		}
		var collections []collectionRow
		if err := db.WithContext(ctx).Raw(`
			SELECT c.name, c.description, c.fields, c.key_fields, c.sdk_delivery_enabled,
				c.schema_version, v.config_revision,
				COALESCE((
					SELECT MAX(change_log.id)
					FROM configuration_change_log change_log
					WHERE change_log.collection_name = c.name
						AND (
							change_log.environment = v.environment
							OR (change_log.kind = 'METADATA' AND change_log.environment = '')
						)
				), 0) AS change_cursor
			FROM configuration_collections c
			JOIN configuration_versions v ON v.collection_name = c.name AND v.environment = ?
			WHERE c.status = 'ENABLED'
			ORDER BY c.name
		`, environment).Scan(&collections).Error; err != nil {
			return err
		}
		loaded.Inputs = make([]snapshot.CollectionInput, 0, len(collections))
		for _, row := range collections {
			definition, err := compileCollection(row)
			if err != nil {
				loaded.Failures[row.Name] = err
				continue
			}
			models, err := loadModels(ctx, db, definition)
			if err != nil {
				loaded.Failures[row.Name] = err
				continue
			}
			records, err := loadRecords(ctx, db, definition.Name(), environment)
			if err != nil {
				loaded.Failures[row.Name] = err
				continue
			}
			overlayRules, err := loadOverlayRules(ctx, db, definition.Name(), environment)
			if err != nil {
				loaded.Failures[row.Name] = err
				continue
			}
			loaded.Inputs = append(loaded.Inputs, snapshot.CollectionInput{
				Definition: definition, Models: models, Version: catalog.ConfigRevision(row.ConfigRevision),
				Cursor: row.ChangeCursor, Records: records, OverlayRules: overlayRules,
			})
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return snapshot.EnvironmentLoad{}, fmt.Errorf("load distribution environment %q: %w", environment, err)
	}
	return loaded, nil
}

func formatFailures(failures map[string]error) string {
	names := make([]string, 0, len(failures))
	for name := range failures {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for index, name := range names {
		parts[index] = fmt.Sprintf("%s: %v", name, failures[name])
	}
	return strings.Join(parts, "; ")
}

func (source *Source) AuthorizedCollections(ctx context.Context, consumerID string) ([]string, error) {
	var collections []string
	err := source.database.WithinTransaction(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: true}, func(db *gorm.DB) error {
		return db.WithContext(ctx).Raw(`
			SELECT DISTINCT s.collection_name
			FROM configuration_subscriptions s
			JOIN configuration_collections c ON c.name = s.collection_name
			WHERE s.consumer_id = ? AND s.enabled = TRUE
				AND c.status = 'ENABLED' AND c.sdk_delivery_enabled = TRUE
			ORDER BY s.collection_name
		`, consumerID).Scan(&collections).Error
	})
	if err != nil {
		return nil, fmt.Errorf("load authorized collections: %w", err)
	}
	return collections, nil
}

func (source *Source) LoadVersions(ctx context.Context, environment string) (map[string]catalog.ConfigRevision, error) {
	type row struct {
		CollectionName string
		ConfigRevision uint64
	}
	var rows []row
	err := source.database.WithinTransaction(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: true}, func(db *gorm.DB) error {
		return db.WithContext(ctx).Raw(`
			SELECT v.collection_name, v.config_revision
			FROM configuration_versions v
			JOIN configuration_collections c ON c.name = v.collection_name
			WHERE v.environment = ? AND c.status = 'ENABLED'
			ORDER BY v.collection_name
		`, environment).Scan(&rows).Error
	})
	if err != nil {
		return nil, fmt.Errorf("load distribution versions for %q: %w", environment, err)
	}
	versions := make(map[string]catalog.ConfigRevision, len(rows))
	for _, row := range rows {
		versions[row.CollectionName] = catalog.ConfigRevision(row.ConfigRevision)
	}
	return versions, nil
}

func compileCollection(row struct {
	Name               string
	Description        string
	Fields             []byte
	KeyFields          []byte
	SDKDeliveryEnabled bool
	SchemaVersion      uint64
	ConfigRevision     uint64
	ChangeCursor       uint64
}) (catalog.CollectionDefinition, error) {
	var fields []catalog.FieldDefinition
	var keyFields []string
	if err := json.Unmarshal(row.Fields, &fields); err != nil {
		return catalog.CollectionDefinition{}, fmt.Errorf("decode collection %q fields: %w", row.Name, err)
	}
	if err := json.Unmarshal(row.KeyFields, &keyFields); err != nil {
		return catalog.CollectionDefinition{}, fmt.Errorf("decode collection %q key fields: %w", row.Name, err)
	}
	definition, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: row.Name, Description: row.Description, Fields: fields, KeyFields: keyFields,
		SDKDeliveryEnabled: row.SDKDeliveryEnabled, SchemaVersion: int64(row.SchemaVersion),
	})
	if err != nil {
		return catalog.CollectionDefinition{}, fmt.Errorf("compile collection %q: %w", row.Name, err)
	}
	return definition, nil
}

func loadModels(ctx context.Context, db *gorm.DB, definition catalog.CollectionDefinition) ([]catalog.CompiledModel, error) {
	type modelRow struct {
		Code, Name string
		Definition []byte
		Revision   uint64
	}
	var rows []modelRow
	if err := db.WithContext(ctx).Raw(`
		SELECT code, name, definition, config_revision AS revision
		FROM configuration_models
		WHERE collection_name = ? AND enabled = TRUE
		ORDER BY code
	`, definition.Name()).Scan(&rows).Error; err != nil {
		return nil, err
	}
	models := make([]catalog.CompiledModel, len(rows))
	for index, row := range rows {
		var spec catalog.ModelSpec
		if err := json.Unmarshal(row.Definition, &spec); err != nil {
			return nil, fmt.Errorf("decode model %q: %w", row.Code, err)
		}
		if err := resolveReleaseTypeAvailability(ctx, db, row.Code, spec.ReleaseTypes); err != nil {
			return nil, err
		}
		spec.Code, spec.Name, spec.Collection = row.Code, row.Name, definition.Name()
		spec.ConfigRevision = catalog.ConfigRevision(row.Revision)
		model, err := catalog.CompileModel(definition, spec)
		if err != nil {
			return nil, fmt.Errorf("compile model %q: %w", row.Code, err)
		}
		models[index] = model
	}
	return models, nil
}

func resolveReleaseTypeAvailability(ctx context.Context, db *gorm.DB, modelCode string, definitions []catalog.ReleaseTypeDefinition) error {
	type templateRow struct {
		ReleaseTypeCode string
		Code            string
	}
	var rows []templateRow
	if err := db.WithContext(ctx).Raw(`
		SELECT release_type_code, code
		FROM release_templates
		WHERE model_code = ? AND active_slot = 'A'
	`, modelCode).Scan(&rows).Error; err != nil {
		return fmt.Errorf("load active release templates for model %q: %w", modelCode, err)
	}
	active := make(map[string]string, len(rows))
	for _, row := range rows {
		active[row.ReleaseTypeCode] = row.Code
	}
	for index := range definitions {
		definition := &definitions[index]
		definition.Available = false
		switch {
		case !definition.Enabled:
			definition.UnavailableReasonCode = "RELEASE_TYPE_DISABLED"
		case active[definition.Code] == "":
			definition.UnavailableReasonCode = "ACTIVE_TEMPLATE_NOT_FOUND"
		case active[definition.Code] != definition.TemplateCode:
			definition.UnavailableReasonCode = "ACTIVE_TEMPLATE_MISMATCH"
		default:
			definition.Available = true
			definition.UnavailableReasonCode = ""
		}
	}
	return nil
}

func loadRecords(ctx context.Context, db *gorm.DB, collection, environment string) ([]catalog.ConfigurationRecord, error) {
	type row struct {
		RecordKey      string
		Data           []byte
		ConfigRevision uint64
	}
	var rows []row
	if err := db.WithContext(ctx).Raw(`
		SELECT record_key, data, config_revision
		FROM configuration_records
		WHERE collection_name = ? AND environment = ?
		ORDER BY record_key
	`, collection, environment).Scan(&rows).Error; err != nil {
		return nil, err
	}
	records := make([]catalog.ConfigurationRecord, len(rows))
	for index, row := range rows {
		var data map[string]string
		if err := json.Unmarshal(row.Data, &data); err != nil {
			return nil, fmt.Errorf("decode record %q: %w", row.RecordKey, err)
		}
		records[index] = catalog.ConfigurationRecord{
			Collection: collection, Environment: environment, RecordKey: row.RecordKey,
			Data: data, ConfigRevision: catalog.ConfigRevision(row.ConfigRevision),
		}
	}
	return records, nil
}

func loadOverlayRules(ctx context.Context, db *gorm.DB, collection, environment string) ([]overlay.Rule, error) {
	type row struct {
		ID, Region, Stage, RecordKey, Action, ReleaseOrderID string
		Content, RolloutRanges                               []byte
		ConfigRevision                                       uint64
		EffectiveFrom, EffectiveUntil                        *time.Time
		ActivatedRevision, ExpiredRevision                   *uint64
		ActivatedAt, ExpiredAt                               *time.Time
		CreatedAt, UpdatedAt                                 time.Time
		CreatedBy, UpdatedBy                                 string
	}
	var rows []row
	if err := db.WithContext(ctx).Raw(`
		SELECT id, region, stage, record_key, action, content, rollout_ranges,
			config_revision, release_order_id, effective_from, effective_until,
			activated_revision, activated_at, expired_revision, expired_at,
			created_at, created_by, updated_at, updated_by
		FROM configuration_overlays
		WHERE collection_name = ? AND environment = ?
		ORDER BY region, stage, record_key
	`, collection, environment).Scan(&rows).Error; err != nil {
		return nil, err
	}
	rules := make([]overlay.Rule, len(rows))
	for index, loaded := range rows {
		var content map[string]string
		if len(loaded.Content) > 0 {
			if err := json.Unmarshal(loaded.Content, &content); err != nil {
				return nil, fmt.Errorf("decode overlay %q content: %w", loaded.ID, err)
			}
		}
		var ranges []overlay.BucketRange
		if err := json.Unmarshal(loaded.RolloutRanges, &ranges); err != nil {
			return nil, fmt.Errorf("decode overlay %q ranges: %w", loaded.ID, err)
		}
		rule := overlay.Rule{
			ID: loaded.ID, Collection: collection,
			Scope:     overlay.Scope{Region: loaded.Region, Environment: environment, Stage: loaded.Stage},
			RecordKey: loaded.RecordKey, Action: overlay.Action(loaded.Action), Content: content,
			RolloutRanges: ranges, ConfigRevision: catalog.ConfigRevision(loaded.ConfigRevision),
			ReleaseOrderID: loaded.ReleaseOrderID, EffectiveFrom: loaded.EffectiveFrom, EffectiveUntil: loaded.EffectiveUntil,
			ActivatedAt: loaded.ActivatedAt, ExpiredAt: loaded.ExpiredAt,
			CreatedAt: loaded.CreatedAt, CreatedBy: loaded.CreatedBy, UpdatedAt: loaded.UpdatedAt, UpdatedBy: loaded.UpdatedBy,
		}
		if loaded.ActivatedRevision != nil {
			value := catalog.ConfigRevision(*loaded.ActivatedRevision)
			rule.ActivatedRevision = &value
		}
		if loaded.ExpiredRevision != nil {
			value := catalog.ConfigRevision(*loaded.ExpiredRevision)
			rule.ExpiredRevision = &value
		}
		rules[index] = rule
	}
	return rules, nil
}

var _ snapshot.Source = (*Source)(nil)
var _ snapshot.VersionSource = (*Source)(nil)

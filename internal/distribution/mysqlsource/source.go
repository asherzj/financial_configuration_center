package mysqlsource

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
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
	var inputs []snapshot.CollectionInput
	err := source.database.WithinTransaction(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(db *gorm.DB) error {
		type collectionRow struct {
			Name               string
			Description        string
			Fields             []byte
			KeyFields          []byte
			SDKDeliveryEnabled bool
			SchemaVersion      uint64
			ConfigRevision     uint64
		}
		var collections []collectionRow
		if err := db.WithContext(ctx).Raw(`
			SELECT c.name, c.description, c.fields, c.key_fields, c.sdk_delivery_enabled,
				c.schema_version, v.config_revision
			FROM configuration_collections c
			JOIN configuration_versions v ON v.collection_name = c.name AND v.environment = ?
			WHERE c.status = 'ENABLED'
			ORDER BY c.name
		`, environment).Scan(&collections).Error; err != nil {
			return err
		}
		inputs = make([]snapshot.CollectionInput, len(collections))
		for index, row := range collections {
			definition, err := compileCollection(row)
			if err != nil {
				return err
			}
			models, err := loadModels(ctx, db, definition)
			if err != nil {
				return err
			}
			records, err := loadRecords(ctx, db, definition.Name(), environment)
			if err != nil {
				return err
			}
			inputs[index] = snapshot.CollectionInput{Definition: definition, Models: models, Version: catalog.ConfigRevision(row.ConfigRevision), Records: records}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load distribution environment %q: %w", environment, err)
	}
	return inputs, nil
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

var _ snapshot.Source = (*Source)(nil)
var _ snapshot.VersionSource = (*Source)(nil)

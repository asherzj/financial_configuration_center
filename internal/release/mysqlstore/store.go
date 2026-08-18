package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
	platformmysql "github.com/asherzj/financial_configuration_center/internal/platform/mysql"
	"github.com/asherzj/financial_configuration_center/internal/release/application"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

type Store struct{ database *platformmysql.Database }

func New(database *platformmysql.Database) (*Store, error) {
	if database == nil {
		return nil, errors.New("new release MySQL store: database is required")
	}
	return &Store{database: database}, nil
}

func (store *Store) WithinTransaction(ctx context.Context, work func(application.Transaction) error) error {
	if work == nil {
		return errors.New("release MySQL transaction callback is required")
	}
	err := store.database.WithinTransaction(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead}, func(db *gorm.DB) error {
		return work(&transaction{db: db})
	})
	return classifyTransactionError(err)
}

func classifyTransactionError(err error) error {
	if err == nil {
		return nil
	}
	var mysqlError *drivermysql.MySQLError
	if !errors.As(err, &mysqlError) {
		return err
	}
	switch mysqlError.Number {
	case 1205, 1213:
		return fmt.Errorf("%w: %v", application.ErrRetryableTransaction, err)
	case 1062:
		switch {
		case strings.Contains(mysqlError.Message, "uq_release_create_idempotency"):
			return fmt.Errorf("%w: %v", application.ErrCreateRequestRace, err)
		case strings.Contains(mysqlError.Message, "uq_release_item_active_conflict"):
			return fmt.Errorf("%w: %v", release.ErrActiveConflict, err)
		default:
			return fmt.Errorf("%w: duplicate persisted release fact", release.ErrAborted)
		}
	default:
		return err
	}
}

type transaction struct{ db *gorm.DB }

func (transaction *transaction) LoadCatalog(ctx context.Context, modelCode, releaseTypeCode string) (application.CatalogBundle, error) {
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
		TemplateCode          string
		TemplateVersion       uint64
		ReleaseTypeCode       string
		FinalEffect           string
		TemplateDocument      []byte
	}
	var loaded row
	result := transaction.db.WithContext(ctx).Raw(`
		SELECT
			c.name AS collection_name, c.description AS collection_description,
			c.fields, c.key_fields, c.sdk_delivery_enabled, c.schema_version,
			c.status AS collection_status,
			m.code AS model_code, m.name AS model_name, m.definition AS model_definition,
			m.enabled AS model_enabled, m.config_revision AS model_revision,
			t.code AS template_code, t.version AS template_version,
			t.release_type_code, t.final_effect, t.template AS template_document
		FROM configuration_models m
		JOIN configuration_collections c ON c.name = m.collection_name
		JOIN release_templates t ON t.model_code = m.code AND t.active_slot = 'A'
		WHERE m.code = ? AND t.release_type_code = ?
		FOR SHARE
	`, modelCode, releaseTypeCode).Scan(&loaded)
	if result.Error != nil {
		return application.CatalogBundle{}, result.Error
	}
	if result.RowsAffected != 1 {
		return application.CatalogBundle{}, fmt.Errorf("enabled model %q with one active release template was not found", modelCode)
	}
	if loaded.CollectionStatus != "ENABLED" || !loaded.ModelEnabled {
		return application.CatalogBundle{}, fmt.Errorf("model %q or its collection is disabled", modelCode)
	}
	var fields []catalog.FieldDefinition
	var keyFields []string
	if err := json.Unmarshal(loaded.Fields, &fields); err != nil {
		return application.CatalogBundle{}, fmt.Errorf("decode collection fields: %w", err)
	}
	if err := json.Unmarshal(loaded.KeyFields, &keyFields); err != nil {
		return application.CatalogBundle{}, fmt.Errorf("decode collection key fields: %w", err)
	}
	definition, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: loaded.CollectionName, Description: loaded.CollectionDescription, Fields: fields,
		KeyFields: keyFields, SDKDeliveryEnabled: loaded.SDKDeliveryEnabled, SchemaVersion: int64(loaded.SchemaVersion),
	})
	if err != nil {
		return application.CatalogBundle{}, fmt.Errorf("compile persisted collection: %w", err)
	}
	var modelSpec catalog.ModelSpec
	if err := json.Unmarshal(loaded.ModelDefinition, &modelSpec); err != nil {
		return application.CatalogBundle{}, fmt.Errorf("decode model definition: %w", err)
	}
	modelSpec.Code = loaded.ModelCode
	modelSpec.Name = loaded.ModelName
	modelSpec.Collection = loaded.CollectionName
	modelSpec.ConfigRevision = catalog.ConfigRevision(loaded.ModelRevision)
	model, err := catalog.CompileModel(definition, modelSpec)
	if err != nil {
		return application.CatalogBundle{}, fmt.Errorf("compile persisted model: %w", err)
	}
	template, err := release.CompileTemplate(loaded.TemplateDocument, release.FinalEffect(loaded.FinalEffect))
	if err != nil {
		return application.CatalogBundle{}, fmt.Errorf("compile persisted release template: %w", err)
	}
	return application.CatalogBundle{
		Definition: definition,
		Model:      model,
		Template: application.TemplateRef{
			Code: loaded.TemplateCode, Version: loaded.TemplateVersion, ReleaseTypeCode: loaded.ReleaseTypeCode, Definition: template,
		},
	}, nil
}

func (transaction *transaction) LoadBaseAuthority(ctx context.Context, collection, environment string, recordKeys []string) (release.BaseAuthority, error) {
	var revision uint64
	result := transaction.db.WithContext(ctx).Raw(`
		SELECT config_revision FROM configuration_versions
		WHERE collection_name = ? AND environment = ?
		FOR UPDATE
	`, collection, environment).Scan(&revision)
	if result.Error != nil {
		return release.BaseAuthority{}, result.Error
	}
	if result.RowsAffected != 1 || revision == 0 {
		return release.BaseAuthority{}, fmt.Errorf("collection version %s/%s was not found", collection, environment)
	}
	authority := release.BaseAuthority{CollectionRevision: catalog.ConfigRevision(revision), Records: make(map[string]*catalog.ConfigurationRecord, len(recordKeys))}
	if len(recordKeys) == 0 {
		return authority, nil
	}
	type recordRow struct {
		RecordKey      string
		Data           []byte
		ConfigRevision uint64
	}
	var rows []recordRow
	result = transaction.db.WithContext(ctx).Raw(`
		SELECT record_key, data, config_revision
		FROM configuration_records
		WHERE collection_name = ? AND environment = ? AND record_key IN ?
		FOR UPDATE
	`, collection, environment, recordKeys).Scan(&rows)
	if result.Error != nil {
		return release.BaseAuthority{}, result.Error
	}
	for _, row := range rows {
		var data map[string]string
		if err := json.Unmarshal(row.Data, &data); err != nil {
			return release.BaseAuthority{}, fmt.Errorf("decode base record %q: %w", row.RecordKey, err)
		}
		record := catalog.ConfigurationRecord{Collection: collection, Environment: environment, RecordKey: row.RecordKey, Data: data, ConfigRevision: catalog.ConfigRevision(row.ConfigRevision)}
		authority.Records[row.RecordKey] = &record
	}
	return authority, nil
}

func (transaction *transaction) LoadOverlayRules(ctx context.Context, collection string, scope release.Scope, recordKeys []string) ([]overlay.Rule, error) {
	if len(recordKeys) == 0 {
		return []overlay.Rule{}, nil
	}
	type row struct {
		ID, CollectionName, Region, Environment, Stage, RecordKey, Action, ReleaseOrderID string
		Content, RolloutRanges                                                            []byte
		ConfigRevision                                                                    uint64
		EffectiveFrom, EffectiveUntil, ActivatedAt, ExpiredAt                             *time.Time
		ActivatedRevision, ExpiredRevision                                                *uint64
		CreatedAt, UpdatedAt                                                              time.Time
		CreatedBy, UpdatedBy                                                              string
	}
	var rows []row
	result := transaction.db.WithContext(ctx).Raw(`
		SELECT id, collection_name, region, environment, stage, record_key, action,
			content, rollout_ranges, config_revision, release_order_id,
			effective_from, effective_until, activated_revision, activated_at,
			expired_revision, expired_at, created_at, created_by, updated_at, updated_by
		FROM configuration_overlays
		WHERE collection_name = ? AND environment = ? AND region = ?
		  AND stage IN ('', ?) AND record_key IN ?
		ORDER BY stage, record_key
		FOR UPDATE
	`, collection, scope.Environment, scope.Region, scope.Stage, recordKeys).Scan(&rows)
	if result.Error != nil {
		return nil, result.Error
	}
	rules := make([]overlay.Rule, len(rows))
	for index, loaded := range rows {
		var content map[string]string
		if len(loaded.Content) != 0 {
			if err := json.Unmarshal(loaded.Content, &content); err != nil {
				return nil, fmt.Errorf("decode overlay %q content: %w", loaded.ID, err)
			}
		}
		var ranges []overlay.BucketRange
		if err := json.Unmarshal(loaded.RolloutRanges, &ranges); err != nil {
			return nil, fmt.Errorf("decode overlay %q rollout ranges: %w", loaded.ID, err)
		}
		rule := overlay.Rule{
			ID: loaded.ID, Collection: loaded.CollectionName,
			Scope:     overlay.Scope{Region: loaded.Region, Environment: loaded.Environment, Stage: loaded.Stage},
			RecordKey: loaded.RecordKey, Action: overlay.Action(loaded.Action), Content: content, RolloutRanges: ranges,
			ConfigRevision: catalog.ConfigRevision(loaded.ConfigRevision), ReleaseOrderID: loaded.ReleaseOrderID,
			EffectiveFrom: loaded.EffectiveFrom, EffectiveUntil: loaded.EffectiveUntil,
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

func (transaction *transaction) FindCreateResult(ctx context.Context, actor, idempotencyKey string) (application.StoredRequestResult, bool, error) {
	type row struct {
		ID, RequestDigest, Status, CurrentStepCode, CurrentStepType, CurrentStepStatus string
		EntityRevision                                                                 uint64
	}
	var loaded row
	result := transaction.db.WithContext(ctx).Raw(`
		SELECT o.id, o.request_digest, o.status, o.current_step_code, s.step_type AS current_step_type,
			s.status AS current_step_status, o.entity_revision
		FROM release_orders o
		JOIN release_step_states s
		  ON s.release_order_id = o.id AND s.step_code = o.current_step_code
		WHERE o.created_by = ? AND o.idempotency_key = ?
		FOR UPDATE
	`, actor, idempotencyKey).Scan(&loaded)
	if result.Error != nil {
		return application.StoredRequestResult{}, false, result.Error
	}
	if result.RowsAffected == 0 {
		return application.StoredRequestResult{}, false, nil
	}
	view := application.OrderView{
		ID: loaded.ID, Status: release.OrderStatus(loaded.Status), CurrentStepCode: loaded.CurrentStepCode, CurrentStep: release.StepType(loaded.CurrentStepType),
		CurrentStepStatus: release.StepStatus(loaded.CurrentStepStatus), Revision: release.EntityRevision(loaded.EntityRevision),
	}
	type stepProjectionRow struct {
		Code   string
		Type   string
		Status string
	}
	var stepRows []stepProjectionRow
	if err := transaction.db.WithContext(ctx).Raw(`
		SELECT step_code AS code, step_type AS type, status
		FROM release_step_states
		WHERE release_order_id = ?
		ORDER BY sequence_no
	`, loaded.ID).Scan(&stepRows).Error; err != nil {
		return application.StoredRequestResult{}, false, err
	}
	view.Steps = make([]application.StepView, len(stepRows))
	for index, step := range stepRows {
		view.Steps[index] = application.StepView{Code: step.Code, Type: release.StepType(step.Type), Status: release.StepStatus(step.Status)}
	}
	setCapabilities(&view)
	return application.StoredRequestResult{
		RequestDigest: loaded.RequestDigest,
		Result:        view,
	}, true, nil
}

func setCapabilities(view *application.OrderView) {
	if view.Status != release.OrderInProgress {
		return
	}
	switch {
	case view.CurrentStep == release.StepManualReview && view.CurrentStepStatus == release.StepPending:
		view.CanExecute = true
	case view.CurrentStep == release.StepManualReview && view.CurrentStepStatus == release.StepExecuting:
		view.CanApprove, view.CanReject = true, true
	case view.CurrentStepStatus == release.StepApproved || view.CurrentStepStatus == release.StepExecuted:
		view.CanAdvance = view.CurrentStep != release.StepComplete
	case (view.CurrentStep == release.StepBaseApply || view.CurrentStep == release.StepComplete) && view.CurrentStepStatus == release.StepPending:
		view.CanExecute = true
	}
}

func (transaction *transaction) InsertOrder(ctx context.Context, order *release.Order) error {
	state := order.State()
	templateSnapshot, err := marshalTemplateSnapshot(state.Steps)
	if err != nil {
		return err
	}
	batchType := "SINGLE"
	if len(state.Items) > 1 {
		batchType = "BATCH"
	}
	result := transaction.db.WithContext(ctx).Exec(`
		INSERT INTO release_orders (
			id, release_number, idempotency_key, request_digest, model_code,
			template_code, template_version, release_type_code, region, environment, stage,
			status, current_step_code, template_snapshot, description, authorized_roles,
			batch_type, compensates_order_id, entity_revision,
			created_at, created_by, updated_at, updated_by, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', JSON_ARRAY(), ?, NULL, ?, ?, ?, ?, ?, NULL)
	`, state.ID, state.ReleaseNumber, state.IdempotencyKey, state.RequestDigest, state.ModelCode,
		state.TemplateCode, state.TemplateVersion, state.ReleaseTypeCode,
		state.Scope.Region, state.Scope.Environment, state.Scope.Stage,
		state.Status, persistedStepCode(state.Steps[state.CurrentStep]), templateSnapshot, batchType, state.Revision,
		state.CreatedAt, state.CreatedBy, state.UpdatedAt, state.UpdatedBy)
	if result.Error != nil {
		return result.Error
	}
	for position, item := range state.Items {
		baseBefore, err := marshalRecordData(item.BaseBefore)
		if err != nil {
			return err
		}
		effectiveBefore, err := marshalRecordData(item.EffectiveBefore)
		if err != nil {
			return err
		}
		after, err := marshalRecordData(item.After)
		if err != nil {
			return err
		}
		if err := transaction.db.WithContext(ctx).Exec(`
			INSERT INTO release_order_items (
				id, release_order_id, position, action, collection_name, record_key,
				target, target_description, base_before, effective_before, after_data,
				expected_record_revision, expected_collection_revision, preserve_sensitive_fields,
				status, active_conflict_key, entity_revision,
				created_at, created_by, updated_at, updated_by
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, JSON_ARRAY(), ?, ?, 1, ?, ?, ?, ?)
		`, item.ID, state.ID, position, item.Action, item.Collection, item.RecordKey,
			item.RecordKey, item.RecordKey, baseBefore, effectiveBefore, after, item.ExpectedRecordRevision, item.ExpectedCollectionRevision,
			item.Status, nullableString(item.ActiveConflictKey), state.CreatedAt, state.CreatedBy, state.UpdatedAt, state.UpdatedBy).Error; err != nil {
			return err
		}
	}
	for sequence, step := range state.Steps {
		stepContext, err := json.Marshal(map[string]any{
			"requiredRoles": step.RequiredRoles, "selfApprovalPolicy": step.SelfApprovalPolicy,
			"rolloutRanges": step.RolloutRanges,
		})
		if err != nil {
			return err
		}
		if err := transaction.db.WithContext(ctx).Exec(`
			INSERT INTO release_step_states (
				release_order_id, step_code, step_type, sequence_no, status, context,
				approval, effect, compare_result, execute_count, executed_at, executed_by,
				rolled_back_at, rolled_back_by, error_code, error_message, entity_revision,
				created_at, created_by, updated_at, updated_by
			) VALUES (?, ?, ?, ?, ?, ?, NULL, NULL, NULL, 0, NULL, NULL, NULL, NULL, NULL, NULL, 1, ?, ?, ?, ?)
		`, state.ID, persistedStepCode(step), step.Type, sequence, step.Status, stepContext,
			state.CreatedAt, state.CreatedBy, state.UpdatedAt, state.UpdatedBy).Error; err != nil {
			return err
		}
	}
	return transaction.insertAudit(ctx, state.CreatedAt, state.CreatedBy, "RELEASE_CREATED", "RELEASE_ORDER", state.ID, state.Scope, state.ID)
}

func (transaction *transaction) LoadOrderForUpdate(ctx context.Context, orderID string) (*release.Order, error) {
	type orderRow struct {
		ID, ReleaseNumber, IdempotencyKey, RequestDigest, ModelCode string
		TemplateCode, ReleaseTypeCode                               string
		TemplateVersion                                             uint64
		Region, Environment, Stage                                  string
		Status, CurrentStepCode                                     string
		EntityRevision                                              uint64
		CreatedAt, UpdatedAt                                        time.Time
		CreatedBy, UpdatedBy                                        string
		CompletedAt                                                 *time.Time
	}
	var loaded orderRow
	result := transaction.db.WithContext(ctx).Raw(`
		SELECT id, release_number, idempotency_key, request_digest, model_code,
			template_code, template_version, release_type_code, region, environment, stage,
			status, current_step_code, entity_revision, created_at, created_by,
			updated_at, updated_by, completed_at
		FROM release_orders WHERE id = ? FOR UPDATE
	`, orderID).Scan(&loaded)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, fmt.Errorf("release order %q was not found", orderID)
	}
	type itemRow struct {
		ID, Action, CollectionName, RecordKey, Status      string
		BaseBefore, EffectiveBefore, AfterData             []byte
		ExpectedRecordRevision, ExpectedCollectionRevision uint64
		ActiveConflictKey                                  *string
	}
	var itemRows []itemRow
	if err := transaction.db.WithContext(ctx).Raw(`
		SELECT id, action, collection_name, record_key, base_before, effective_before,
			after_data, expected_record_revision, expected_collection_revision,
			status, active_conflict_key
		FROM release_order_items WHERE release_order_id = ? ORDER BY position
	`, orderID).Scan(&itemRows).Error; err != nil {
		return nil, err
	}
	items := make([]release.Item, len(itemRows))
	for index, row := range itemRows {
		baseBefore, err := unmarshalRecord(row.BaseBefore, row.CollectionName, loaded.Environment, row.RecordKey, 0)
		if err != nil {
			return nil, err
		}
		effectiveBefore, err := unmarshalRecord(row.EffectiveBefore, row.CollectionName, loaded.Environment, row.RecordKey, 0)
		if err != nil {
			return nil, err
		}
		after, err := unmarshalRecord(row.AfterData, row.CollectionName, loaded.Environment, row.RecordKey, 0)
		if err != nil {
			return nil, err
		}
		active := ""
		if row.ActiveConflictKey != nil {
			active = *row.ActiveConflictKey
		}
		items[index] = release.Item{
			ID: row.ID, Action: release.ChangeAction(row.Action), Collection: row.CollectionName, RecordKey: row.RecordKey,
			BaseBefore: baseBefore, EffectiveBefore: effectiveBefore, After: after,
			ExpectedRecordRevision: catalog.ConfigRevision(row.ExpectedRecordRevision), ExpectedCollectionRevision: catalog.ConfigRevision(row.ExpectedCollectionRevision),
			Status: release.ItemStatus(row.Status), ActiveConflictKey: active,
		}
	}
	type stepRow struct {
		StepCode, StepType, Status, ExecutedBy string
		Context, Approval, Effect              []byte
		ExecutedAt, RolledBackAt               *time.Time
		RolledBackBy                           string
	}
	var stepRows []stepRow
	if err := transaction.db.WithContext(ctx).Raw(`
		SELECT step_code, step_type, status, context, approval, effect, executed_at,
			COALESCE(executed_by, '') AS executed_by, rolled_back_at, COALESCE(rolled_back_by, '') AS rolled_back_by
		FROM release_step_states WHERE release_order_id = ? ORDER BY sequence_no
	`, orderID).Scan(&stepRows).Error; err != nil {
		return nil, err
	}
	steps := make([]release.StepState, len(stepRows))
	currentStep := -1
	for index, row := range stepRows {
		var contextValue struct {
			RequiredRoles      []string                   `json:"requiredRoles"`
			SelfApprovalPolicy release.SelfApprovalPolicy `json:"selfApprovalPolicy"`
			RolloutRanges      []overlay.BucketRange      `json:"rolloutRanges"`
		}
		if err := json.Unmarshal(row.Context, &contextValue); err != nil {
			return nil, fmt.Errorf("decode step %q context: %w", row.StepCode, err)
		}
		var approval *release.ApprovalState
		if len(row.Approval) != 0 {
			approval = &release.ApprovalState{}
			if err := json.Unmarshal(row.Approval, approval); err != nil {
				return nil, fmt.Errorf("decode step %q approval: %w", row.StepCode, err)
			}
		}
		var effect *release.StepEffectEnvelope
		if len(row.Effect) != 0 {
			effect = &release.StepEffectEnvelope{}
			if err := json.Unmarshal(row.Effect, effect); err != nil {
				return nil, fmt.Errorf("decode step %q effect: %w", row.StepCode, err)
			}
			if effect.EffectVersion != 1 || effect.Overlay == nil || effect.Overlay.EffectVersion != 1 {
				return nil, fmt.Errorf("decode step %q effect: unsupported effect envelope", row.StepCode)
			}
		}
		steps[index] = release.StepState{
			Code: row.StepCode, Type: release.StepType(row.StepType), Status: release.StepStatus(row.Status),
			RequiredRoles: contextValue.RequiredRoles, SelfApprovalPolicy: contextValue.SelfApprovalPolicy,
			RolloutRanges: contextValue.RolloutRanges,
			Approval:      approval, Effect: effect, ExecutedAt: row.ExecutedAt, ExecutedBy: row.ExecutedBy,
			RolledBackAt: row.RolledBackAt, RolledBackBy: row.RolledBackBy,
		}
		if row.StepCode == loaded.CurrentStepCode {
			currentStep = index
		}
	}
	return release.RestoreOrder(release.OrderState{
		ID: loaded.ID, ReleaseNumber: loaded.ReleaseNumber, IdempotencyKey: loaded.IdempotencyKey,
		RequestDigest: loaded.RequestDigest, ModelCode: loaded.ModelCode, TemplateCode: loaded.TemplateCode,
		TemplateVersion: loaded.TemplateVersion, ReleaseTypeCode: loaded.ReleaseTypeCode,
		Scope:     release.Scope{Region: loaded.Region, Environment: loaded.Environment, Stage: loaded.Stage},
		CreatedBy: loaded.CreatedBy, CreatedAt: loaded.CreatedAt, UpdatedBy: loaded.UpdatedBy, UpdatedAt: loaded.UpdatedAt,
		CompletedAt: loaded.CompletedAt, Status: release.OrderStatus(loaded.Status), Revision: release.EntityRevision(loaded.EntityRevision),
		CurrentStep: currentStep, Steps: steps, Items: items,
	})
}

func (transaction *transaction) FindActionResult(ctx context.Context, orderID, actionRequestID string) (application.StoredRequestResult, bool, error) {
	type row struct {
		RequestDigest    string
		ResultProjection []byte
	}
	var loaded row
	result := transaction.db.WithContext(ctx).Raw(`
		SELECT request_digest, result_projection
		FROM release_action_requests
		WHERE release_order_id = ? AND action_request_id = ?
	`, orderID, actionRequestID).Scan(&loaded)
	if result.Error != nil {
		return application.StoredRequestResult{}, false, result.Error
	}
	if result.RowsAffected == 0 {
		return application.StoredRequestResult{}, false, nil
	}
	var projection application.OrderView
	if err := json.Unmarshal(loaded.ResultProjection, &projection); err != nil {
		return application.StoredRequestResult{}, false, fmt.Errorf("decode action result projection: %w", err)
	}
	return application.StoredRequestResult{RequestDigest: loaded.RequestDigest, Result: projection}, true, nil
}

func (transaction *transaction) AllocateConfigRevision(ctx context.Context) (catalog.ConfigRevision, error) {
	var current uint64
	result := transaction.db.WithContext(ctx).Raw(`
		SELECT current_revision FROM configuration_revision_counters
		WHERE counter_name = 'global' FOR UPDATE
	`).Scan(&current)
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected != 1 {
		return 0, errors.New("global config revision counter was not found")
	}
	next := current + 1
	if err := transaction.db.WithContext(ctx).Exec(`
		UPDATE configuration_revision_counters SET current_revision = ?, updated_at = UTC_TIMESTAMP(6)
		WHERE counter_name = 'global' AND current_revision = ?
	`, next, current).Error; err != nil {
		return 0, err
	}
	return catalog.ConfigRevision(next), nil
}

func (transaction *transaction) ApplyBaseEffect(ctx context.Context, orderID string, effect release.BaseEffect, revision catalog.ConfigRevision) error {
	for _, change := range effect.Changes {
		if change.Action != release.ChangeAdd {
			return fmt.Errorf("base effect action %q is not implemented", change.Action)
		}
		data, err := json.Marshal(change.After.Data)
		if err != nil {
			return err
		}
		if err := transaction.db.WithContext(ctx).Exec(`
			INSERT INTO configuration_records (
				collection_name, environment, record_key, data, config_revision,
				created_at, created_by, updated_at, updated_by
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, effect.Collection, effect.Environment, change.After.RecordKey, data, revision,
			effect.ExecutedAt, effect.ExecutedBy, effect.ExecutedAt, effect.ExecutedBy).Error; err != nil {
			return err
		}
		if err := transaction.db.WithContext(ctx).Exec(`
			INSERT INTO configuration_change_log (
				collection_name, kind, region, environment, stage, record_key, action,
				before_data, after_data, config_revision, release_order_id, created_at
			) SELECT ?, 'BASE_RECORD', region, environment, stage, ?, 'ADD', NULL, ?, ?, id, ?
			FROM release_orders WHERE id = ?
		`, effect.Collection, change.After.RecordKey, data, revision, effect.ExecutedAt, orderID).Error; err != nil {
			return err
		}
	}

	type recordRow struct {
		RecordKey string
		Data      []byte
	}
	var rows []recordRow
	if err := transaction.db.WithContext(ctx).Raw(`
		SELECT record_key, data FROM configuration_records
		WHERE collection_name = ? AND environment = ? ORDER BY record_key
	`, effect.Collection, effect.Environment).Scan(&rows).Error; err != nil {
		return err
	}
	records := make([]catalog.ConfigurationRecord, len(rows))
	for index, row := range rows {
		var data map[string]string
		if err := json.Unmarshal(row.Data, &data); err != nil {
			return err
		}
		records[index] = catalog.ConfigurationRecord{RecordKey: row.RecordKey, Data: data}
	}
	digest, err := catalog.ComputeBaseDigest(records)
	if err != nil {
		return err
	}
	result := transaction.db.WithContext(ctx).Exec(`
		UPDATE configuration_versions
		SET config_revision = ?, base_digest = ?, release_order_id = ?, updated_at = ?
		WHERE collection_name = ? AND environment = ? AND config_revision = ?
	`, revision, digest.Value, orderID, effect.ExecutedAt, effect.Collection, effect.Environment, effect.PreviousRevision)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("%w: collection version changed while applying base effect", release.ErrAborted)
	}
	payload, _ := json.Marshal(map[string]any{
		"schemaVersion": 1, "collection": effect.Collection, "environment": effect.Environment,
		"configRevision": revision, "releaseOrderId": orderID,
	})
	if err := transaction.db.WithContext(ctx).Exec(`
		INSERT INTO outbox_events (
			id, aggregate_type, aggregate_id, event_type, payload_version, payload,
			idempotency_key, status, lease_revision, attempts, next_attempt_at,
			created_at, updated_at
		) VALUES (UUID(), 'RELEASE_ORDER', ?, 'CONFIGURATION_CHANGED', 1, ?, ?, 'PENDING', 1, 0, ?, ?, ?)
	`, orderID, payload, fmt.Sprintf("configuration-changed:%s:%d", orderID, revision), effect.ExecutedAt, effect.ExecutedAt, effect.ExecutedAt).Error; err != nil {
		return err
	}
	return transaction.insertAudit(ctx, effect.ExecutedAt, effect.ExecutedBy, "BASE_APPLY", "RELEASE_ORDER", orderID, release.Scope{Environment: effect.Environment}, orderID)
}

func (transaction *transaction) ApplyOverlayEffect(ctx context.Context, orderID string, effect release.OverlayEffect) error {
	if effect.EffectVersion != 1 || effect.AppliedRevision <= effect.PreviousRevision || len(effect.Changes) == 0 {
		return fmt.Errorf("invalid overlay effect")
	}
	for _, change := range effect.Changes {
		if change.NewRule == nil {
			if err := transaction.db.WithContext(ctx).Exec(`
				DELETE FROM configuration_overlays
				WHERE collection_name = ? AND region = ? AND environment = ? AND stage = ? AND record_key = ?
			`, effect.Collection, effect.Scope.Region, effect.Scope.Environment, effect.Scope.Stage, change.RecordKey).Error; err != nil {
				return err
			}
		} else if err := transaction.upsertOverlayRule(ctx, *change.NewRule); err != nil {
			return err
		}
		before, err := overlayChangeData(change.PreviousRule)
		if err != nil {
			return err
		}
		after, err := overlayChangeData(change.NewRule)
		if err != nil {
			return err
		}
		action := "MODIFY"
		if change.PreviousRule == nil {
			action = "ADD"
		} else if change.NewRule == nil {
			action = "DELETE"
		}
		if err := transaction.db.WithContext(ctx).Exec(`
			INSERT INTO configuration_change_log (
				collection_name, kind, region, environment, stage, record_key, action,
				before_data, after_data, config_revision, release_order_id, created_at
			) VALUES (?, 'OVERLAY', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, effect.Collection, effect.Scope.Region, effect.Scope.Environment, effect.Scope.Stage,
			change.RecordKey, action, before, after, effect.AppliedRevision, orderID, effect.ExecutedAt).Error; err != nil {
			return err
		}
	}

	rules, err := transaction.loadEnvironmentOverlayRules(ctx, effect.Collection, effect.Scope.Environment)
	if err != nil {
		return err
	}
	digest, err := overlay.ComputeDigest(rules)
	if err != nil {
		return err
	}
	result := transaction.db.WithContext(ctx).Exec(`
		UPDATE configuration_versions
		SET config_revision = ?, overlay_digest = ?, release_order_id = ?, updated_at = ?
		WHERE collection_name = ? AND environment = ? AND config_revision = ?
	`, effect.AppliedRevision, digest.Value, orderID, effect.ExecutedAt,
		effect.Collection, effect.Scope.Environment, effect.PreviousRevision)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("%w: collection version changed while applying overlay effect", release.ErrAborted)
	}
	payload, _ := json.Marshal(map[string]any{
		"schemaVersion": 1, "collection": effect.Collection, "scope": effect.Scope,
		"configRevision": effect.AppliedRevision, "releaseOrderId": orderID,
	})
	if err := transaction.db.WithContext(ctx).Exec(`
		INSERT INTO outbox_events (
			id, aggregate_type, aggregate_id, event_type, payload_version, payload,
			idempotency_key, status, lease_revision, attempts, next_attempt_at,
			created_at, updated_at
		) VALUES (UUID(), 'RELEASE_ORDER', ?, 'CONFIGURATION_CHANGED', 1, ?, ?, 'PENDING', 1, 0, ?, ?, ?)
	`, orderID, payload, fmt.Sprintf("configuration-changed:%s:%d", orderID, effect.AppliedRevision),
		effect.ExecutedAt, effect.ExecutedAt, effect.ExecutedAt).Error; err != nil {
		return err
	}
	return transaction.insertAudit(ctx, effect.ExecutedAt, effect.ExecutedBy, "OVERLAY_APPLY", "RELEASE_ORDER", orderID, effect.Scope, orderID)
}

func (transaction *transaction) upsertOverlayRule(ctx context.Context, rule overlay.Rule) error {
	var content any
	if rule.Content != nil {
		encoded, err := json.Marshal(rule.Content)
		if err != nil {
			return err
		}
		content = encoded
	}
	ranges := rule.RolloutRanges
	if ranges == nil {
		ranges = []overlay.BucketRange{}
	}
	encodedRanges, err := json.Marshal(ranges)
	if err != nil {
		return err
	}
	return transaction.db.WithContext(ctx).Exec(`
		INSERT INTO configuration_overlays (
			id, collection_name, region, environment, stage, record_key, action,
			content, rollout_ranges, config_revision, release_order_id,
			effective_from, effective_until, activated_revision, activated_at,
			expired_revision, expired_at, created_at, created_by, updated_at, updated_by
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			id = VALUES(id), action = VALUES(action), content = VALUES(content),
			rollout_ranges = VALUES(rollout_ranges), config_revision = VALUES(config_revision),
			release_order_id = VALUES(release_order_id), effective_from = VALUES(effective_from),
			effective_until = VALUES(effective_until), activated_revision = VALUES(activated_revision),
			activated_at = VALUES(activated_at), expired_revision = VALUES(expired_revision),
			expired_at = VALUES(expired_at), created_at = VALUES(created_at), created_by = VALUES(created_by),
			updated_at = VALUES(updated_at), updated_by = VALUES(updated_by)
	`, rule.ID, rule.Collection, rule.Scope.Region, rule.Scope.Environment, rule.Scope.Stage,
		rule.RecordKey, rule.Action, content, encodedRanges, rule.ConfigRevision, rule.ReleaseOrderID,
		rule.EffectiveFrom, rule.EffectiveUntil, rule.ActivatedRevision, rule.ActivatedAt,
		rule.ExpiredRevision, rule.ExpiredAt, rule.CreatedAt, rule.CreatedBy, rule.UpdatedAt, rule.UpdatedBy).Error
}

func (transaction *transaction) loadEnvironmentOverlayRules(ctx context.Context, collection, environment string) ([]overlay.Rule, error) {
	type row struct {
		ID, Region, Stage, RecordKey, Action string
		Content, RolloutRanges               []byte
		ActivatedRevision, ExpiredRevision   *uint64
	}
	var rows []row
	if err := transaction.db.WithContext(ctx).Raw(`
		SELECT id, region, stage, record_key, action, content, rollout_ranges,
			activated_revision, expired_revision
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
				return nil, err
			}
		}
		var ranges []overlay.BucketRange
		if err := json.Unmarshal(loaded.RolloutRanges, &ranges); err != nil {
			return nil, err
		}
		rule := overlay.Rule{
			ID: loaded.ID, Collection: collection, Scope: overlay.Scope{Region: loaded.Region, Environment: environment, Stage: loaded.Stage},
			RecordKey: loaded.RecordKey, Action: overlay.Action(loaded.Action), Content: content, RolloutRanges: ranges,
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

func overlayChangeData(rule *overlay.Rule) (any, error) {
	if rule == nil {
		return nil, nil
	}
	return json.Marshal(rule.Content)
}

func (transaction *transaction) SaveOrder(ctx context.Context, order *release.Order) error {
	state := order.State()
	if state.Revision <= 1 {
		return errors.New("save release order requires an incremented entity revision")
	}
	result := transaction.db.WithContext(ctx).Exec(`
		UPDATE release_orders
		SET status = ?, current_step_code = ?, entity_revision = ?, updated_at = ?, updated_by = ?, completed_at = ?
		WHERE id = ? AND entity_revision = ?
	`, state.Status, persistedStepCode(state.Steps[state.CurrentStep]), state.Revision, state.UpdatedAt, state.UpdatedBy, state.CompletedAt, state.ID, state.Revision-1)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("%w: order entity revision changed", release.ErrAborted)
	}
	for _, item := range state.Items {
		if err := transaction.db.WithContext(ctx).Exec(`
			UPDATE release_order_items
			SET status = ?, active_conflict_key = ?, entity_revision = entity_revision + 1,
				updated_at = ?, updated_by = ?
			WHERE id = ? AND release_order_id = ?
		`, item.Status, nullableString(item.ActiveConflictKey), state.UpdatedAt, state.UpdatedBy, item.ID, state.ID).Error; err != nil {
			return err
		}
	}
	for _, step := range state.Steps {
		var approval any
		if step.Approval != nil {
			encoded, err := json.Marshal(step.Approval)
			if err != nil {
				return err
			}
			approval = encoded
		}
		var effect any
		if step.Effect != nil {
			encoded, err := json.Marshal(step.Effect)
			if err != nil {
				return err
			}
			effect = encoded
		}
		if err := transaction.db.WithContext(ctx).Exec(`
			UPDATE release_step_states
			SET status = ?, approval = ?, effect = ?, executed_at = ?, executed_by = ?,
				rolled_back_at = ?, rolled_back_by = ?, entity_revision = entity_revision + 1,
				updated_at = ?, updated_by = ?
			WHERE release_order_id = ? AND step_code = ?
		`, step.Status, approval, effect, step.ExecutedAt, nullableString(step.ExecutedBy),
			step.RolledBackAt, nullableString(step.RolledBackBy), state.UpdatedAt, state.UpdatedBy, state.ID, persistedStepCode(step)).Error; err != nil {
			return err
		}
	}
	return nil
}

func (transaction *transaction) InsertActionResult(ctx context.Context, orderID, actionRequestID, requestDigest string, projection application.OrderView, createdAt time.Time) error {
	encoded, err := json.Marshal(projection)
	if err != nil {
		return fmt.Errorf("encode action result projection: %w", err)
	}
	return transaction.db.WithContext(ctx).Exec(`
		INSERT INTO release_action_requests (
			release_order_id, action_request_id, request_digest, result_projection, created_at
		) VALUES (?, ?, ?, ?, ?)
	`, orderID, actionRequestID, requestDigest, encoded, createdAt).Error
}

func (transaction *transaction) RecordAction(ctx context.Context, record application.ActionRecord) error {
	message := fmt.Sprintf("%s succeeded", record.Action)
	if err := transaction.db.WithContext(ctx).Exec(`
		INSERT INTO release_operation_logs (
			id, release_order_id, release_item_id, step_code, action, result,
			actor_subject, actor_name, message, error_code, error_detail, trace_id, created_at
		) VALUES (UUID(), ?, NULL, ?, ?, 'SUCCEEDED', ?, ?, ?, NULL, NULL, '', ?)
	`, record.OrderID, nullableString(record.StepCode), record.Action, record.Actor, record.Actor, message, record.At).Error; err != nil {
		return err
	}
	return transaction.insertAudit(ctx, record.At, record.Actor, string(record.Action), "RELEASE_ORDER", record.OrderID, record.Scope, record.OrderID)
}

func (transaction *transaction) insertAudit(ctx context.Context, at time.Time, actor, action, resourceType, resourceID string, scope release.Scope, requestID string) error {
	return transaction.db.WithContext(ctx).Exec(`
		INSERT INTO audit_records (
			occurred_at, principal_subject, principal_display_name, action,
			resource_type, resource_id, region, environment, stage, result,
			before_data, after_data, metadata, request_id, trace_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'SUCCEEDED', NULL, NULL, JSON_OBJECT(), ?, '')
	`, at, actor, actor, action, resourceType, resourceID, scope.Region, scope.Environment, scope.Stage, requestID).Error
}

func stepCode(stepType release.StepType) string {
	switch stepType {
	case release.StepBaseApply:
		return "base-apply"
	case release.StepComplete:
		return "complete"
	default:
		return string(stepType)
	}
}

func persistedStepCode(step release.StepState) string {
	if step.Code != "" {
		return step.Code
	}
	return stepCode(step.Type)
}

func marshalTemplateSnapshot(steps []release.StepState) ([]byte, error) {
	type definition struct {
		Code               string                     `json:"code"`
		Type               release.StepType           `json:"type"`
		RequiredRoles      []string                   `json:"requiredRoles,omitempty"`
		SelfApprovalPolicy release.SelfApprovalPolicy `json:"selfApprovalPolicy,omitempty"`
		RolloutRanges      []overlay.BucketRange      `json:"rolloutRanges,omitempty"`
	}
	finalEffect := release.FinalEffectBase
	for _, step := range steps {
		if step.Type == release.StepOverlayApply {
			finalEffect = release.FinalEffectOverlay
		}
	}
	document := struct {
		FinalEffect release.FinalEffect `json:"finalEffect"`
		Steps       []definition        `json:"steps"`
	}{FinalEffect: finalEffect, Steps: make([]definition, len(steps))}
	for index, step := range steps {
		document.Steps[index] = definition{
			Code: persistedStepCode(step), Type: step.Type, RequiredRoles: append([]string(nil), step.RequiredRoles...),
			SelfApprovalPolicy: step.SelfApprovalPolicy, RolloutRanges: append([]overlay.BucketRange(nil), step.RolloutRanges...),
		}
	}
	return json.Marshal(document)
}

func marshalRecordData(record *catalog.ConfigurationRecord) ([]byte, error) {
	if record == nil {
		return nil, nil
	}
	return json.Marshal(record.Data)
}

func unmarshalRecord(data []byte, collection, environment, recordKey string, revision catalog.ConfigRevision) (*catalog.ConfigurationRecord, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var values map[string]string
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	return &catalog.ConfigurationRecord{Collection: collection, Environment: environment, RecordKey: recordKey, Data: values, ConfigRevision: revision}, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

var _ application.UnitOfWork = (*Store)(nil)

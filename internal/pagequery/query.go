package pagequery

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
)

type SnapshotProvider interface {
	Current() *snapshot.Snapshot
}

type QueryType string

const (
	TypeAll      QueryType = "ALL"
	TypeOnlyData QueryType = "ONLY_DATA"
)

type PageSpec struct {
	Number *int32
	Size   *int32
}

type Request struct {
	ModelCode     string
	Region        string
	Environment   string
	Stage         string
	PreviewBucket *int32
	Type          QueryType
	Page          PageSpec
}

type InteractionField struct {
	Name                   string
	DisplayName            string
	Description            string
	Type                   catalog.FieldType
	UIControl              catalog.UIControlType
	Queryable              bool
	Editable               bool
	Required               bool
	Sensitive              bool
	Projected              bool
	KeyField               bool
	AllowedFilterOperators []catalog.FilterOperator
	DefaultFilterOperator  catalog.FilterOperator
	DefaultValue           *string
	AutoFill               *catalog.AutoFillRule
	ValidationRules        []catalog.ValidationRule
	DisplayOrder           int32
	Options                []catalog.SelectOptionDefinition
}

type Row struct {
	RecordKey      string
	RecordRevision catalog.ConfigRevision
	Values         map[string]string
	BasePresent    bool
	BaseValues     map[string]string
	ChangedFields  []string
	MaskedFields   []string
}

type ReleaseType struct {
	Code                  string
	Name                  string
	TemplateCode          string
	Available             bool
	UnavailableReasonCode string
}

type Result struct {
	ModelCode          string
	ModelName          string
	QueryType          QueryType
	Rows               []Row
	ProjectionFields   []string
	InteractionFields  []InteractionField
	ReleaseTypes       []ReleaseType
	PageNumber         int32
	PageSize           int32
	TotalNumber        int64
	TotalPages         int64
	Snapshot           snapshot.Identity
	ModelRevision      catalog.ConfigRevision
	CollectionRevision catalog.ConfigRevision
}

type Querier struct{ snapshots SnapshotProvider }

func New(snapshots SnapshotProvider) *Querier { return &Querier{snapshots: snapshots} }

func (querier *Querier) Query(request Request) (Result, error) {
	if querier == nil || querier.snapshots == nil {
		return Result{}, errors.New("page query: snapshot provider is required")
	}
	request.ModelCode = strings.TrimSpace(request.ModelCode)
	request.Region = strings.TrimSpace(request.Region)
	request.Environment = strings.TrimSpace(request.Environment)
	request.Stage = strings.TrimSpace(request.Stage)
	if request.ModelCode == "" || request.Region == "" || request.Environment == "" {
		return Result{}, errors.New("page query: model, region, and environment are required")
	}
	if request.PreviewBucket != nil && (*request.PreviewBucket < 0 || *request.PreviewBucket > 99) {
		return Result{}, errors.New("page query: preview bucket must be between 0 and 99")
	}
	if request.Type != TypeAll && request.Type != TypeOnlyData {
		return Result{}, fmt.Errorf("page query: unsupported query type %q", request.Type)
	}
	current := querier.snapshots.Current()
	if current == nil || current.Environment() != request.Environment {
		return Result{}, errors.New("page query: requested environment is not loaded")
	}
	model, exists := current.Model(request.ModelCode)
	if !exists {
		return Result{}, fmt.Errorf("page query: model %q is not loaded", request.ModelCode)
	}
	definition, exists := current.Definition(model.Collection())
	if !exists {
		return Result{}, errors.New("page query: model collection is not loaded")
	}
	collectionRevision, exists := current.CollectionVersion(model.Collection())
	if !exists {
		return Result{}, errors.New("page query: collection version is not loaded")
	}

	pageNumber, pageSize, err := normalizePage(request.Page, model.DefaultPageSize(), model.MaxPageSize())
	if err != nil {
		return Result{}, err
	}
	projection := model.ProjectionFields()
	projectionSet := make(map[string]struct{}, len(projection))
	for _, field := range projection {
		projectionSet[field] = struct{}{}
	}
	baseRecords := current.Records(model.Collection())
	records, err := overlay.Evaluate(overlay.Query{
		Collection:    model.Collection(),
		Scope:         overlay.Scope{Region: request.Region, Environment: request.Environment, Stage: request.Stage},
		PreviewBucket: request.PreviewBucket,
	}, baseRecords, current.OverlayRules(model.Collection()))
	if err != nil {
		return Result{}, fmt.Errorf("page query: evaluate scope: %w", err)
	}
	baseByKey := make(map[string]catalog.ConfigurationRecord, len(baseRecords))
	for _, record := range baseRecords {
		baseByKey[record.RecordKey] = record
	}
	rows := make([]Row, len(records))
	modelFields := model.Fields()
	sensitiveFields := make(map[string]struct{})
	for _, field := range modelFields {
		if field.Sensitive {
			sensitiveFields[field.Name] = struct{}{}
		}
	}
	for index, record := range records {
		values := make(map[string]string, len(projection))
		baseValues := make(map[string]string, len(projection))
		base, basePresent := baseByKey[record.RecordKey]
		changedFields := make([]string, 0, len(projection))
		for _, field := range projection {
			if _, sensitive := sensitiveFields[field]; sensitive {
				_, effectivePresent := record.Data[field]
				_, baseValuePresent := base.Data[field]
				if effectivePresent != (basePresent && baseValuePresent) || (effectivePresent && basePresent && record.Data[field] != base.Data[field]) {
					changedFields = append(changedFields, field)
				}
				continue
			}
			if value, present := record.Data[field]; present {
				values[field] = value
			}
			if value, present := base.Data[field]; basePresent && present {
				baseValues[field] = value
			}
			if !basePresent || values[field] != baseValues[field] {
				changedFields = append(changedFields, field)
			}
		}
		var masked []string
		for _, field := range modelFields {
			if field.Sensitive {
				if _, present := record.Data[field.Name]; present {
					masked = append(masked, field.Name)
				}
			}
		}
		sort.Strings(masked)
		rows[index] = Row{
			RecordKey: record.RecordKey, RecordRevision: record.ConfigRevision, Values: values,
			BasePresent: basePresent, BaseValues: baseValues, ChangedFields: changedFields, MaskedFields: masked,
		}
	}
	total := int64(len(rows))
	totalPages := int64(0)
	if total > 0 {
		totalPages = (total + int64(pageSize) - 1) / int64(pageSize)
	}
	start := int64(pageNumber-1) * int64(pageSize)
	end := start + int64(pageSize)
	if start >= total {
		rows = []Row{}
	} else {
		if end > total {
			end = total
		}
		rows = rows[start:end]
	}

	result := Result{
		ModelCode: model.Code(), ModelName: model.Name(), QueryType: request.Type,
		Rows: rows, PageNumber: pageNumber, PageSize: pageSize, TotalNumber: total, TotalPages: totalPages,
		Snapshot: current.Identity(), ModelRevision: model.ConfigRevision(), CollectionRevision: collectionRevision,
	}
	if request.Type == TypeAll {
		result.ProjectionFields = slices.Clone(projection)
		result.InteractionFields, err = interactionFields(current, request, definition, model, projectionSet)
		if err != nil {
			return Result{}, err
		}
		definitions := model.ReleaseTypes()
		result.ReleaseTypes = make([]ReleaseType, len(definitions))
		for index, definition := range definitions {
			result.ReleaseTypes[index] = ReleaseType{
				Code: definition.Code, Name: definition.Name, TemplateCode: definition.TemplateCode,
				Available: definition.Enabled && definition.Available, UnavailableReasonCode: definition.UnavailableReasonCode,
			}
		}
	}
	return result, nil
}

func normalizePage(page PageSpec, defaultSize, maxSize int32) (int32, int32, error) {
	number := int32(1)
	if page.Number != nil {
		if *page.Number <= 0 {
			return 0, 0, errors.New("page query: explicit page number must be positive")
		}
		number = *page.Number
	}
	size := defaultSize
	if page.Size != nil {
		if *page.Size <= 0 {
			return 0, 0, errors.New("page query: explicit page size must be positive")
		}
		size = *page.Size
	}
	if size > maxSize {
		return 0, 0, fmt.Errorf("page query: page size %d exceeds maximum %d", size, maxSize)
	}
	return number, size, nil
}

func interactionFields(current *snapshot.Snapshot, request Request, definition catalog.CollectionDefinition, model catalog.CompiledModel, projection map[string]struct{}) ([]InteractionField, error) {
	keySet := make(map[string]struct{}, len(model.KeyFields()))
	for _, field := range model.KeyFields() {
		keySet[field] = struct{}{}
	}
	modelFields := model.Fields()
	autoFillByField := make(map[string]catalog.AutoFillRule)
	for _, rule := range model.AutoFillRules() {
		autoFillByField[rule.Field] = rule
	}
	fields := make([]InteractionField, len(modelFields))
	for index, modelField := range modelFields {
		definitionField, _ := definition.Field(modelField.Name)
		_, projected := projection[modelField.Name]
		_, key := keySet[modelField.Name]
		autoFill, generated := autoFillByField[modelField.Name]
		var defaultOperator catalog.FilterOperator
		if len(modelField.AllowedFilterOperators) > 0 {
			defaultOperator = modelField.AllowedFilterOperators[0]
		}
		fields[index] = InteractionField{
			Name: modelField.Name, DisplayName: definitionField.DisplayName, Description: definitionField.Description,
			Type: modelField.Type, UIControl: modelField.UIControl, Queryable: modelField.Queryable,
			Editable: modelField.Editable, Required: modelField.Required, Sensitive: modelField.Sensitive,
			Projected: projected, KeyField: key, AllowedFilterOperators: slices.Clone(modelField.AllowedFilterOperators),
			DefaultFilterOperator: defaultOperator, DefaultValue: cloneStringPointer(modelField.DefaultValue), DisplayOrder: definitionField.DisplayOrder,
			ValidationRules: modelField.ValidationRules,
		}
		if generated {
			fields[index].AutoFill = &autoFill
		}
		options, err := resolveOptions(current, request, modelField.OptionSource)
		if err != nil {
			return nil, fmt.Errorf("page query: resolve field %q options: %w", modelField.Name, err)
		}
		fields[index].Options = options
	}
	sort.SliceStable(fields, func(left, right int) bool { return fields[left].DisplayOrder < fields[right].DisplayOrder })
	return fields, nil
}

func resolveOptions(current *snapshot.Snapshot, request Request, source *catalog.OptionSourceDefinition) ([]catalog.SelectOptionDefinition, error) {
	if source == nil {
		return nil, nil
	}
	if source.Kind == catalog.OptionSourceStatic {
		return catalog.ResolveSelectOptions(*source, catalog.CollectionDefinition{}, nil)
	}
	if source.Kind != catalog.OptionSourceCollection {
		return nil, fmt.Errorf("unsupported source kind %q", source.Kind)
	}
	definition, exists := current.Definition(source.Collection)
	if !exists {
		return nil, fmt.Errorf("option collection %q is unavailable", source.Collection)
	}
	records, err := overlay.Evaluate(overlay.Query{
		Collection: source.Collection,
		Scope:      overlay.Scope{Region: request.Region, Environment: request.Environment, Stage: request.Stage},
	}, current.Records(source.Collection), current.OverlayRules(source.Collection))
	if err != nil {
		return nil, fmt.Errorf("evaluate option collection: %w", err)
	}
	return catalog.ResolveSelectOptions(*source, definition, records)
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

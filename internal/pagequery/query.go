package pagequery

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
)

type SnapshotProvider interface {
	Current() *snapshot.Snapshot
}

var (
	ErrInvalidArgument            = errors.New("invalid page query argument")
	ErrManagedEnvironmentMismatch = errors.New("page query environment does not match the managed environment")
	ErrSnapshotUnavailable        = errors.New("page query snapshot is unavailable")
	ErrNotFound                   = errors.New("page query resource was not found")
)

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
	Conditions    []FilterCondition
}

type ScalarValue struct {
	Type      catalog.FieldType
	Canonical string
}

type FilterCondition struct {
	Field    string
	Operator catalog.FilterOperator
	Value    *ScalarValue
	Lower    *ScalarValue
	Upper    *ScalarValue
	Set      []ScalarValue
}

type compiledCondition struct {
	field    catalog.ModelField
	operator catalog.FilterOperator
	value    string
	lower    *string
	upper    *string
	set      map[string]struct{}
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

type Querier struct {
	snapshots          SnapshotProvider
	managedEnvironment string
}

func New(snapshots SnapshotProvider, managedEnvironment string) *Querier {
	return &Querier{snapshots: snapshots, managedEnvironment: strings.TrimSpace(managedEnvironment)}
}

func (querier *Querier) Query(request Request) (Result, error) {
	if querier == nil || querier.snapshots == nil || querier.managedEnvironment == "" {
		return Result{}, errors.New("page query: snapshot provider and managed environment are required")
	}
	request.ModelCode = strings.TrimSpace(request.ModelCode)
	request.Region = strings.TrimSpace(request.Region)
	request.Environment = strings.TrimSpace(request.Environment)
	request.Stage = strings.TrimSpace(request.Stage)
	if request.ModelCode == "" || request.Region == "" || request.Environment == "" {
		return Result{}, fmt.Errorf("%w: model, region, and environment are required", ErrInvalidArgument)
	}
	if request.Environment != querier.managedEnvironment {
		return Result{}, fmt.Errorf("%w: got %q, want %q", ErrManagedEnvironmentMismatch, request.Environment, querier.managedEnvironment)
	}
	if request.PreviewBucket != nil && (*request.PreviewBucket < 0 || *request.PreviewBucket > 99) {
		return Result{}, fmt.Errorf("%w: preview bucket must be between 0 and 99", ErrInvalidArgument)
	}
	if request.Type != TypeAll && request.Type != TypeOnlyData {
		return Result{}, fmt.Errorf("%w: unsupported query type %q", ErrInvalidArgument, request.Type)
	}
	current := querier.snapshots.Current()
	if current == nil {
		return Result{}, fmt.Errorf("%w", ErrSnapshotUnavailable)
	}
	if current.Environment() != querier.managedEnvironment {
		return Result{}, fmt.Errorf("%w: managed environment %q is not loaded", ErrManagedEnvironmentMismatch, querier.managedEnvironment)
	}
	model, exists := current.Model(request.ModelCode)
	if !exists {
		return Result{}, fmt.Errorf("%w: model %q is not loaded", ErrNotFound, request.ModelCode)
	}
	definition, exists := current.Definition(model.Collection())
	if !exists {
		return Result{}, fmt.Errorf("%w: model collection %q is not loaded", ErrNotFound, model.Collection())
	}
	collectionRevision, exists := current.CollectionVersion(model.Collection())
	if !exists {
		return Result{}, errors.New("page query: collection version is not loaded")
	}
	conditions, err := compileConditions(current, request, model)
	if err != nil {
		return Result{}, fmt.Errorf("%w: conditions: %v", ErrInvalidArgument, err)
	}

	pageNumber, pageSize, err := normalizePage(request.Page, model.DefaultPageSize(), model.MaxPageSize())
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
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
	if len(conditions) > 0 {
		filtered := make([]catalog.ConfigurationRecord, 0, len(records))
		for _, record := range records {
			if matchesConditions(record, conditions) {
				filtered = append(filtered, record)
			}
		}
		records = filtered
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
	if total > 0 && start >= total {
		pageNumber = int32(totalPages)
		start = int64(pageNumber-1) * int64(pageSize)
		end = start + int64(pageSize)
	}
	if total > 0 {
		if end > total {
			end = total
		}
		rows = rows[start:end]
	} else {
		rows = []Row{}
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

func compileConditions(current *snapshot.Snapshot, request Request, model catalog.CompiledModel) ([]compiledCondition, error) {
	if len(request.Conditions) > 20 {
		return nil, errors.New("at most 20 conditions are allowed")
	}
	fields := make(map[string]catalog.ModelField)
	for _, field := range model.Fields() {
		fields[field.Name] = field
	}
	compiled := make([]compiledCondition, len(request.Conditions))
	optionCache := make(map[string]map[string]bool)
	for index, condition := range request.Conditions {
		field, exists := fields[strings.TrimSpace(condition.Field)]
		if !exists || !field.Queryable || field.Sensitive {
			return nil, fmt.Errorf("condition %d field %q is not queryable", index, condition.Field)
		}
		if !slices.Contains(field.AllowedFilterOperators, condition.Operator) {
			return nil, fmt.Errorf("condition %d operator %q is not allowed for field %q", index, condition.Operator, field.Name)
		}
		result := compiledCondition{field: field, operator: condition.Operator}
		canonical := func(value *ScalarValue) (string, error) {
			if value == nil {
				return "", errors.New("scalar is required")
			}
			if value.Type != "" && value.Type != field.Type {
				return "", fmt.Errorf("scalar type %q does not match %q", value.Type, field.Type)
			}
			return catalog.CanonicalizeScalar(field.Type, value.Canonical)
		}
		switch condition.Operator {
		case catalog.FilterExact, catalog.FilterContains:
			if condition.Value == nil || condition.Lower != nil || condition.Upper != nil || len(condition.Set) != 0 {
				return nil, fmt.Errorf("condition %d requires only value", index)
			}
			value, err := canonical(condition.Value)
			if err != nil {
				return nil, fmt.Errorf("condition %d value: %w", index, err)
			}
			result.value = value
		case catalog.FilterClosedRange, catalog.FilterOpenRange:
			if condition.Value != nil || len(condition.Set) != 0 || (condition.Lower == nil && condition.Upper == nil) {
				return nil, fmt.Errorf("condition %d requires lower and/or upper only", index)
			}
			if condition.Lower != nil {
				value, err := canonical(condition.Lower)
				if err != nil {
					return nil, fmt.Errorf("condition %d lower: %w", index, err)
				}
				result.lower = &value
			}
			if condition.Upper != nil {
				value, err := canonical(condition.Upper)
				if err != nil {
					return nil, fmt.Errorf("condition %d upper: %w", index, err)
				}
				result.upper = &value
			}
			if result.lower != nil && result.upper != nil && compareScalar(field.Type, *result.lower, *result.upper) > 0 {
				return nil, fmt.Errorf("condition %d lower exceeds upper", index)
			}
		case catalog.FilterIn, catalog.FilterNotIn:
			if condition.Value != nil || condition.Lower != nil || condition.Upper != nil || len(condition.Set) == 0 || len(condition.Set) > 100 {
				return nil, fmt.Errorf("condition %d requires a set with 1..100 values", index)
			}
			result.set = make(map[string]struct{}, len(condition.Set))
			for valueIndex := range condition.Set {
				value, err := canonical(&condition.Set[valueIndex])
				if err != nil {
					return nil, fmt.Errorf("condition %d set value %d: %w", index, valueIndex, err)
				}
				if _, duplicate := result.set[value]; duplicate {
					return nil, fmt.Errorf("condition %d repeats set value %q", index, value)
				}
				result.set[value] = struct{}{}
			}
		default:
			return nil, fmt.Errorf("condition %d operator %q is unsupported", index, condition.Operator)
		}
		if field.OptionSource != nil {
			allowed, cached := optionCache[field.Name]
			if !cached {
				options, err := resolveOptions(current, request, field.OptionSource)
				if err != nil {
					return nil, fmt.Errorf("condition %d options: %w", index, err)
				}
				allowed = make(map[string]bool, len(options))
				for _, option := range options {
					allowed[option.Code] = !option.Disabled
				}
				optionCache[field.Name] = allowed
			}
			for _, value := range conditionValues(result) {
				if !allowed[value] {
					return nil, fmt.Errorf("condition %d selection %q is missing or disabled", index, value)
				}
			}
		}
		compiled[index] = result
	}
	return compiled, nil
}

func conditionValues(condition compiledCondition) []string {
	if condition.set != nil {
		values := make([]string, 0, len(condition.set))
		for value := range condition.set {
			values = append(values, value)
		}
		return values
	}
	if condition.lower != nil || condition.upper != nil {
		values := make([]string, 0, 2)
		if condition.lower != nil {
			values = append(values, *condition.lower)
		}
		if condition.upper != nil {
			values = append(values, *condition.upper)
		}
		return values
	}
	return []string{condition.value}
}

func matchesConditions(record catalog.ConfigurationRecord, conditions []compiledCondition) bool {
	for _, condition := range conditions {
		value, present := record.Data[condition.field.Name]
		if !present {
			return false
		}
		switch condition.operator {
		case catalog.FilterExact:
			if value != condition.value {
				return false
			}
		case catalog.FilterContains:
			if !strings.Contains(value, condition.value) {
				return false
			}
		case catalog.FilterClosedRange, catalog.FilterOpenRange:
			if condition.lower != nil {
				comparison := compareScalar(condition.field.Type, value, *condition.lower)
				if comparison < 0 || (condition.operator == catalog.FilterOpenRange && comparison == 0) {
					return false
				}
			}
			if condition.upper != nil {
				comparison := compareScalar(condition.field.Type, value, *condition.upper)
				if comparison > 0 || (condition.operator == catalog.FilterOpenRange && comparison == 0) {
					return false
				}
			}
		case catalog.FilterIn:
			if _, exists := condition.set[value]; !exists {
				return false
			}
		case catalog.FilterNotIn:
			if _, exists := condition.set[value]; exists {
				return false
			}
		}
	}
	return true
}

func compareScalar(fieldType catalog.FieldType, left, right string) int {
	switch fieldType {
	case catalog.FieldTypeInt64:
		leftValue, _ := strconv.ParseInt(left, 10, 64)
		rightValue, _ := strconv.ParseInt(right, 10, 64)
		if leftValue < rightValue {
			return -1
		}
		if leftValue > rightValue {
			return 1
		}
	case catalog.FieldTypeFloat64:
		leftValue, _ := strconv.ParseFloat(left, 64)
		rightValue, _ := strconv.ParseFloat(right, 64)
		if leftValue < rightValue {
			return -1
		}
		if leftValue > rightValue {
			return 1
		}
	case catalog.FieldTypeTimestamp:
		leftValue, _ := time.Parse(time.RFC3339Nano, left)
		rightValue, _ := time.Parse(time.RFC3339Nano, right)
		return leftValue.Compare(rightValue)
	default:
		return strings.Compare(left, right)
	}
	return 0
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

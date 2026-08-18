package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

type UIControlType string

const (
	UIControlInput    UIControlType = "INPUT"
	UIControlSelect   UIControlType = "SELECT"
	UIControlTime     UIControlType = "TIME"
	UIControlNumber   UIControlType = "NUMBER"
	UIControlBoolean  UIControlType = "BOOLEAN"
	UIControlTextarea UIControlType = "TEXTAREA"
	UIControlJSON     UIControlType = "JSON"
)

type FilterOperator string

const (
	FilterExact       FilterOperator = "EXACT"
	FilterContains    FilterOperator = "CONTAINS"
	FilterClosedRange FilterOperator = "CLOSED_RANGE"
	FilterOpenRange   FilterOperator = "OPEN_RANGE"
	FilterIn          FilterOperator = "IN"
	FilterNotIn       FilterOperator = "NOT_IN"
)

// ModelField adds interaction behavior to a Collection field while repeating
// its data semantics for transport compatibility.
type ModelField struct {
	Name                   string
	Type                   FieldType
	Required               bool
	Sensitive              bool
	Editable               bool
	Queryable              bool
	DefaultValue           *string
	UIControl              UIControlType
	AllowedFilterOperators []FilterOperator
}

type ReleaseTypeDefinition struct {
	Code                  string
	Name                  string
	TemplateCode          string
	Enabled               bool
	Available             bool
	UnavailableReasonCode string
}

// ModelSpec is untrusted model input to CompileModel.
type ModelSpec struct {
	Code             string
	Name             string
	Collection       string
	Fields           []ModelField
	ProjectionFields []string
	KeyFields        []string
	DefaultPageSize  int32
	MaxPageSize      int32
	ReleaseTypes     []ReleaseTypeDefinition
	ConfigRevision   ConfigRevision
}

// CompiledModel is safe to publish in a snapshot.
type CompiledModel struct {
	code             string
	name             string
	collection       string
	fields           []ModelField
	projectionFields []string
	keyFields        []string
	defaultPageSize  int32
	maxPageSize      int32
	releaseTypes     []ReleaseTypeDefinition
	configRevision   ConfigRevision
}

func CompileModel(collection CollectionDefinition, spec ModelSpec) (CompiledModel, error) {
	spec.Code = strings.TrimSpace(spec.Code)
	if !identifierPattern.MatchString(spec.Code) {
		return CompiledModel{}, errors.New("compile model: code must be an ASCII identifier")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return CompiledModel{}, errors.New("compile model: name is required")
	}
	if spec.Collection != collection.Name() {
		return CompiledModel{}, fmt.Errorf("compile model: collection %q does not match %q", spec.Collection, collection.Name())
	}
	if len(spec.Fields) == 0 {
		return CompiledModel{}, errors.New("compile model: at least one field is required")
	}
	if spec.DefaultPageSize <= 0 || spec.MaxPageSize < spec.DefaultPageSize {
		return CompiledModel{}, errors.New("compile model: page sizes are invalid")
	}
	if spec.ConfigRevision == 0 {
		return CompiledModel{}, errors.New("compile model: config revision must be positive")
	}

	collectionFields := make(map[string]FieldDefinition, len(collection.fields))
	for _, field := range collection.fields {
		collectionFields[field.Name] = field
	}
	fields := make([]ModelField, len(spec.Fields))
	seen := make(map[string]struct{}, len(spec.Fields))
	for index, field := range spec.Fields {
		definition, exists := collectionFields[field.Name]
		if !exists {
			return CompiledModel{}, fmt.Errorf("compile model: field %q does not exist in collection", field.Name)
		}
		if _, duplicate := seen[field.Name]; duplicate {
			return CompiledModel{}, fmt.Errorf("compile model: duplicate field %q", field.Name)
		}
		if field.Type != definition.Type || field.Required != definition.Required || field.Sensitive != definition.Sensitive || !sameOptionalString(field.DefaultValue, definition.DefaultValue) {
			return CompiledModel{}, fmt.Errorf("compile model: field %q data semantics differ from collection", field.Name)
		}
		if !validUIControl(field.UIControl) {
			return CompiledModel{}, fmt.Errorf("compile model: field %q has invalid UI control %q", field.Name, field.UIControl)
		}
		if field.Queryable && len(field.AllowedFilterOperators) == 0 {
			return CompiledModel{}, fmt.Errorf("compile model: queryable field %q needs allowed filter operators", field.Name)
		}
		if !field.Queryable && len(field.AllowedFilterOperators) != 0 {
			return CompiledModel{}, fmt.Errorf("compile model: non-queryable field %q has filter operators", field.Name)
		}
		operators := make(map[FilterOperator]struct{}, len(field.AllowedFilterOperators))
		for _, operator := range field.AllowedFilterOperators {
			if !validFilterOperator(field.Type, operator) {
				return CompiledModel{}, fmt.Errorf("compile model: field %q cannot use filter operator %q", field.Name, operator)
			}
			if _, duplicate := operators[operator]; duplicate {
				return CompiledModel{}, fmt.Errorf("compile model: field %q repeats filter operator %q", field.Name, operator)
			}
			operators[operator] = struct{}{}
		}
		if field.DefaultValue != nil {
			field.DefaultValue = stringPointer(*field.DefaultValue)
		}
		field.AllowedFilterOperators = slices.Clone(field.AllowedFilterOperators)
		fields[index] = field
		seen[field.Name] = struct{}{}
	}

	if len(spec.ProjectionFields) == 0 {
		return CompiledModel{}, errors.New("compile model: projection fields are required")
	}
	projection := slices.Clone(spec.ProjectionFields)
	projectionSeen := make(map[string]struct{}, len(projection))
	for _, name := range projection {
		definition, exists := collectionFields[name]
		if !exists {
			return CompiledModel{}, fmt.Errorf("compile model: projection field %q does not exist", name)
		}
		if _, exposed := seen[name]; !exposed {
			return CompiledModel{}, fmt.Errorf("compile model: projection field %q has no model field", name)
		}
		if definition.Sensitive {
			return CompiledModel{}, fmt.Errorf("compile model: sensitive field %q cannot be projected", name)
		}
		if _, duplicate := projectionSeen[name]; duplicate {
			return CompiledModel{}, fmt.Errorf("compile model: duplicate projection field %q", name)
		}
		projectionSeen[name] = struct{}{}
	}
	if !slices.Equal(spec.KeyFields, collection.keyFields) {
		return CompiledModel{}, errors.New("compile model: release key fields must match collection key fields in order")
	}
	releaseTypes, err := compileReleaseTypes(spec.ReleaseTypes)
	if err != nil {
		return CompiledModel{}, err
	}

	return CompiledModel{
		code:             spec.Code,
		name:             spec.Name,
		collection:       collection.Name(),
		fields:           fields,
		projectionFields: projection,
		keyFields:        slices.Clone(spec.KeyFields),
		defaultPageSize:  spec.DefaultPageSize,
		maxPageSize:      spec.MaxPageSize,
		releaseTypes:     releaseTypes,
		configRevision:   spec.ConfigRevision,
	}, nil
}

func (model CompiledModel) Code() string { return model.code }

func (model CompiledModel) Name() string { return model.name }

func (model CompiledModel) Collection() string { return model.collection }

func (model CompiledModel) Fields() []ModelField {
	fields := make([]ModelField, len(model.fields))
	for index, field := range model.fields {
		if field.DefaultValue != nil {
			field.DefaultValue = stringPointer(*field.DefaultValue)
		}
		field.AllowedFilterOperators = slices.Clone(field.AllowedFilterOperators)
		fields[index] = field
	}
	return fields
}

func (model CompiledModel) ProjectionFields() []string { return slices.Clone(model.projectionFields) }

func (model CompiledModel) KeyFields() []string { return slices.Clone(model.keyFields) }

func (model CompiledModel) DefaultPageSize() int32 { return model.defaultPageSize }

func (model CompiledModel) MaxPageSize() int32 { return model.maxPageSize }

func (model CompiledModel) ReleaseTypes() []ReleaseTypeDefinition {
	return slices.Clone(model.releaseTypes)
}

func (model CompiledModel) ConfigRevision() ConfigRevision { return model.configRevision }

func compileReleaseTypes(source []ReleaseTypeDefinition) ([]ReleaseTypeDefinition, error) {
	compiled := make([]ReleaseTypeDefinition, len(source))
	seen := make(map[string]struct{}, len(source))
	for index, definition := range source {
		definition.Code = strings.TrimSpace(definition.Code)
		definition.Name = strings.TrimSpace(definition.Name)
		definition.TemplateCode = strings.TrimSpace(definition.TemplateCode)
		definition.UnavailableReasonCode = strings.TrimSpace(definition.UnavailableReasonCode)
		if !identifierPattern.MatchString(definition.Code) || !identifierPattern.MatchString(definition.TemplateCode) || definition.Name == "" {
			return nil, fmt.Errorf("compile model: release type %d has invalid identity", index)
		}
		if _, duplicate := seen[definition.Code]; duplicate {
			return nil, fmt.Errorf("compile model: duplicate release type %q", definition.Code)
		}
		if definition.Available && !definition.Enabled {
			return nil, fmt.Errorf("compile model: disabled release type %q cannot be available", definition.Code)
		}
		if definition.Available && definition.UnavailableReasonCode != "" {
			return nil, fmt.Errorf("compile model: available release type %q has an unavailable reason", definition.Code)
		}
		compiled[index] = definition
		seen[definition.Code] = struct{}{}
	}
	return compiled, nil
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func validUIControl(control UIControlType) bool {
	switch control {
	case UIControlInput, UIControlSelect, UIControlTime, UIControlNumber, UIControlBoolean, UIControlTextarea, UIControlJSON:
		return true
	default:
		return false
	}
}

func validFilterOperator(fieldType FieldType, operator FilterOperator) bool {
	switch operator {
	case FilterExact, FilterIn, FilterNotIn:
		return true
	case FilterContains:
		return fieldType == FieldTypeString
	case FilterClosedRange, FilterOpenRange:
		return fieldType == FieldTypeInt64 || fieldType == FieldTypeFloat64 || fieldType == FieldTypeTimestamp
	default:
		return false
	}
}

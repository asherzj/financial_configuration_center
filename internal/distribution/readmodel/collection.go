package readmodel

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,190}$`)

// FieldDefinition declares one canonical record field.
type FieldDefinition struct {
	Name            string           `json:"name"`
	DisplayName     string           `json:"displayName"`
	Type            FieldType        `json:"type"`
	Required        bool             `json:"required"`
	Sensitive       bool             `json:"sensitive"`
	DefaultValue    *string          `json:"defaultValue,omitempty"`
	Description     string           `json:"description"`
	DisplayOrder    int32            `json:"displayOrder"`
	ValidationRules []ValidationRule `json:"validationRules"`
}

// CollectionSpec is untrusted input to CompileCollection.
type CollectionSpec struct {
	Name               string
	Description        string
	Fields             []FieldDefinition
	KeyFields          []string
	SDKDeliveryEnabled bool
	SchemaVersion      int64
}

// CollectionDefinition is a compiled data contract. Its slices and pointer
// values are kept private so callers cannot mutate validation semantics.
type CollectionDefinition struct {
	name               string
	description        string
	fields             []FieldDefinition
	fieldsByName       map[string]FieldDefinition
	keyFields          []string
	sdkDeliveryEnabled bool
	schemaVersion      int64
}

func CompileCollection(spec CollectionSpec) (CollectionDefinition, error) {
	spec.Name = strings.TrimSpace(spec.Name)
	if !identifierPattern.MatchString(spec.Name) {
		return CollectionDefinition{}, errors.New("compile collection: name must be an ASCII identifier")
	}
	if len(spec.Fields) == 0 {
		return CollectionDefinition{}, errors.New("compile collection: at least one field is required")
	}
	if len(spec.KeyFields) == 0 {
		return CollectionDefinition{}, errors.New("compile collection: at least one key field is required")
	}
	if spec.SchemaVersion <= 0 {
		return CollectionDefinition{}, errors.New("compile collection: schema version must be positive")
	}

	fields := make([]FieldDefinition, len(spec.Fields))
	fieldsByName := make(map[string]FieldDefinition, len(spec.Fields))
	displayOrders := make(map[int32]struct{}, len(spec.Fields))
	for index, field := range spec.Fields {
		field.Name = strings.TrimSpace(field.Name)
		if !identifierPattern.MatchString(field.Name) {
			return CollectionDefinition{}, fmt.Errorf("compile collection: field %d has an invalid name", index)
		}
		if strings.TrimSpace(field.DisplayName) == "" {
			return CollectionDefinition{}, fmt.Errorf("compile collection: field %q display name is required", field.Name)
		}
		if field.DisplayOrder < 0 {
			return CollectionDefinition{}, fmt.Errorf("compile collection: field %q display order must be non-negative", field.Name)
		}
		if _, exists := fieldsByName[field.Name]; exists {
			return CollectionDefinition{}, fmt.Errorf("compile collection: duplicate field %q", field.Name)
		}
		if _, exists := displayOrders[field.DisplayOrder]; exists {
			return CollectionDefinition{}, fmt.Errorf("compile collection: duplicate display order %d", field.DisplayOrder)
		}
		if _, err := CanonicalizeScalar(field.Type, zeroFixture(field.Type)); err != nil {
			return CollectionDefinition{}, fmt.Errorf("compile collection: field %q: %w", field.Name, err)
		}
		if field.DefaultValue != nil {
			canonical, err := CanonicalizeScalar(field.Type, *field.DefaultValue)
			if err != nil {
				return CollectionDefinition{}, fmt.Errorf("compile collection: field %q default: %w", field.Name, err)
			}
			field.DefaultValue = stringPointer(canonical)
		}
		compiledRules, err := compileValidationRules(field.Type, field.Required, field.ValidationRules)
		if err != nil {
			return CollectionDefinition{}, fmt.Errorf("compile collection: field %q: %w", field.Name, err)
		}
		field.ValidationRules = compiledRules
		if field.DefaultValue != nil {
			if err := validateCanonicalFieldValue(field, *field.DefaultValue); err != nil {
				return CollectionDefinition{}, fmt.Errorf("compile collection: field %q default: %w", field.Name, err)
			}
		}
		fields[index] = cloneField(field)
		fieldsByName[field.Name] = cloneField(field)
		displayOrders[field.DisplayOrder] = struct{}{}
	}

	keyFields := slices.Clone(spec.KeyFields)
	seenKeys := make(map[string]struct{}, len(keyFields))
	for index := range keyFields {
		keyFields[index] = strings.TrimSpace(keyFields[index])
		field, exists := fieldsByName[keyFields[index]]
		if !exists {
			return CollectionDefinition{}, fmt.Errorf("compile collection: key field %q does not exist", keyFields[index])
		}
		if _, duplicate := seenKeys[keyFields[index]]; duplicate {
			return CollectionDefinition{}, fmt.Errorf("compile collection: duplicate key field %q", keyFields[index])
		}
		if field.Sensitive {
			return CollectionDefinition{}, fmt.Errorf("compile collection: sensitive field %q cannot be a key", keyFields[index])
		}
		seenKeys[keyFields[index]] = struct{}{}
	}

	return CollectionDefinition{
		name:               spec.Name,
		description:        spec.Description,
		fields:             fields,
		fieldsByName:       fieldsByName,
		keyFields:          keyFields,
		sdkDeliveryEnabled: spec.SDKDeliveryEnabled,
		schemaVersion:      spec.SchemaVersion,
	}, nil
}

func (definition CollectionDefinition) Name() string { return definition.name }

func (definition CollectionDefinition) Description() string { return definition.description }

func (definition CollectionDefinition) KeyFields() []string {
	return slices.Clone(definition.keyFields)
}

func (definition CollectionDefinition) Fields() []FieldDefinition {
	fields := make([]FieldDefinition, len(definition.fields))
	for index, field := range definition.fields {
		fields[index] = cloneField(field)
	}
	return fields
}

func (definition CollectionDefinition) Field(name string) (FieldDefinition, bool) {
	field, exists := definition.fieldsByName[name]
	if !exists {
		return FieldDefinition{}, false
	}
	return cloneField(field), true
}

func (definition CollectionDefinition) SDKDeliveryEnabled() bool {
	return definition.sdkDeliveryEnabled
}

func (definition CollectionDefinition) SchemaVersion() int64 { return definition.schemaVersion }

// CanonicalRecordKey derives identity from a partial record. Key fields are
// never sensitive, so callers can identify a masked admin row without
// supplying protected values.
func (definition CollectionDefinition) CanonicalRecordKey(input map[string]string) (string, error) {
	data := make(map[string]string, len(definition.keyFields))
	for _, name := range definition.keyFields {
		value, exists := input[name]
		if !exists {
			return "", fmt.Errorf("record key: field %q is missing", name)
		}
		field := definition.fieldsByName[name]
		canonical, err := CanonicalizeScalar(field.Type, value)
		if err != nil {
			return "", fmt.Errorf("record key: field %q: %w", name, err)
		}
		data[name] = canonical
	}
	return EncodeKey(definition.keyFields, data)
}

// NewRecord validates and canonicalizes a complete record for one Environment.
func (definition CollectionDefinition) NewRecord(environment string, input map[string]string) (ConfigurationRecord, error) {
	environment = strings.TrimSpace(environment)
	if environment == "" || len(environment) > 64 {
		return ConfigurationRecord{}, errors.New("new record: environment is required and must be at most 64 bytes")
	}
	data := make(map[string]string, len(definition.fields))
	for name := range input {
		if _, exists := definition.fieldsByName[name]; !exists {
			return ConfigurationRecord{}, fmt.Errorf("new record: unknown field %q", name)
		}
	}
	for _, field := range definition.fields {
		value, exists := input[field.Name]
		if !exists && field.DefaultValue != nil {
			value, exists = *field.DefaultValue, true
		}
		if !exists {
			if field.Required {
				return ConfigurationRecord{}, fmt.Errorf("new record: required field %q is missing", field.Name)
			}
			continue
		}
		canonical, err := CanonicalizeScalar(field.Type, value)
		if err != nil {
			return ConfigurationRecord{}, fmt.Errorf("new record: field %q: %w", field.Name, err)
		}
		if err := validateCanonicalFieldValue(field, canonical); err != nil {
			return ConfigurationRecord{}, fmt.Errorf("new record: field %q: %w", field.Name, err)
		}
		data[field.Name] = canonical
	}
	recordKey, err := EncodeKey(definition.keyFields, data)
	if err != nil {
		return ConfigurationRecord{}, fmt.Errorf("new record: %w", err)
	}
	return ConfigurationRecord{
		Collection:  definition.name,
		Environment: environment,
		RecordKey:   recordKey,
		Data:        data,
	}, nil
}

func cloneField(field FieldDefinition) FieldDefinition {
	if field.DefaultValue != nil {
		field.DefaultValue = stringPointer(*field.DefaultValue)
	}
	field.ValidationRules = cloneValidationRules(field.ValidationRules)
	return field
}

func stringPointer(value string) *string { return &value }

func zeroFixture(fieldType FieldType) string {
	switch fieldType {
	case FieldTypeString:
		return ""
	case FieldTypeInt64, FieldTypeFloat64:
		return "0"
	case FieldTypeBool:
		return "false"
	case FieldTypeTimestamp:
		return "1970-01-01T00:00:00Z"
	case FieldTypeJSON:
		return "null"
	default:
		return ""
	}
}

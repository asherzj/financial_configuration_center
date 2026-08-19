package readmodel

import (
	"fmt"
	"slices"
	"sort"
)

// ResolveSelectOptions is the single interpretation of a compiled option
// source. COLLECTION records must already represent one effective scope.
func ResolveSelectOptions(source OptionSourceDefinition, definition CollectionDefinition, records []ConfigurationRecord) ([]SelectOptionDefinition, error) {
	switch source.Kind {
	case OptionSourceStatic:
		return slices.Clone(source.StaticOptions), nil
	case OptionSourceCollection:
		return resolveCollectionOptions(source, definition, records)
	default:
		return nil, fmt.Errorf("unsupported option source kind %q", source.Kind)
	}
}

func resolveCollectionOptions(source OptionSourceDefinition, definition CollectionDefinition, records []ConfigurationRecord) ([]SelectOptionDefinition, error) {
	valueField, exists := definition.Field(source.ValueField)
	if !exists || valueField.Sensitive {
		return nil, fmt.Errorf("option value field %q is missing or sensitive", source.ValueField)
	}
	labelField, exists := definition.Field(source.LabelField)
	if !exists || labelField.Sensitive {
		return nil, fmt.Errorf("option label field %q is missing or sensitive", source.LabelField)
	}

	filters := make([]OptionFixedFilter, len(source.FixedFilters))
	for index, filter := range source.FixedFilters {
		field, exists := definition.Field(filter.Field)
		if !exists || field.Sensitive {
			return nil, fmt.Errorf("option filter field %q is missing or sensitive", filter.Field)
		}
		canonical, err := CanonicalizeScalar(field.Type, filter.Value)
		if err != nil {
			return nil, fmt.Errorf("option filter field %q: %w", filter.Field, err)
		}
		filters[index] = OptionFixedFilter{Field: filter.Field, Value: canonical}
	}

	labels := make(map[string]string)
	for _, record := range records {
		matches := true
		for _, filter := range filters {
			if record.Data[filter.Field] != filter.Value {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		code, codePresent := record.Data[source.ValueField]
		label, labelPresent := record.Data[source.LabelField]
		if !codePresent || !labelPresent {
			return nil, fmt.Errorf("option record %q lacks value or label", record.RecordKey)
		}
		if previous, duplicate := labels[code]; duplicate {
			if previous != label {
				return nil, fmt.Errorf("option code %q has conflicting labels", code)
			}
			continue
		}
		labels[code] = label
	}
	if len(labels) > int(source.Limit) || len(labels) > 1000 {
		return nil, fmt.Errorf("option count %d exceeds limit %d", len(labels), source.Limit)
	}
	options := make([]SelectOptionDefinition, 0, len(labels))
	for code, label := range labels {
		options = append(options, SelectOptionDefinition{Code: code, Label: label})
	}
	sort.Slice(options, func(left, right int) bool { return options[left].Code < options[right].Code })
	return options, nil
}

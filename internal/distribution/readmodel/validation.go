package readmodel

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type ValidationRuleKind string

const (
	ValidationRequired  ValidationRuleKind = "REQUIRED"
	ValidationEnum      ValidationRuleKind = "ENUM"
	ValidationRegex     ValidationRuleKind = "REGEX"
	ValidationMin       ValidationRuleKind = "MIN"
	ValidationMax       ValidationRuleKind = "MAX"
	ValidationMinLength ValidationRuleKind = "MIN_LENGTH"
	ValidationMaxLength ValidationRuleKind = "MAX_LENGTH"
)

type ValidationRule struct {
	Kind    ValidationRuleKind `json:"kind"`
	Params  map[string]string  `json:"params"`
	Message string             `json:"message"`
}

func compileValidationRules(fieldType FieldType, required bool, source []ValidationRule) ([]ValidationRule, error) {
	compiled := make([]ValidationRule, len(source))
	seen := make(map[ValidationRuleKind]struct{}, len(source))
	for index, rule := range source {
		rule.Message = strings.TrimSpace(rule.Message)
		if rule.Message == "" {
			return nil, fmt.Errorf("validation rule %q requires a message", rule.Kind)
		}
		if _, duplicate := seen[rule.Kind]; duplicate {
			return nil, fmt.Errorf("validation rule %q is duplicated", rule.Kind)
		}
		seen[rule.Kind] = struct{}{}
		params := cloneStringMap(rule.Params)
		switch rule.Kind {
		case ValidationRequired:
			if !required || len(params) != 0 {
				return nil, errors.New("REQUIRED rule requires a required field and no params")
			}
		case ValidationEnum:
			if len(params) != 1 {
				return nil, errors.New("ENUM rule requires only the values param")
			}
			var values []string
			if err := json.Unmarshal([]byte(params["values"]), &values); err != nil || len(values) == 0 || len(values) > 1000 {
				return nil, errors.New("ENUM values must be a JSON string array with 1..1000 elements")
			}
			canonical := make([]string, len(values))
			valueSet := make(map[string]struct{}, len(values))
			for valueIndex, value := range values {
				compiledValue, err := CanonicalizeScalar(fieldType, value)
				if err != nil {
					return nil, fmt.Errorf("ENUM value %d: %w", valueIndex, err)
				}
				if _, duplicate := valueSet[compiledValue]; duplicate {
					return nil, fmt.Errorf("ENUM value %q is duplicated after canonicalization", compiledValue)
				}
				valueSet[compiledValue] = struct{}{}
				canonical[valueIndex] = compiledValue
			}
			sort.Strings(canonical)
			encoded, _ := json.Marshal(canonical)
			params["values"] = string(encoded)
		case ValidationRegex:
			pattern, only := onlyParam(params, "pattern")
			if !only || fieldType != FieldTypeString || len(pattern) == 0 || len(pattern) > 1024 {
				return nil, errors.New("REGEX requires only a 1..1024 byte pattern on STRING")
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return nil, fmt.Errorf("REGEX pattern: %w", err)
			}
		case ValidationMin, ValidationMax:
			value, only := onlyParam(params, "value")
			if !only || (fieldType != FieldTypeInt64 && fieldType != FieldTypeFloat64 && fieldType != FieldTypeTimestamp) {
				return nil, fmt.Errorf("%s requires only value on INT64, FLOAT64, or TIMESTAMP", rule.Kind)
			}
			canonical, err := CanonicalizeScalar(fieldType, value)
			if err != nil {
				return nil, fmt.Errorf("%s value: %w", rule.Kind, err)
			}
			params["value"] = canonical
		case ValidationMinLength, ValidationMaxLength:
			value, only := onlyParam(params, "value")
			length, err := strconv.Atoi(value)
			if !only || err != nil || length < 0 || fieldType != FieldTypeString {
				return nil, fmt.Errorf("%s requires one non-negative integer value on STRING", rule.Kind)
			}
			params["value"] = strconv.Itoa(length)
		default:
			return nil, fmt.Errorf("validation rule kind %q is invalid", rule.Kind)
		}
		compiled[index] = ValidationRule{Kind: rule.Kind, Params: params, Message: rule.Message}
	}
	return compiled, nil
}

func validateCanonicalFieldValue(field FieldDefinition, value string) error {
	for _, rule := range field.ValidationRules {
		valid := true
		switch rule.Kind {
		case ValidationRequired:
			valid = value != ""
		case ValidationEnum:
			var values []string
			_ = json.Unmarshal([]byte(rule.Params["values"]), &values)
			valid = slices.Contains(values, value)
		case ValidationRegex:
			valid, _ = regexp.MatchString(rule.Params["pattern"], value)
		case ValidationMin:
			valid = compareCanonical(field.Type, value, rule.Params["value"]) >= 0
		case ValidationMax:
			valid = compareCanonical(field.Type, value, rule.Params["value"]) <= 0
		case ValidationMinLength:
			minimum, _ := strconv.Atoi(rule.Params["value"])
			valid = utf8.RuneCountInString(value) >= minimum
		case ValidationMaxLength:
			maximum, _ := strconv.Atoi(rule.Params["value"])
			valid = utf8.RuneCountInString(value) <= maximum
		}
		if !valid {
			return errors.New(rule.Message)
		}
	}
	return nil
}

func compareCanonical(fieldType FieldType, left, right string) int {
	switch fieldType {
	case FieldTypeInt64:
		leftValue, _ := strconv.ParseInt(left, 10, 64)
		rightValue, _ := strconv.ParseInt(right, 10, 64)
		return compare(leftValue, rightValue)
	case FieldTypeFloat64:
		leftValue, _ := strconv.ParseFloat(left, 64)
		rightValue, _ := strconv.ParseFloat(right, 64)
		return compare(leftValue, rightValue)
	case FieldTypeTimestamp:
		leftValue, _ := time.Parse(time.RFC3339Nano, left)
		rightValue, _ := time.Parse(time.RFC3339Nano, right)
		return leftValue.Compare(rightValue)
	default:
		return strings.Compare(left, right)
	}
}

func compare[T ~int64 | ~float64](left, right T) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func sameValidationRules(left, right []ValidationRule) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Kind != right[index].Kind || left[index].Message != right[index].Message || !stringMapsEqual(left[index].Params, right[index].Params) {
			return false
		}
	}
	return true
}

func cloneValidationRules(source []ValidationRule) []ValidationRule {
	result := make([]ValidationRule, len(source))
	for index, rule := range source {
		rule.Params = cloneStringMap(rule.Params)
		result[index] = rule
	}
	return result
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return map[string]string{}
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func stringMapsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func onlyParam(params map[string]string, name string) (string, bool) {
	value, exists := params[name]
	return value, exists && len(params) == 1
}

package readmodel

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"
)

// FieldType is the closed set of scalar representations accepted by a
// ConfigurationCollection. The zero value is intentionally invalid.
type FieldType string

const (
	FieldTypeString    FieldType = "STRING"
	FieldTypeInt64     FieldType = "INT64"
	FieldTypeFloat64   FieldType = "FLOAT64"
	FieldTypeBool      FieldType = "BOOL"
	FieldTypeTimestamp FieldType = "TIMESTAMP"
	FieldTypeJSON      FieldType = "JSON"
)

// Digest identifies stable serialized configuration content.
type Digest struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

// ConfigRevision is the global monotonic watermark for distribution-visible
// configuration facts. It must never be reused for aggregate or lease CAS.
type ConfigRevision uint64

// ConfigurationRecord is the canonical representation used at the Catalog
// boundary. Data values are canonical strings, including numbers and JSON.
type ConfigurationRecord struct {
	Collection     string
	Environment    string
	RecordKey      string
	Data           map[string]string
	ConfigRevision ConfigRevision
}

// CanonicalizeScalar converts an input value to the one storage and comparison
// representation for its declared type.
func CanonicalizeScalar(fieldType FieldType, input string) (string, error) {
	switch fieldType {
	case FieldTypeString:
		return input, nil
	case FieldTypeInt64:
		value, err := strconv.ParseInt(input, 10, 64)
		if err != nil {
			return "", fmt.Errorf("canonicalize INT64: %w", err)
		}
		return strconv.FormatInt(value, 10), nil
	case FieldTypeFloat64:
		value, err := strconv.ParseFloat(input, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return "", errors.New("canonicalize FLOAT64: value must be finite")
		}
		return strconv.FormatFloat(value, 'g', -1, 64), nil
	case FieldTypeBool:
		value, err := strconv.ParseBool(input)
		if err != nil {
			return "", fmt.Errorf("canonicalize BOOL: %w", err)
		}
		return strconv.FormatBool(value), nil
	case FieldTypeTimestamp:
		value, err := time.Parse(time.RFC3339Nano, input)
		if err != nil {
			return "", fmt.Errorf("canonicalize TIMESTAMP: %w", err)
		}
		return value.UTC().Format(time.RFC3339Nano), nil
	case FieldTypeJSON:
		return canonicalizeJSON(input)
	default:
		return "", fmt.Errorf("canonicalize scalar: unsupported field type %q", fieldType)
	}
}

// EncodeKey uses JSON tuple encoding followed by raw URL-safe base64. Field
// order is significant and missing fields are not equivalent to empty values.
func EncodeKey(fields []string, data map[string]string) (string, error) {
	if len(fields) == 0 {
		return "", errors.New("encode key: at least one field is required")
	}
	values := make([]string, len(fields))
	for index, field := range fields {
		value, ok := data[field]
		if !ok {
			return "", fmt.Errorf("encode key: field %q is missing", field)
		}
		values[index] = value
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode key tuple: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

// ComputeBaseDigest hashes the stable JSON representation of records sorted by
// RecordKey. Duplicate keys are rejected because they would make identity
// ambiguous even if their data happened to match.
func ComputeBaseDigest(records []ConfigurationRecord) (Digest, error) {
	type digestRecord struct {
		key  string
		data map[string]string
	}
	ordered := make([]digestRecord, len(records))
	for index, record := range records {
		if record.RecordKey == "" {
			return Digest{}, errors.New("compute base digest: record key is required")
		}
		ordered[index] = digestRecord{key: record.RecordKey, data: record.Data}
	}
	for left := 1; left < len(ordered); left++ {
		for right := left; right > 0 && ordered[right].key < ordered[right-1].key; right-- {
			ordered[right], ordered[right-1] = ordered[right-1], ordered[right]
		}
	}
	payload := make([]any, len(ordered))
	for index, record := range ordered {
		if index > 0 && record.key == ordered[index-1].key {
			return Digest{}, fmt.Errorf("compute base digest: duplicate record key %q", record.key)
		}
		payload[index] = []any{record.key, record.data}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Digest{}, fmt.Errorf("compute base digest: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return Digest{Algorithm: "SHA-256", Value: hex.EncodeToString(sum[:])}, nil
}

func canonicalizeJSON(input string) (string, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(input))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("canonicalize JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return "", errors.New("canonicalize JSON: multiple values are not allowed")
		}
		return "", fmt.Errorf("canonicalize JSON trailing data: %w", err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("canonicalize JSON: %w", err)
	}
	return string(encoded), nil
}

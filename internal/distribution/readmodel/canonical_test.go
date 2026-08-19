package readmodel_test

import (
	"encoding/base64"
	"testing"

	domain "github.com/asherzj/financial_configuration_center/internal/distribution/readmodel"
)

func TestCanonicalizeScalar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		fieldType domain.FieldType
		input     string
		want      string
	}{
		{name: "string remains byte-for-byte", fieldType: domain.FieldTypeString, input: "  Route-甲  ", want: "  Route-甲  "},
		{name: "int64", fieldType: domain.FieldTypeInt64, input: "+00042", want: "42"},
		{name: "float64", fieldType: domain.FieldTypeFloat64, input: "1.2300", want: "1.23"},
		{name: "bool", fieldType: domain.FieldTypeBool, input: "TRUE", want: "true"},
		{name: "timestamp", fieldType: domain.FieldTypeTimestamp, input: "2026-08-19T11:22:33.123456789+08:00", want: "2026-08-19T03:22:33.123456789Z"},
		{name: "json", fieldType: domain.FieldTypeJSON, input: `{ "z": 1.0, "a": [true, {"b":2,"a":1}], "large": 9007199254740993 }`, want: `{"a":[true,{"a":1,"b":2}],"large":9007199254740993,"z":1.0}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.CanonicalizeScalar(tt.fieldType, tt.input)
			if err != nil {
				t.Fatalf("CanonicalizeScalar: %v", err)
			}
			if got != tt.want {
				t.Fatalf("CanonicalizeScalar(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCanonicalizeScalarRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		fieldType domain.FieldType
		input     string
	}{
		{domain.FieldTypeInt64, "1.1"},
		{domain.FieldTypeFloat64, "NaN"},
		{domain.FieldTypeFloat64, "+Inf"},
		{domain.FieldTypeBool, "yes"},
		{domain.FieldTypeTimestamp, "2026-08-19"},
		{domain.FieldTypeJSON, `{"a":1} trailing`},
		{domain.FieldType("MONEY"), "1"},
	} {
		if got, err := domain.CanonicalizeScalar(tt.fieldType, tt.input); err == nil {
			t.Errorf("CanonicalizeScalar(%q, %q) = %q, want error", tt.fieldType, tt.input, got)
		}
	}
}

func TestRecordKeyUsesCollisionFreeOrderedEncoding(t *testing.T) {
	t.Parallel()

	data := map[string]string{"left": "a,b", "right": "c", "empty": ""}
	key, err := domain.EncodeKey([]string{"left", "right", "empty"}, data)
	if err != nil {
		t.Fatalf("EncodeKey: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	if got, want := string(raw), `["a,b","c",""]`; got != want {
		t.Fatalf("decoded key = %s, want %s", got, want)
	}

	collidingDelimiterKey, err := domain.EncodeKey([]string{"left", "right"}, map[string]string{"left": "a", "right": "b,c"})
	if err != nil {
		t.Fatalf("EncodeKey collision fixture: %v", err)
	}
	if key == collidingDelimiterKey {
		t.Fatal("distinct tuples produced the same key")
	}
	if _, err := domain.EncodeKey([]string{"missing"}, data); err == nil {
		t.Fatal("missing key field succeeded")
	}
}

func TestBaseDigestIsStableAcrossInputAndMapOrder(t *testing.T) {
	t.Parallel()

	recordsA := []domain.ConfigurationRecord{
		{RecordKey: "b", Data: map[string]string{"z": "2", "a": "1"}},
		{RecordKey: "a", Data: map[string]string{"x": "true"}},
	}
	recordsB := []domain.ConfigurationRecord{
		{RecordKey: "a", Data: map[string]string{"x": "true"}},
		{RecordKey: "b", Data: map[string]string{"a": "1", "z": "2"}},
	}

	first, err := domain.ComputeBaseDigest(recordsA)
	if err != nil {
		t.Fatalf("ComputeBaseDigest A: %v", err)
	}
	second, err := domain.ComputeBaseDigest(recordsB)
	if err != nil {
		t.Fatalf("ComputeBaseDigest B: %v", err)
	}
	if first != second {
		t.Fatalf("digest changed with ordering: %v != %v", first, second)
	}
	if first.Algorithm != "SHA-256" || len(first.Value) != 64 {
		t.Fatalf("unexpected digest: %+v", first)
	}
	if first.Value != "70b79f915bffea02dbd2e1f92974af6c8d1b53585d89327a838cb5b2999e678d" {
		t.Fatalf("non-empty protocol digest = %s", first.Value)
	}

	empty, err := domain.ComputeBaseDigest(nil)
	if err != nil {
		t.Fatalf("empty digest: %v", err)
	}
	if empty.Value != "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945" {
		t.Fatalf("empty digest = %s", empty.Value)
	}
}

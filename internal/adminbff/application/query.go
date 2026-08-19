package application

import (
	"context"
	"errors"
	"time"
)

var ErrPageQueryInvalid = errors.New("invalid page query")

type QueryType string

const (
	QueryTypeAll      QueryType = "ALL"
	QueryTypeOnlyData QueryType = "ONLY_DATA"
)

type FieldType string

const (
	FieldTypeString    FieldType = "STRING"
	FieldTypeInt64     FieldType = "INT64"
	FieldTypeFloat64   FieldType = "FLOAT64"
	FieldTypeBool      FieldType = "BOOL"
	FieldTypeTimestamp FieldType = "TIMESTAMP"
	FieldTypeJSON      FieldType = "JSON"
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

type AutoFillSource string

const (
	AutoFillActorSubject AutoFillSource = "ACTOR_SUBJECT"
	AutoFillActorName    AutoFillSource = "ACTOR_NAME"
	AutoFillCurrentTime  AutoFillSource = "CURRENT_TIME"
	AutoFillConstant     AutoFillSource = "CONSTANT"
	AutoFillUUID         AutoFillSource = "UUID"
)

type PageSpec struct {
	Number *int32
	Size   *int32
}

type ScalarValue struct {
	Type      FieldType
	Canonical string
}

type FilterCondition struct {
	Field    string
	Operator FilterOperator
	Value    *ScalarValue
	Lower    *ScalarValue
	Upper    *ScalarValue
	Set      []ScalarValue
}

type QueryRequest struct {
	ModelCode     string
	Region        string
	Environment   string
	Stage         string
	PreviewBucket *int32
	Type          QueryType
	Page          PageSpec
	Conditions    []FilterCondition
}

type SnapshotIdentity struct {
	ServerEpoch      string
	ServerInstanceID string
	SnapshotInstance string
	Generation       uint64
	PublishedAt      time.Time
}

type SelectOption struct {
	Code     string
	Label    string
	Disabled bool
}

type ValidationRule struct {
	Kind    ValidationRuleKind
	Params  map[string]string
	Message string
}

type AutoFillRule struct {
	Source AutoFillSource
	Value  string
}

type InteractionField struct {
	Name                   string
	DisplayName            string
	Description            string
	Type                   FieldType
	UIControl              UIControlType
	Queryable              bool
	Editable               bool
	Required               bool
	Sensitive              bool
	Projected              bool
	KeyField               bool
	AllowedFilterOperators []FilterOperator
	DefaultFilterOperator  FilterOperator
	DefaultValue           *string
	AutoFill               *AutoFillRule
	ValidationRules        []ValidationRule
	DisplayOrder           int32
	Options                []SelectOption
}

type Row struct {
	RecordKey      string
	RecordRevision uint64
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

type QueryResult struct {
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
	Snapshot           SnapshotIdentity
	ModelRevision      uint64
	CollectionRevision uint64
}

type PageQueryPort interface {
	QueryPage(context.Context, QueryRequest) (QueryResult, error)
}

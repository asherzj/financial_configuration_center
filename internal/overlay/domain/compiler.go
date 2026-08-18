package domain

import (
	"errors"
	"fmt"
	"maps"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
)

var ErrInvalidSpec = errors.New("invalid overlay rule specification")

// RuleSpec contains rule identity and the base-relative desired effective
// state. The action is intentionally absent and is always derived.
type RuleSpec struct {
	ID                string
	Collection        string
	Scope             Scope
	RecordKey         string
	Base              *catalog.ConfigurationRecord
	Desired           *catalog.ConfigurationRecord
	RolloutRanges     []BucketRange
	ConfigRevision    catalog.ConfigRevision
	ReleaseOrderID    string
	ActivatedRevision *catalog.ConfigRevision
	ExpiredRevision   *catalog.ConfigRevision
}

// CompileRule derives a canonical OverlayRule from base and desired states.
func CompileRule(spec RuleSpec) (Rule, error) {
	if spec.Collection == "" || spec.Scope.Region == "" || spec.Scope.Environment == "" || spec.Scope.Stage == "" || spec.RecordKey == "" {
		return Rule{}, fmt.Errorf("%w: collection, full scope, and record key are required", ErrInvalidSpec)
	}
	if spec.Base == nil && spec.Desired == nil {
		return Rule{}, fmt.Errorf("%w: base and desired cannot both be missing", ErrInvalidSpec)
	}
	if spec.Base != nil && !matchesTarget(*spec.Base, spec) {
		return Rule{}, fmt.Errorf("%w: base record identity does not match target", ErrInvalidSpec)
	}
	if spec.Desired != nil && !matchesTarget(*spec.Desired, spec) {
		return Rule{}, fmt.Errorf("%w: desired record identity does not match target", ErrInvalidSpec)
	}
	if (spec.Base != nil && spec.Base.Data == nil) || (spec.Desired != nil && spec.Desired.Data == nil) {
		return Rule{}, fmt.Errorf("%w: present records require content", ErrInvalidSpec)
	}
	if spec.Base != nil && spec.Desired != nil && maps.Equal(spec.Base.Data, spec.Desired.Data) {
		return Rule{}, fmt.Errorf("%w: desired state is unchanged", ErrInvalidSpec)
	}
	ranges, err := normalizeRanges(spec.RolloutRanges)
	if err != nil {
		return Rule{}, fmt.Errorf("%w: %v", ErrInvalidSpec, err)
	}
	action := ActionDelete
	var content map[string]string
	if spec.Base == nil {
		action = ActionAdd
		content = cloneData(spec.Desired.Data)
	} else if spec.Desired != nil {
		action = ActionModify
		content = cloneData(spec.Desired.Data)
	}
	return Rule{
		ID:                spec.ID,
		Collection:        spec.Collection,
		Scope:             spec.Scope,
		RecordKey:         spec.RecordKey,
		Action:            action,
		Content:           content,
		RolloutRanges:     ranges,
		ConfigRevision:    spec.ConfigRevision,
		ReleaseOrderID:    spec.ReleaseOrderID,
		ActivatedRevision: cloneRevision(spec.ActivatedRevision),
		ExpiredRevision:   cloneRevision(spec.ExpiredRevision),
	}, nil
}

func matchesTarget(record catalog.ConfigurationRecord, spec RuleSpec) bool {
	return record.Collection == spec.Collection &&
		record.Environment == spec.Scope.Environment &&
		record.RecordKey == spec.RecordKey
}

func cloneRevision(revision *catalog.ConfigRevision) *catalog.ConfigRevision {
	if revision == nil {
		return nil
	}
	cloned := *revision
	return &cloned
}

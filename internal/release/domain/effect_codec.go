package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
)

const stepEffectVersion int32 = 1

func EncodeStepEffect(effect *StepEffectEnvelope) ([]byte, error) {
	if err := ValidateStepEffect(effect); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(effect)
	if err != nil {
		return nil, fmt.Errorf("encode step effect: %w", err)
	}
	return encoded, nil
}

func DecodeStepEffect(document []byte) (*StepEffectEnvelope, error) {
	if len(bytes.TrimSpace(document)) == 0 {
		return nil, errors.New("decode step effect: document is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var effect StepEffectEnvelope
	if err := decoder.Decode(&effect); err != nil {
		return nil, fmt.Errorf("decode step effect: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return nil, err
	}
	if err := ValidateStepEffect(&effect); err != nil {
		return nil, fmt.Errorf("decode step effect: %w", err)
	}
	return cloneStepEffect(&effect), nil
}

func ValidateStepEffect(effect *StepEffectEnvelope) error {
	if effect == nil || effect.EffectVersion != stepEffectVersion {
		return errors.New("unsupported step effect envelope version")
	}
	payloads := 0
	if effect.Base != nil {
		payloads++
	}
	if effect.Overlay != nil {
		payloads++
	}
	if effect.Percent != nil {
		payloads++
	}
	if payloads != 1 {
		return errors.New("step effect envelope must contain exactly one payload")
	}
	switch effect.EffectType {
	case StepEffectBase:
		if effect.Base == nil || effect.Overlay != nil || effect.Percent != nil {
			return errors.New("BASE step effect payload does not match effect type")
		}
		return validateBaseEffect(effect.Base)
	case StepEffectOverlay:
		if effect.Overlay == nil || effect.Base != nil || effect.Percent != nil {
			return errors.New("OVERLAY step effect payload does not match effect type")
		}
		return validateOverlayEffect(effect.Overlay)
	case StepEffectPercent:
		if effect.Percent == nil || effect.Base != nil || effect.Overlay != nil {
			return errors.New("PERCENT step effect payload does not match effect type")
		}
		return validatePercentEffect(effect.Percent)
	default:
		return fmt.Errorf("unsupported step effect type %q", effect.EffectType)
	}
}

func validateBaseEffect(effect *BaseEffect) error {
	if err := validateEffectIdentity(effect.EffectVersion, effect.Collection, effect.Scope, effect.PreviousRevision, effect.AppliedRevision, effect.ExecutedBy, effect.ExecutedAt.IsZero()); err != nil {
		return fmt.Errorf("BASE effect: %w", err)
	}
	if len(effect.Changes) == 0 {
		return errors.New("BASE effect: changes are required")
	}
	for _, change := range effect.Changes {
		valid := change.Action == ChangeAdd && change.Before == nil && change.After != nil ||
			change.Action == ChangeModify && change.Before != nil && change.After != nil ||
			change.Action == ChangeDelete && change.Before != nil && change.After == nil
		if !valid {
			return fmt.Errorf("BASE effect: invalid %s change", change.Action)
		}
	}
	return validateOverlayChanges("BASE effect overlay cleanup", effect.OverlayChanges, true)
}

func validateOverlayEffect(effect *OverlayEffect) error {
	if err := validateEffectIdentity(effect.EffectVersion, effect.Collection, effect.Scope, effect.PreviousRevision, effect.AppliedRevision, effect.ExecutedBy, effect.ExecutedAt.IsZero()); err != nil {
		return fmt.Errorf("OVERLAY effect: %w", err)
	}
	if strings.TrimSpace(effect.Scope.Stage) == "" {
		return errors.New("OVERLAY effect: stage is required")
	}
	return validateOverlayChanges("OVERLAY effect", effect.Changes, false)
}

func validatePercentEffect(effect *PercentEffect) error {
	if err := validateEffectIdentity(effect.EffectVersion, effect.Collection, effect.Scope, effect.PreviousRevision, effect.AppliedRevision, effect.ExecutedBy, effect.ExecutedAt.IsZero()); err != nil {
		return fmt.Errorf("PERCENT effect: %w", err)
	}
	if strings.TrimSpace(effect.Scope.Stage) == "" || len(effect.AddedRanges) == 0 {
		return errors.New("PERCENT effect: stage and added ranges are required")
	}
	for _, interval := range effect.AddedRanges {
		if interval.Start < 0 || interval.End > 99 || interval.Start > interval.End {
			return errors.New("PERCENT effect: added range is invalid")
		}
	}
	return validateOverlayChanges("PERCENT effect", effect.Changes, false)
}

func validateEffectIdentity(version int32, collection string, scope Scope, previousRevision, appliedRevision catalog.ConfigRevision, actor string, zeroTime bool) error {
	if version != stepEffectVersion {
		return errors.New("unsupported payload version")
	}
	if strings.TrimSpace(collection) == "" || strings.TrimSpace(scope.Region) == "" || strings.TrimSpace(scope.Environment) == "" || strings.TrimSpace(actor) == "" || zeroTime {
		return errors.New("complete collection, scope, actor, and execution time are required")
	}
	if appliedRevision <= previousRevision || previousRevision == 0 {
		return errors.New("applied revision must be greater than a positive previous revision")
	}
	return nil
}

func validateOverlayChanges(label string, changes []OverlayRuleChange, allowEmpty bool) error {
	if len(changes) == 0 && !allowEmpty {
		return fmt.Errorf("%s: changes are required", label)
	}
	for _, change := range changes {
		if strings.TrimSpace(change.RecordKey) == "" || change.PreviousRule == nil && change.NewRule == nil {
			return fmt.Errorf("%s: each change requires a record key and at least one rule", label)
		}
	}
	return nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode step effect: multiple JSON values are not allowed")
		}
		return fmt.Errorf("decode step effect: trailing data: %w", err)
	}
	return nil
}

func cloneStepEffect(effect *StepEffectEnvelope) *StepEffectEnvelope {
	if effect == nil {
		return nil
	}
	copy := *effect
	copy.Base = cloneBaseEffectPointer(effect.Base)
	copy.Overlay = cloneOverlayEffectPointer(effect.Overlay)
	copy.Percent = clonePercentEffectPointer(effect.Percent)
	return &copy
}

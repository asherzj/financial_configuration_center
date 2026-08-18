package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type FinalEffect string

const (
	FinalEffectBase    FinalEffect = "BASE_FINAL"
	FinalEffectOverlay FinalEffect = "OVERLAY_FINAL"
)

type SelfApprovalPolicy string

const (
	SelfApprovalDenyProduction SelfApprovalPolicy = "DENY_PRODUCTION"
	SelfApprovalAllow          SelfApprovalPolicy = "ALLOW"
)

type ManualReviewParams struct {
	SelfApprovalPolicy SelfApprovalPolicy
}

type BaseApplyParams struct {
	CleanupScopeOverlay bool
}

type CompareParams struct {
	Mode          string
	PreviewBucket *int
}

type StepDefinition struct {
	Code          string
	Type          StepType
	RequiredRoles []string
	ManualReview  *ManualReviewParams
	BaseApply     *BaseApplyParams
	Compare       *CompareParams
}

type CompiledTemplate struct {
	finalEffect FinalEffect
	steps       []StepDefinition
}

func CompileTemplate(document []byte, finalEffect FinalEffect) (CompiledTemplate, error) {
	if finalEffect != FinalEffectBase {
		return CompiledTemplate{}, errors.New("compile template: only BASE_FINAL is implemented")
	}
	var root struct {
		Steps []json.RawMessage `json:"steps"`
	}
	if err := decodeStrict(document, &root); err != nil {
		return CompiledTemplate{}, fmt.Errorf("compile template document: %w", err)
	}
	if len(root.Steps) == 0 {
		return CompiledTemplate{}, errors.New("compile template: steps are required")
	}
	steps := make([]StepDefinition, len(root.Steps))
	seenCodes := make(map[string]struct{}, len(root.Steps))
	baseCount, completeCount := 0, 0
	for index, raw := range root.Steps {
		var envelope struct {
			Code          string          `json:"code"`
			Type          StepType        `json:"type"`
			RequiredRoles []string        `json:"requiredRoles"`
			Params        json.RawMessage `json:"params"`
		}
		if err := decodeStrict(raw, &envelope); err != nil {
			return CompiledTemplate{}, fmt.Errorf("compile template step %d: %w", index, err)
		}
		if envelope.Code == "" || envelope.Code != strings.TrimSpace(envelope.Code) {
			return CompiledTemplate{}, fmt.Errorf("compile template step %d: stable code is required", index)
		}
		if _, duplicate := seenCodes[envelope.Code]; duplicate {
			return CompiledTemplate{}, fmt.Errorf("compile template: duplicate step code %q", envelope.Code)
		}
		seenCodes[envelope.Code] = struct{}{}
		roles, err := compileRoles(envelope.RequiredRoles)
		if err != nil {
			return CompiledTemplate{}, fmt.Errorf("compile template step %q: %w", envelope.Code, err)
		}
		step := StepDefinition{Code: envelope.Code, Type: envelope.Type, RequiredRoles: roles}
		switch envelope.Type {
		case StepManualReview:
			if len(roles) == 0 {
				return CompiledTemplate{}, fmt.Errorf("compile template step %q: manual review requires roles", envelope.Code)
			}
			var params struct {
				SelfApprovalPolicy SelfApprovalPolicy `json:"selfApprovalPolicy"`
			}
			if err := decodeStrict(envelope.Params, &params); err != nil {
				return CompiledTemplate{}, fmt.Errorf("compile template step %q params: %w", envelope.Code, err)
			}
			if params.SelfApprovalPolicy != SelfApprovalDenyProduction && params.SelfApprovalPolicy != SelfApprovalAllow {
				return CompiledTemplate{}, fmt.Errorf("compile template step %q: invalid self approval policy", envelope.Code)
			}
			step.ManualReview = &ManualReviewParams{SelfApprovalPolicy: params.SelfApprovalPolicy}
		case StepBaseApply:
			baseCount++
			var params struct {
				CleanupScopeOverlay bool `json:"cleanupScopeOverlay"`
			}
			if err := decodeStrict(envelope.Params, &params); err != nil {
				return CompiledTemplate{}, fmt.Errorf("compile template step %q params: %w", envelope.Code, err)
			}
			if !params.CleanupScopeOverlay {
				return CompiledTemplate{}, fmt.Errorf("compile template step %q: BASE_FINAL must clean scope overlay", envelope.Code)
			}
			step.BaseApply = &BaseApplyParams{CleanupScopeOverlay: true}
		case StepCompare:
			var params struct {
				Mode          string `json:"mode"`
				PreviewBucket *int   `json:"previewBucket"`
			}
			if err := decodeStrict(envelope.Params, &params); err != nil {
				return CompiledTemplate{}, fmt.Errorf("compile template step %q params: %w", envelope.Code, err)
			}
			if params.Mode != "EFFECTIVE" && params.Mode != "BASE" {
				return CompiledTemplate{}, fmt.Errorf("compile template step %q: compare mode is invalid", envelope.Code)
			}
			if params.PreviewBucket != nil && (*params.PreviewBucket < 0 || *params.PreviewBucket > 99) {
				return CompiledTemplate{}, fmt.Errorf("compile template step %q: preview bucket is outside 0..99", envelope.Code)
			}
			step.Compare = &CompareParams{Mode: params.Mode, PreviewBucket: cloneInt(params.PreviewBucket)}
		case StepComplete:
			completeCount++
			var params struct{}
			if err := decodeStrict(envelope.Params, &params); err != nil {
				return CompiledTemplate{}, fmt.Errorf("compile template step %q params: %w", envelope.Code, err)
			}
		default:
			return CompiledTemplate{}, fmt.Errorf("compile template step %q: unsupported type %q", envelope.Code, envelope.Type)
		}
		steps[index] = step
	}
	if baseCount != 1 || completeCount != 1 || steps[len(steps)-1].Type != StepComplete {
		return CompiledTemplate{}, errors.New("compile BASE_FINAL template: exactly one BASE_APPLY and one final COMPLETE are required")
	}
	return CompiledTemplate{finalEffect: finalEffect, steps: steps}, nil
}

func (template CompiledTemplate) FinalEffect() FinalEffect { return template.finalEffect }

func (template CompiledTemplate) Steps() []StepDefinition {
	steps := make([]StepDefinition, len(template.steps))
	for index, step := range template.steps {
		steps[index] = cloneStepDefinition(step)
	}
	return steps
}

func decodeStrict(data []byte, target any) error {
	if len(data) == 0 {
		return errors.New("JSON value is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("JSON value must contain exactly one document")
	}
	return nil
}

func compileRoles(source []string) ([]string, error) {
	roles := make([]string, len(source))
	seen := make(map[string]struct{}, len(source))
	for index, role := range source {
		if role == "" || role != strings.TrimSpace(role) {
			return nil, errors.New("roles must be stable non-empty identifiers")
		}
		if _, duplicate := seen[role]; duplicate {
			return nil, fmt.Errorf("duplicate role %q", role)
		}
		seen[role] = struct{}{}
		roles[index] = role
	}
	return roles, nil
}

func cloneStepDefinition(step StepDefinition) StepDefinition {
	step.RequiredRoles = append([]string(nil), step.RequiredRoles...)
	if step.ManualReview != nil {
		params := *step.ManualReview
		step.ManualReview = &params
	}
	if step.BaseApply != nil {
		params := *step.BaseApply
		step.BaseApply = &params
	}
	if step.Compare != nil {
		params := *step.Compare
		params.PreviewBucket = cloneInt(params.PreviewBucket)
		step.Compare = &params
	}
	return step
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

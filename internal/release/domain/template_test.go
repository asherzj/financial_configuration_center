package domain_test

import (
	"testing"

	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
)

func TestCompileBaseFinalTemplateUsesClosedTypedStepSchemas(t *testing.T) {
	t.Parallel()
	template, err := release.CompileTemplate([]byte(`{
		"steps":[
			{"code":"review","type":"MANUAL_REVIEW","requiredRoles":["RELEASE_APPROVER"],"params":{"selfApprovalPolicy":"DENY_PRODUCTION"}},
			{"code":"apply","type":"BASE_APPLY","params":{"cleanupScopeOverlay":true}},
			{"code":"compare","type":"COMPARE","params":{"mode":"EFFECTIVE"}},
			{"code":"complete","type":"COMPLETE","params":{}}
		]
	}`), release.FinalEffectBase)
	if err != nil {
		t.Fatal(err)
	}
	steps := template.Steps()
	if len(steps) != 4 || steps[0].ManualReview == nil || steps[0].ManualReview.SelfApprovalPolicy != release.SelfApprovalDenyProduction || steps[1].BaseApply == nil || !steps[1].BaseApply.CleanupScopeOverlay || steps[2].Compare == nil {
		t.Fatalf("compiled steps = %+v", steps)
	}
	steps[0].RequiredRoles[0] = "mutated"
	if template.Steps()[0].RequiredRoles[0] != "RELEASE_APPROVER" {
		t.Fatal("compiled template leaked mutable roles")
	}
}

func TestCompileOverlayFinalTemplateUsesTypedOverlayApply(t *testing.T) {
	t.Parallel()
	template, err := release.CompileTemplate([]byte(`{
		"steps":[
			{"code":"apply-overlay","type":"OVERLAY_APPLY","params":{}},
			{"code":"complete","type":"COMPLETE","params":{}}
		]
	}`), release.FinalEffectOverlay)
	if err != nil {
		t.Fatal(err)
	}
	steps := template.Steps()
	if template.FinalEffect() != release.FinalEffectOverlay || len(steps) != 2 || steps[0].OverlayApply == nil || steps[0].Type != release.StepOverlayApply {
		t.Fatalf("compiled overlay template = effect %s steps %+v", template.FinalEffect(), steps)
	}
}

func TestCompileBaseFinalTemplateUsesTypedMonotonicPercentageSteps(t *testing.T) {
	t.Parallel()
	template, err := release.CompileTemplate([]byte(`{
		"steps":[
			{"code":"percent-10","type":"PERCENT_ROLLOUT","params":{"ranges":[{"start":0,"end":9}]}},
			{"code":"percent-50","type":"PERCENT_ROLLOUT","params":{"ranges":[{"start":25,"end":49},{"start":10,"end":24}]}},
			{"code":"compare","type":"COMPARE","params":{"mode":"EFFECTIVE","previewBucket":25}},
			{"code":"promote","type":"BASE_APPLY","params":{"cleanupScopeOverlay":true}},
			{"code":"complete","type":"COMPLETE","params":{}}
		]
	}`), release.FinalEffectBase)
	if err != nil {
		t.Fatal(err)
	}
	steps := template.Steps()
	if len(steps) != 5 || steps[0].PercentRollout == nil || steps[1].PercentRollout == nil {
		t.Fatalf("compiled percentage steps = %+v", steps)
	}
	if got := steps[1].PercentRollout.Ranges; len(got) != 1 || got[0] != (overlay.BucketRange{Start: 10, End: 49}) {
		t.Fatalf("canonical second step ranges = %+v", got)
	}
	steps[0].PercentRollout.Ranges[0].End = 99
	if template.Steps()[0].PercentRollout.Ranges[0].End != 9 {
		t.Fatal("compiled template leaked mutable percentage ranges")
	}
}

func TestCompileTemplateRejectsUnsafePercentageRanges(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"empty":                 `{"steps":[{"code":"percent","type":"PERCENT_ROLLOUT","params":{"ranges":[]}},{"code":"apply","type":"BASE_APPLY","params":{"cleanupScopeOverlay":true}},{"code":"complete","type":"COMPLETE","params":{}}]}`,
		"overlap between steps": `{"steps":[{"code":"first","type":"PERCENT_ROLLOUT","params":{"ranges":[{"start":0,"end":20}]}},{"code":"second","type":"PERCENT_ROLLOUT","params":{"ranges":[{"start":20,"end":40}]}},{"code":"apply","type":"BASE_APPLY","params":{"cleanupScopeOverlay":true}},{"code":"complete","type":"COMPLETE","params":{}}]}`,
		"outside protocol":      `{"steps":[{"code":"percent","type":"PERCENT_ROLLOUT","params":{"ranges":[{"start":0,"end":100}]}},{"code":"apply","type":"BASE_APPLY","params":{"cleanupScopeOverlay":true}},{"code":"complete","type":"COMPLETE","params":{}}]}`,
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := release.CompileTemplate([]byte(document), release.FinalEffectBase); err == nil {
				t.Fatal("unsafe percentage template compiled")
			}
		})
	}
}

func TestCompileTemplateRejectsOpenOrUnsafeShapes(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"unknown param":       `{"steps":[{"code":"apply","type":"BASE_APPLY","params":{"cleanupScopeOverlay":true,"sql":"DELETE"}},{"code":"complete","type":"COMPLETE","params":{}}]}`,
		"review without role": `{"steps":[{"code":"review","type":"MANUAL_REVIEW","params":{"selfApprovalPolicy":"DENY_PRODUCTION"}},{"code":"apply","type":"BASE_APPLY","params":{"cleanupScopeOverlay":true}},{"code":"complete","type":"COMPLETE","params":{}}]}`,
		"not complete last":   `{"steps":[{"code":"complete","type":"COMPLETE","params":{}},{"code":"apply","type":"BASE_APPLY","params":{"cleanupScopeOverlay":true}}]}`,
		"duplicate code":      `{"steps":[{"code":"apply","type":"BASE_APPLY","params":{"cleanupScopeOverlay":true}},{"code":"apply","type":"COMPLETE","params":{}}]}`,
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := release.CompileTemplate([]byte(document), release.FinalEffectBase); err == nil {
				t.Fatal("invalid template compiled")
			}
		})
	}
}

func TestCompileTemplateRejectsFinalEffectStepMismatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		finalEffect release.FinalEffect
		document    string
	}{
		{
			name:        "overlay final with base apply",
			finalEffect: release.FinalEffectOverlay,
			document:    `{"steps":[{"code":"apply","type":"BASE_APPLY","params":{"cleanupScopeOverlay":true}},{"code":"complete","type":"COMPLETE","params":{}}]}`,
		},
		{
			name:        "base final with overlay apply",
			finalEffect: release.FinalEffectBase,
			document:    `{"steps":[{"code":"apply","type":"OVERLAY_APPLY","params":{}},{"code":"complete","type":"COMPLETE","params":{}}]}`,
		},
		{
			name:        "overlay apply with unknown param",
			finalEffect: release.FinalEffectOverlay,
			document:    `{"steps":[{"code":"apply","type":"OVERLAY_APPLY","params":{"sql":"UPDATE"}},{"code":"complete","type":"COMPLETE","params":{}}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := release.CompileTemplate([]byte(test.document), test.finalEffect); err == nil {
				t.Fatal("mismatched template compiled")
			}
		})
	}
}

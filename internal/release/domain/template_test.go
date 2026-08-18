package domain_test

import (
	"testing"

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

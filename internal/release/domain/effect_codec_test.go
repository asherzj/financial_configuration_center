package domain_test

import (
	"strings"
	"testing"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
)

func TestStepEffectCodecRoundTripsEveryClosedVariant(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	record := catalog.ConfigurationRecord{Collection: "routes", Environment: "production", RecordKey: "key", Data: map[string]string{"name": "visa"}, ConfigRevision: 8}
	tests := []struct {
		name     string
		envelope release.StepEffectEnvelope
		wantType release.StepEffectType
	}{
		{name: "base", wantType: release.StepEffectBase, envelope: release.StepEffectEnvelope{EffectVersion: 1, EffectType: release.StepEffectBase, Base: &release.BaseEffect{
			EffectVersion: 1, Collection: "routes", Scope: release.Scope{Region: "cn", Environment: "production"}, PreviousRevision: 7, AppliedRevision: 8,
			Changes: []release.BaseChange{{Action: release.ChangeAdd, After: &record}}, ExecutedAt: now, ExecutedBy: "operator",
		}}},
		{name: "overlay", wantType: release.StepEffectOverlay, envelope: release.StepEffectEnvelope{EffectVersion: 1, EffectType: release.StepEffectOverlay, Overlay: &release.OverlayEffect{
			EffectVersion: 1, Collection: "routes", Scope: release.Scope{Region: "cn", Environment: "production", Stage: "blue"}, PreviousRevision: 7, AppliedRevision: 8,
			Changes: []release.OverlayRuleChange{{RecordKey: "key", NewRule: &overlay.Rule{ID: "rule"}}}, ExecutedAt: now, ExecutedBy: "operator",
		}}},
		{name: "percent", wantType: release.StepEffectPercent, envelope: release.StepEffectEnvelope{EffectVersion: 1, EffectType: release.StepEffectPercent, Percent: &release.PercentEffect{
			EffectVersion: 1, Collection: "routes", Scope: release.Scope{Region: "cn", Environment: "production", Stage: "blue"}, PreviousRevision: 7, AppliedRevision: 8,
			AddedRanges: []overlay.BucketRange{{Start: 0, End: 9}}, Changes: []release.OverlayRuleChange{{RecordKey: "key", NewRule: &overlay.Rule{ID: "rule"}}}, ExecutedAt: now, ExecutedBy: "operator",
		}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := release.EncodeStepEffect(&test.envelope)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := release.DecodeStepEffect(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if decoded.EffectType != test.wantType || !strings.Contains(string(encoded), `"effectType":"`+string(test.wantType)+`"`) {
				t.Fatalf("decoded=%+v json=%s", decoded, encoded)
			}
		})
	}
}

func TestStepEffectCodecRejectsUnknownOrAmbiguousFacts(t *testing.T) {
	t.Parallel()
	for _, document := range []string{
		`{"effectVersion":2,"effectType":"BASE","base":{}}`,
		`{"effectVersion":1,"effectType":"FUTURE","base":{}}`,
		`{"effectVersion":1,"effectType":"BASE","base":{"effectVersion":2}}`,
		`{"effectVersion":1,"effectType":"BASE","base":{},"overlay":{}}`,
		`{"effectVersion":1,"effectType":"BASE","future":{}}`,
	} {
		if decoded, err := release.DecodeStepEffect([]byte(document)); err == nil || decoded != nil {
			t.Fatalf("DecodeStepEffect(%s) = %+v, %v", document, decoded, err)
		}
	}
}

package contracts_test

import (
	"strings"
	"testing"

	configv1 "github.com/asherzj/financial_configuration_center/contracts/gen/go/finconfig/config/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestSnapshotDiagnosticsAdditiveFieldNumbersAreStable(t *testing.T) {
	t.Parallel()

	descriptor := (&configv1.GetSnapshotStatusResponse{}).ProtoReflect().Descriptor()
	want := map[protoreflect.Name]protoreflect.FieldNumber{
		"snapshot":                        1,
		"collection_count":                2,
		"failed_dependency_groups":        3,
		"environment":                     4,
		"collections":                     5,
		"failed_dependency_group_details": 6,
		"last_error_code":                 7,
	}
	if descriptor.Fields().Len() != len(want) {
		t.Fatalf("field count = %d, want %d", descriptor.Fields().Len(), len(want))
	}
	for name, number := range want {
		field := descriptor.Fields().ByName(name)
		if field == nil || field.Number() != number {
			t.Fatalf("field %s = %v, want number %d", name, field, number)
		}
	}
	if field := descriptor.Fields().ByName("collections"); field.Cardinality() != protoreflect.Repeated {
		t.Fatalf("collections cardinality = %s", field.Cardinality())
	}
	if field := descriptor.Fields().ByName("failed_dependency_group_details"); field.Cardinality() != protoreflect.Repeated {
		t.Fatalf("failed dependency group cardinality = %s", field.Cardinality())
	}
	if field := descriptor.Fields().ByName("last_error_code"); !field.HasOptionalKeyword() {
		t.Fatal("last_error_code must preserve absence separately from an empty code")
	}
}

func TestSnapshotDiagnosticsMessagesKeepStructuredCollectionAndGroupData(t *testing.T) {
	t.Parallel()

	lastErrorCode := "DEPENDENCY_GROUP_FAILED"
	response := &configv1.GetSnapshotStatusResponse{
		Environment: "production",
		Collections: []*configv1.SnapshotCollectionStatus{{
			Collection: "routes", ConfigRevision: 8, ChangeCursor: 34,
		}},
		FailedDependencyGroupDetails: []*configv1.FailedDependencyGroup{{Collections: []string{"routes", "options"}}},
		LastErrorCode:                &lastErrorCode,
	}
	if response.GetEnvironment() != "production" || response.GetCollections()[0].GetConfigRevision() != 8 || response.GetCollections()[0].GetChangeCursor() != 34 {
		t.Fatalf("collection diagnostics = %+v", response)
	}
	if got := response.GetFailedDependencyGroupDetails()[0].GetCollections(); len(got) != 2 || got[0] != "routes" || got[1] != "options" {
		t.Fatalf("failed dependency group = %v", got)
	}
	if response.GetLastErrorCode() != lastErrorCode {
		t.Fatalf("last error code = %q", response.GetLastErrorCode())
	}
}

func TestSnapshotDiagnosticsCompatibilityFixtureDualWritesEquivalentViews(t *testing.T) {
	t.Parallel()

	groups := []*configv1.FailedDependencyGroup{{Collections: []string{"routes", "options"}}, {Collections: []string{"features"}}}
	response := &configv1.GetSnapshotStatusResponse{
		CollectionCount:              1,
		Collections:                  []*configv1.SnapshotCollectionStatus{{Collection: "routes"}},
		FailedDependencyGroups:       []string{"routes,options", "features"},
		FailedDependencyGroupDetails: groups,
	}
	if response.GetCollectionCount() != int64(len(response.GetCollections())) {
		t.Fatalf("legacy collection count %d does not match %d structured collections", response.GetCollectionCount(), len(response.GetCollections()))
	}
	if len(response.GetFailedDependencyGroups()) != len(response.GetFailedDependencyGroupDetails()) {
		t.Fatalf("legacy failed groups %d does not match %d structured groups", len(response.GetFailedDependencyGroups()), len(response.GetFailedDependencyGroupDetails()))
	}
	for index, group := range response.GetFailedDependencyGroupDetails() {
		if got, want := response.GetFailedDependencyGroups()[index], strings.Join(group.GetCollections(), ","); got != want {
			t.Fatalf("legacy failed group %d = %q, want %q", index, got, want)
		}
	}

	descriptor := response.ProtoReflect().Descriptor()
	for _, name := range []protoreflect.Name{"collection_count", "failed_dependency_groups"} {
		field := descriptor.Fields().ByName(name)
		options, ok := field.Options().(*descriptorpb.FieldOptions)
		if !ok || !options.GetDeprecated() {
			t.Fatalf("legacy field %s must remain present and deprecated", name)
		}
	}
}

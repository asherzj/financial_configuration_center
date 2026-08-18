package domain_test

import (
	"errors"
	"reflect"
	"testing"

	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
)

func TestClientBucketProtocolVectors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		consumer string
		client   string
		want     int32
	}{
		{consumer: "payment-service", client: "pod-8", want: 97},
		{consumer: "payment-service", client: "pod-10", want: 6},
		{consumer: "consumer", client: "client", want: 71},
	}
	for _, test := range tests {
		got, err := overlay.ClientBucket(test.consumer, test.client)
		if err != nil {
			t.Fatalf("ClientBucket(%q, %q): %v", test.consumer, test.client, err)
		}
		if got != test.want {
			t.Fatalf("ClientBucket(%q, %q) = %d, want %d", test.consumer, test.client, got, test.want)
		}
	}
}

func TestClientBucketRejectsNonCanonicalIdentity(t *testing.T) {
	t.Parallel()
	for _, identity := range [][2]string{{"", "client"}, {"consumer", ""}, {" consumer", "client"}, {"consumer\x00alias", "client"}} {
		if _, err := overlay.ClientBucket(identity[0], identity[1]); !errors.Is(err, overlay.ErrInvalidRollout) {
			t.Fatalf("ClientBucket(%q, %q) error = %v", identity[0], identity[1], err)
		}
	}
}

func TestExpandRolloutRangesBuildsCanonicalMonotonicUnion(t *testing.T) {
	t.Parallel()
	current := []overlay.BucketRange{{Start: 20, End: 29}, {Start: 0, End: 9}}
	addition := []overlay.BucketRange{{Start: 30, End: 49}, {Start: 10, End: 19}}

	got, err := overlay.ExpandRolloutRanges(current, addition)
	if err != nil {
		t.Fatalf("ExpandRolloutRanges: %v", err)
	}
	want := []overlay.BucketRange{{Start: 0, End: 49}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expanded ranges = %#v, want %#v", got, want)
	}
	current[0].End = 99
	addition[0].Start = 0
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expanded ranges retained caller aliases: %#v", got)
	}
}

func TestExpandRolloutRangesRejectsOverlapAndInvalidBounds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		current  []overlay.BucketRange
		addition []overlay.BucketRange
	}{
		{current: []overlay.BucketRange{{Start: 0, End: 9}}, addition: []overlay.BucketRange{{Start: 9, End: 19}}},
		{addition: []overlay.BucketRange{{Start: -1, End: 9}}},
		{addition: []overlay.BucketRange{{Start: 80, End: 100}}},
		{addition: []overlay.BucketRange{{Start: 20, End: 10}}},
	}
	for _, test := range tests {
		if _, err := overlay.ExpandRolloutRanges(test.current, test.addition); !errors.Is(err, overlay.ErrInvalidRollout) {
			t.Fatalf("ExpandRolloutRanges(%v, %v) error = %v", test.current, test.addition, err)
		}
	}
}

package overlay_test

import (
	"errors"
	"testing"

	overlay "github.com/asherzj/financial_configuration_center/internal/distribution/overlay"
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

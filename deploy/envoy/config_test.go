package envoy_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestKitexEnvoyBoundaryRequiresMTLSHTTP2AndPrivateUDS(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile("kitex-grpc.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(content, &document); err != nil {
		t.Fatalf("parse Envoy config: %v", err)
	}
	text := string(content)
	for _, required := range []string{
		"require_client_certificate: true",
		"tls_minimum_protocol_version: TLSv1_2",
		"alpn_protocols: [h2]",
		"match_typed_subject_alt_names:",
		"forward_client_cert_details: SANITIZE_SET",
		"codec_type: HTTP2",
		"http2_protocol_options: {}",
		"pipe: {path: /var/run/finconfig/backend.sock}",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("Envoy boundary is missing %q", required)
		}
	}
	if strings.Contains(text, "insecure_skip_verify") || strings.Contains(text, "127.0.0.1:9000") {
		t.Fatalf("Envoy config weakens the private backend boundary:\n%s", text)
	}
}

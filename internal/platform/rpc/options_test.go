package rpc_test

import (
	"crypto/tls"
	"testing"

	platformrpc "github.com/asherzj/financial_configuration_center/internal/platform/rpc"
)

func TestStandardGRPCClientOptionsRequireVerifiedModernTLS(t *testing.T) {
	t.Parallel()

	if _, err := platformrpc.StandardGRPCClientOptions(nil); err == nil {
		t.Fatal("nil TLS config succeeded")
	}
	if _, err := platformrpc.StandardGRPCClientOptions(&tls.Config{InsecureSkipVerify: true}); err == nil { //nolint:gosec // negative contract fixture
		t.Fatal("InsecureSkipVerify succeeded")
	}
	if _, err := platformrpc.StandardGRPCClientOptions(&tls.Config{MinVersion: tls.VersionTLS11}); err == nil { //nolint:staticcheck // negative contract fixture
		t.Fatal("TLS 1.1 succeeded")
	}
	options, err := platformrpc.StandardGRPCClientOptions(&tls.Config{ServerName: "config-server.finconfig.svc"})
	if err != nil {
		t.Fatalf("valid TLS config: %v", err)
	}
	if len(options) != 2 {
		t.Fatalf("option count = %d, want transport and TLS", len(options))
	}
}

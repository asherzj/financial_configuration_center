package rpc

import (
	"crypto/tls"
	"errors"
	"net"
	"path/filepath"
	"strings"

	"github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/server"
	"github.com/cloudwego/kitex/transport"
)

// StandardGRPCClientOptions is the mandatory base for every FinConfig Kitex
// client. Callers may append address, timeout and middleware options, but may
// not replace the transport or weaken the TLS profile.
func StandardGRPCClientOptions(tlsConfig *tls.Config) ([]client.Option, error) {
	if tlsConfig == nil {
		return nil, errors.New("Kitex gRPC TLS config is required")
	}
	if tlsConfig.InsecureSkipVerify {
		return nil, errors.New("Kitex gRPC TLS certificate verification cannot be disabled")
	}
	if tlsConfig.MinVersion != 0 && tlsConfig.MinVersion < tls.VersionTLS12 {
		return nil, errors.New("Kitex gRPC TLS minimum version must be TLS 1.2 or newer")
	}
	configured := tlsConfig.Clone()
	if configured.MinVersion == 0 {
		configured.MinVersion = tls.VersionTLS12
	}
	return []client.Option{
		client.WithTransportProtocol(transport.GRPC),
		client.WithGRPCTLSConfig(configured),
	}, nil
}

// PrivateBackendServerOptions binds Kitex to a same-Pod Unix domain socket.
// Envoy is the only network listener and forwards standard HTTP/2 gRPC over
// this private h2c channel.
func PrivateBackendServerOptions(socketPath string) ([]server.Option, error) {
	clean := filepath.Clean(socketPath)
	if !filepath.IsAbs(socketPath) || clean != socketPath || !strings.HasPrefix(clean, "/var/run/finconfig/") || !strings.HasSuffix(clean, ".sock") {
		return nil, errors.New("Kitex backend must use a normalized .sock path under /var/run/finconfig")
	}
	return []server.Option{server.WithServiceAddr(&net.UnixAddr{Name: clean, Net: "unix"})}, nil
}

package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/platform/config"
)

func TestLoadStrictYAMLThenEnvironmentOverrides(t *testing.T) {
	t.Parallel()
	yaml := `
serviceName: control-plane
version: v1
environment: development
instanceId: local-1
operationsListenAddress: 127.0.0.1:9090
backendSocket: /var/run/finconfig/backend.sock
shutdownTimeout: 30s
mysql:
  dsn: yaml-secret
  maxOpenConnections: 20
  maxIdleConnections: 10
  connectionMaxLifetime: 5m
  connectionMaxIdleTime: 1m
telemetry:
  traceSampleRatio: 0.1
  otlpEndpoint: ""
auth:
  devAuthEnabled: true
`
	loaded, err := config.Load(strings.NewReader(yaml), []string{
		"FINCONFIG_MYSQL_DSN=environment-secret",
		"FINCONFIG_TRACE_SAMPLE_RATIO=0.25",
		"FINCONFIG_DEV_AUTH_ENABLED=false",
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MySQL.DSN != "environment-secret" || loaded.Telemetry.TraceSampleRatio != 0.25 || loaded.Auth.DevAuthEnabled || loaded.ShutdownTimeout.Duration != 30*time.Second {
		t.Fatalf("loaded=%+v", loaded)
	}
	summary := loaded.SafeSummary()
	if strings.Contains(summary, "yaml-secret") || strings.Contains(summary, "environment-secret") || !strings.Contains(summary, "control-plane") {
		t.Fatalf("unsafe summary=%s", summary)
	}
}

func TestLoadRejectsUnknownYAMLAndUnsafeProductionSettings(t *testing.T) {
	t.Parallel()
	if _, err := config.Load(strings.NewReader("serviceName: cp\nunknownSetting: true\n"), nil); err == nil {
		t.Fatal("unknown YAML field accepted")
	}
	production := `
serviceName: control-plane
version: v1
environment: production
instanceId: prod-1
operationsListenAddress: 0.0.0.0:9090
backendSocket: /var/run/finconfig/backend.sock
shutdownTimeout: 30s
mysql: {dsn: secret, maxOpenConnections: 20, maxIdleConnections: 10, connectionMaxLifetime: 5m, connectionMaxIdleTime: 1m}
telemetry: {traceSampleRatio: 0.1, otlpEndpoint: ""}
auth: {devAuthEnabled: true}
`
	if _, err := config.Load(strings.NewReader(production), nil); err == nil {
		t.Fatal("production development auth accepted")
	}
}

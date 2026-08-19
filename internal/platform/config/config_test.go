package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/platform/config"
)

func TestLoadStrictYAMLThenEnvironmentOverrides(t *testing.T) {
	t.Parallel()
	const environmentDSN = "finconfig:environment-secret@tcp(mysql:3306)/finconfig?parseTime=true&loc=UTC&timeout=5s&readTimeout=5s&writeTimeout=5s"
	yaml := `
serviceName: control-plane
version: v1
runtimeMode: development
instanceId: local-1
operationsListenAddress: 127.0.0.1:9090
backendSocket: /var/run/finconfig/backend.sock
shutdownTimeout: 30s
mysql:
  dsn: finconfig:yaml-secret@tcp(mysql:3306)/finconfig?parseTime=true&loc=UTC&timeout=5s&readTimeout=5s&writeTimeout=5s
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
		"FINCONFIG_MYSQL_DSN=" + environmentDSN,
		"FINCONFIG_TRACE_SAMPLE_RATIO=0.25",
		"FINCONFIG_DEV_AUTH_ENABLED=false",
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MySQL.DSN != environmentDSN || loaded.Telemetry.TraceSampleRatio != 0.25 || loaded.Auth.DevAuthEnabled || loaded.ShutdownTimeout.Duration != 30*time.Second {
		t.Fatalf("loaded=%+v", loaded)
	}
	summary := loaded.SafeSummary()
	if strings.Contains(summary, "yaml-secret") || strings.Contains(summary, "environment-secret") || !strings.Contains(summary, "control-plane") {
		t.Fatalf("unsafe summary=%s", summary)
	}
}

func TestLoadNamesProcessSafetyModeRuntimeMode(t *testing.T) {
	t.Parallel()
	loaded, err := config.Load(strings.NewReader(`
serviceName: control-plane
version: v1
runtimeMode: test
instanceId: test-1
operationsListenAddress: 127.0.0.1:9090
shutdownTimeout: 30s
mysql: {maxOpenConnections: 20, maxIdleConnections: 10, connectionMaxLifetime: 5m, connectionMaxIdleTime: 1m}
telemetry: {traceSampleRatio: 0.1, otlpEndpoint: ""}
auth: {devAuthEnabled: false}
`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RuntimeMode != "test" {
		t.Fatalf("runtime mode = %q", loaded.RuntimeMode)
	}
}

func TestLoadConfigServerSeparatesManagedEnvironmentFromRuntimeMode(t *testing.T) {
	t.Parallel()
	loaded, err := config.LoadConfigServer(strings.NewReader(`
serviceName: config-server
version: v1
runtimeMode: development
managedEnvironment: staging
serverEpoch: 018f47cb-42f8-7fb2-a4af-0b0bd6dd98c1
instanceId: config-server-1
operationsListenAddress: 127.0.0.1:9090
backendSocket: /var/run/finconfig/backend.sock
shutdownTimeout: 30s
mysql:
  dsn: finconfig:mysql-secret@tcp(mysql:3306)/finconfig?parseTime=true&loc=UTC&timeout=5s&readTimeout=5s&writeTimeout=5s
  maxOpenConnections: 20
  maxIdleConnections: 10
  connectionMaxLifetime: 5m
  connectionMaxIdleTime: 1m
telemetry: {traceSampleRatio: 0.1, otlpEndpoint: ""}
auth: {devAuthEnabled: true}
`), []string{
		"FINCONFIG_MANAGED_ENVIRONMENT=production",
		"FINCONFIG_SERVER_EPOCH=018f47cb-42f8-7fb2-a4af-0b0bd6dd98c2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RuntimeMode != "development" || loaded.ManagedEnvironment != "production" || loaded.ServerEpoch != "018f47cb-42f8-7fb2-a4af-0b0bd6dd98c2" {
		t.Fatalf("loaded config server profile = %+v", loaded)
	}
	if summary := loaded.SafeSummary(); strings.Contains(summary, "mysql-secret") || !strings.Contains(summary, `"managedEnvironment":"production"`) {
		t.Fatalf("unsafe or incomplete summary = %s", summary)
	}
}

func TestLoadConfigServerRejectsMissingBoundaryIdentityAndDependencies(t *testing.T) {
	t.Parallel()
	base := `
serviceName: config-server
version: v1
runtimeMode: development
managedEnvironment: production
serverEpoch: 018f47cb-42f8-7fb2-a4af-0b0bd6dd98c1
instanceId: config-server-1
operationsListenAddress: 127.0.0.1:9090
backendSocket: /var/run/finconfig/backend.sock
shutdownTimeout: 30s
mysql: {dsn: "finconfig:mysql-secret@tcp(mysql:3306)/finconfig?parseTime=true&loc=UTC&timeout=5s&readTimeout=5s&writeTimeout=5s", maxOpenConnections: 20, maxIdleConnections: 10, connectionMaxLifetime: 5m, connectionMaxIdleTime: 1m}
telemetry: {traceSampleRatio: 0.1, otlpEndpoint: ""}
auth: {devAuthEnabled: true}
`
	tests := []struct {
		name string
		yaml string
	}{
		{name: "managed environment", yaml: strings.Replace(base, "managedEnvironment: production\n", "", 1)},
		{name: "server epoch", yaml: strings.Replace(base, "018f47cb-42f8-7fb2-a4af-0b0bd6dd98c1", "not-a-uuid", 1)},
		{name: "MySQL", yaml: strings.Replace(base, `dsn: "finconfig:mysql-secret@tcp(mysql:3306)/finconfig?parseTime=true&loc=UTC&timeout=5s&readTimeout=5s&writeTimeout=5s"`, `dsn: ""`, 1)},
		{name: "backend UDS", yaml: strings.Replace(base, "backendSocket: /var/run/finconfig/backend.sock\n", "", 1)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := config.LoadConfigServer(strings.NewReader(test.yaml), nil); err == nil {
				t.Fatalf("Config Server profile without %s was accepted", test.name)
			}
		})
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
runtimeMode: production
instanceId: prod-1
operationsListenAddress: 0.0.0.0:9090
backendSocket: /var/run/finconfig/backend.sock
shutdownTimeout: 30s
mysql: {dsn: "finconfig:secret@tcp(mysql:3306)/finconfig?parseTime=true&loc=UTC&timeout=5s&readTimeout=5s&writeTimeout=5s", maxOpenConnections: 20, maxIdleConnections: 10, connectionMaxLifetime: 5m, connectionMaxIdleTime: 1m}
telemetry: {traceSampleRatio: 0.1, otlpEndpoint: ""}
auth: {devAuthEnabled: true}
`
	if _, err := config.Load(strings.NewReader(production), nil); err == nil {
		t.Fatal("production development auth accepted")
	}
}

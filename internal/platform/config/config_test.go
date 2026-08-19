package config_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/platform/config"
)

const productionRPCAuthYAML = `
auth:
  devAuthEnabled: false
  consumerJwt:
    issuer: https://identity.example.com
    audience: finconfig-config-server
    jwksUrl: https://identity.example.com/.well-known/jwks.json
    jwksCacheTtl: 5m
    httpTimeout: 3s
  internalJwt:
    issuer: finconfig-control-plane
    audience: finconfig-config-server
    publicKeyFiles:
      relay-2026-08: /run/secrets/finconfig/relay-2026-08.pem
  refreshRelaySubjects: [control-plane-relay]
  additionalPageQueryRoles: [CONFIG_ADMIN]
`

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
	mode, groupID, err := loaded.BackendSocketPermissions()
	if err != nil || mode != 0o660 || groupID != os.Getegid() {
		t.Fatalf("development socket permissions = %#o/%d, %v", mode, groupID, err)
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
backendSocketGroupId: 1000
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
backendSocketGroupId: 1000
shutdownTimeout: 30s
mysql: {dsn: "finconfig:secret@tcp(mysql:3306)/finconfig?parseTime=true&loc=UTC&timeout=5s&readTimeout=5s&writeTimeout=5s", maxOpenConnections: 20, maxIdleConnections: 10, connectionMaxLifetime: 5m, connectionMaxIdleTime: 1m}
telemetry: {traceSampleRatio: 0.1, otlpEndpoint: ""}
auth: {devAuthEnabled: true}
`
	if _, err := config.Load(strings.NewReader(production), nil); err == nil {
		t.Fatal("production development auth accepted")
	}
}

func TestBackendSocketPermissionsAreStrictAndEnvironmentOverridable(t *testing.T) {
	t.Parallel()
	base := `
serviceName: config-server
version: v1
runtimeMode: production
managedEnvironment: production
serverEpoch: 018f47cb-42f8-7fb2-a4af-0b0bd6dd98c1
instanceId: config-server-1
operationsListenAddress: 127.0.0.1:9090
backendSocket: /var/run/finconfig/backend.sock
backendSocketGroupId: 1000
shutdownTimeout: 30s
mysql: {dsn: "finconfig:secret@tcp(mysql:3306)/finconfig?parseTime=true&loc=UTC&timeout=5s&readTimeout=5s&writeTimeout=5s", maxOpenConnections: 20, maxIdleConnections: 10, connectionMaxLifetime: 5m, connectionMaxIdleTime: 1m}
telemetry: {traceSampleRatio: 0.1, otlpEndpoint: ""}
` + productionRPCAuthYAML
	loaded, err := config.LoadConfigServer(strings.NewReader(base), []string{
		"FINCONFIG_BACKEND_SOCKET_MODE=0620",
		"FINCONFIG_BACKEND_SOCKET_GROUP_ID=2000",
	})
	if err != nil {
		t.Fatal(err)
	}
	mode, groupID, err := loaded.BackendSocketPermissions()
	if err != nil || mode != 0o620 || groupID != 2000 {
		t.Fatalf("overridden socket permissions = %#o/%d, %v", mode, groupID, err)
	}

	for _, test := range []struct {
		name string
		yaml string
	}{
		{name: "production missing group", yaml: strings.Replace(base, "backendSocketGroupId: 1000\n", "", 1)},
		{name: "world writable mode", yaml: strings.Replace(base, "backendSocketGroupId: 1000\n", "backendSocketMode: \"0666\"\nbackendSocketGroupId: 1000\n", 1)},
		{name: "negative group", yaml: strings.Replace(base, "backendSocketGroupId: 1000", "backendSocketGroupId: -1", 1)},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := config.LoadConfigServer(strings.NewReader(test.yaml), nil); err == nil {
				t.Fatalf("unsafe backend socket setting %q accepted", test.name)
			}
		})
	}
}

func TestConfigServerProductionRPCAuthenticationIsStrictAndEnvironmentOverridable(t *testing.T) {
	t.Parallel()
	base := `
serviceName: config-server
version: v1
runtimeMode: production
managedEnvironment: production
serverEpoch: 018f47cb-42f8-7fb2-a4af-0b0bd6dd98c1
instanceId: config-server-1
operationsListenAddress: 127.0.0.1:9090
backendSocket: /var/run/finconfig/backend.sock
backendSocketGroupId: 1000
shutdownTimeout: 30s
mysql: {dsn: "finconfig:secret@tcp(mysql:3306)/finconfig?parseTime=true&loc=UTC&timeout=5s&readTimeout=5s&writeTimeout=5s", maxOpenConnections: 20, maxIdleConnections: 10, connectionMaxLifetime: 5m, connectionMaxIdleTime: 1m}
telemetry: {traceSampleRatio: 0.1, otlpEndpoint: ""}
` + productionRPCAuthYAML
	keyFiles, _ := json.Marshal(map[string]string{"next-key": "/run/secrets/finconfig/next.pem"})
	relays, _ := json.Marshal([]string{"relay-b", "relay-a"})
	roles, _ := json.Marshal([]string{"RELEASE_VIEWER"})
	loaded, err := config.LoadConfigServer(strings.NewReader(base), []string{
		"FINCONFIG_AUTH_CONSUMER_ISSUER=https://issuer.example.com",
		"FINCONFIG_AUTH_CONSUMER_AUDIENCE=config-api",
		"FINCONFIG_AUTH_CONSUMER_JWKS_URL=https://issuer.example.com/jwks",
		"FINCONFIG_AUTH_CONSUMER_JWKS_CACHE_TTL=10m",
		"FINCONFIG_AUTH_CONSUMER_HTTP_TIMEOUT=2s",
		"FINCONFIG_AUTH_INTERNAL_ISSUER=relay-issuer",
		"FINCONFIG_AUTH_INTERNAL_AUDIENCE=config-api",
		"FINCONFIG_AUTH_INTERNAL_PUBLIC_KEY_FILES=" + string(keyFiles),
		"FINCONFIG_AUTH_REFRESH_RELAY_SUBJECTS=" + string(relays),
		"FINCONFIG_AUTH_ADDITIONAL_PAGE_QUERY_ROLES=" + string(roles),
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Auth.ConsumerJWT.Issuer != "https://issuer.example.com" || loaded.Auth.ConsumerJWT.JWKSCacheTTL.Duration != 10*time.Minute || loaded.Auth.InternalJWT.PublicKeyFiles["next-key"] != "/run/secrets/finconfig/next.pem" {
		t.Fatalf("authentication overrides = %+v", loaded.Auth)
	}
	if len(loaded.Auth.RefreshRelaySubjects) != 2 || len(loaded.Auth.AdditionalPageQueryRoles) != 1 {
		t.Fatalf("authentication policy overrides = %+v", loaded.Auth)
	}
	summary := loaded.SafeSummary()
	for _, secret := range []string{"identity.example.com", "issuer.example.com", "next.pem", "relay-a", "RELEASE_VIEWER"} {
		if strings.Contains(summary, secret) {
			t.Fatalf("safe summary leaked %q: %s", secret, summary)
		}
	}
	for _, expected := range []string{`"consumerJwtConfigured":true`, `"internalPublicKeyCount":1`, `"refreshRelaySubjectCount":2`} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("safe summary missing %s: %s", expected, summary)
		}
	}
	incomplete := loaded
	incomplete.Auth.InternalJWT.PublicKeyFiles = nil
	if summary := incomplete.SafeSummary(); strings.Contains(summary, `"rpcAuthConfigured":true`) || strings.Contains(summary, `"internalJwtConfigured":true`) {
		t.Fatalf("incomplete auth was summarized as configured: %s", summary)
	}
}

func TestConfigServerProductionRejectsIncompleteOrUnsafeRPCAuthentication(t *testing.T) {
	t.Parallel()
	base := `
serviceName: config-server
version: v1
runtimeMode: production
managedEnvironment: production
serverEpoch: 018f47cb-42f8-7fb2-a4af-0b0bd6dd98c1
instanceId: config-server-1
operationsListenAddress: 127.0.0.1:9090
backendSocket: /var/run/finconfig/backend.sock
backendSocketGroupId: 1000
shutdownTimeout: 30s
mysql: {dsn: "finconfig:secret@tcp(mysql:3306)/finconfig?parseTime=true&loc=UTC&timeout=5s&readTimeout=5s&writeTimeout=5s", maxOpenConnections: 20, maxIdleConnections: 10, connectionMaxLifetime: 5m, connectionMaxIdleTime: 1m}
telemetry: {traceSampleRatio: 0.1, otlpEndpoint: ""}
` + productionRPCAuthYAML
	tests := []struct {
		name string
		yaml string
	}{
		{name: "missing consumer issuer", yaml: strings.Replace(base, "    issuer: https://identity.example.com\n", "", 1)},
		{name: "insecure JWKS", yaml: strings.Replace(base, "https://identity.example.com/.well-known/jwks.json", "http://identity.example.com/jwks", 1)},
		{name: "unbounded JWKS TTL", yaml: strings.Replace(base, "    jwksCacheTtl: 5m", "    jwksCacheTtl: 25h", 1)},
		{name: "unbounded HTTP timeout", yaml: strings.Replace(base, "    httpTimeout: 3s", "    httpTimeout: 31s", 1)},
		{name: "relative key path", yaml: strings.Replace(base, "/run/secrets/finconfig/relay-2026-08.pem", "relay.pem", 1)},
		{name: "missing relay", yaml: strings.Replace(base, "  refreshRelaySubjects: [control-plane-relay]", "  refreshRelaySubjects: []", 1)},
		{name: "duplicate role", yaml: strings.Replace(base, "[CONFIG_ADMIN]", "[CONFIG_ADMIN, CONFIG_ADMIN]", 1)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := config.LoadConfigServer(strings.NewReader(test.yaml), nil); err == nil {
				t.Fatalf("unsafe authentication setting %q accepted", test.name)
			}
		})
	}
	if _, err := config.LoadConfigServer(strings.NewReader(base), []string{"FINCONFIG_AUTH_ADDITIONAL_PAGE_QUERY_ROLES=null"}); err == nil {
		t.Fatal("JSON null was accepted where an auth role array is required")
	}
}

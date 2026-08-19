package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	platformmysql "github.com/asherzj/financial_configuration_center/internal/platform/mysql"
	platformrpc "github.com/asherzj/financial_configuration_center/internal/platform/rpc"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

type Duration struct{ time.Duration }

func (duration *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return errors.New("duration must be a string")
	}
	parsed, err := time.ParseDuration(node.Value)
	if err != nil {
		return err
	}
	duration.Duration = parsed
	return nil
}

type ServiceConfig struct {
	ServiceName             string          `yaml:"serviceName"`
	Version                 string          `yaml:"version"`
	RuntimeMode             string          `yaml:"runtimeMode"`
	InstanceID              string          `yaml:"instanceId"`
	OperationsListenAddress string          `yaml:"operationsListenAddress"`
	BackendSocket           string          `yaml:"backendSocket"`
	BackendSocketMode       string          `yaml:"backendSocketMode"`
	BackendSocketGroupID    *int            `yaml:"backendSocketGroupId"`
	ShutdownTimeout         Duration        `yaml:"shutdownTimeout"`
	MySQL                   MySQLConfig     `yaml:"mysql"`
	Telemetry               TelemetryConfig `yaml:"telemetry"`
	Auth                    AuthConfig      `yaml:"auth"`
}

type ConfigServerConfig struct {
	ServiceConfig      `yaml:",inline"`
	ManagedEnvironment string `yaml:"managedEnvironment"`
	ServerEpoch        string `yaml:"serverEpoch"`
}

type MySQLConfig struct {
	DSN                   string   `yaml:"dsn"`
	MaxOpenConnections    int      `yaml:"maxOpenConnections"`
	MaxIdleConnections    int      `yaml:"maxIdleConnections"`
	ConnectionMaxLifetime Duration `yaml:"connectionMaxLifetime"`
	ConnectionMaxIdleTime Duration `yaml:"connectionMaxIdleTime"`
}

type TelemetryConfig struct {
	TraceSampleRatio float64 `yaml:"traceSampleRatio"`
	OTLPEndpoint     string  `yaml:"otlpEndpoint"`
}

type AuthConfig struct {
	DevAuthEnabled           bool              `yaml:"devAuthEnabled"`
	ConsumerJWT              ConsumerJWTConfig `yaml:"consumerJwt"`
	InternalJWT              InternalJWTConfig `yaml:"internalJwt"`
	RefreshRelaySubjects     []string          `yaml:"refreshRelaySubjects"`
	AdditionalPageQueryRoles []string          `yaml:"additionalPageQueryRoles"`
}

type ConsumerJWTConfig struct {
	Issuer       string   `yaml:"issuer"`
	Audience     string   `yaml:"audience"`
	JWKSURL      string   `yaml:"jwksUrl"`
	JWKSCacheTTL Duration `yaml:"jwksCacheTtl"`
	HTTPTimeout  Duration `yaml:"httpTimeout"`
}

type InternalJWTConfig struct {
	Issuer         string            `yaml:"issuer"`
	Audience       string            `yaml:"audience"`
	PublicKeyFiles map[string]string `yaml:"publicKeyFiles"`
}

func Load(reader io.Reader, environment []string) (ServiceConfig, error) {
	config := defaults()
	if err := decodeStrict(reader, &config); err != nil {
		return ServiceConfig{}, err
	}
	if err := applyEnvironment(&config, environment); err != nil {
		return ServiceConfig{}, err
	}
	if err := config.Validate(); err != nil {
		return ServiceConfig{}, err
	}
	return config, nil
}

func LoadConfigServer(reader io.Reader, environment []string) (ConfigServerConfig, error) {
	config := ConfigServerConfig{ServiceConfig: defaults()}
	if err := decodeStrict(reader, &config); err != nil {
		return ConfigServerConfig{}, err
	}
	if err := applyEnvironment(&config.ServiceConfig, environment); err != nil {
		return ConfigServerConfig{}, err
	}
	if err := applyConfigServerEnvironment(&config, environment); err != nil {
		return ConfigServerConfig{}, err
	}
	if err := config.Validate(); err != nil {
		return ConfigServerConfig{}, err
	}
	return config, nil
}

func defaults() ServiceConfig {
	return ServiceConfig{
		RuntimeMode: "development", OperationsListenAddress: "127.0.0.1:9090", BackendSocketMode: "0660",
		ShutdownTimeout: Duration{30 * time.Second},
		MySQL:           MySQLConfig{MaxOpenConnections: 20, MaxIdleConnections: 10, ConnectionMaxLifetime: Duration{5 * time.Minute}, ConnectionMaxIdleTime: Duration{time.Minute}},
		Telemetry:       TelemetryConfig{TraceSampleRatio: 0.1},
	}
}

func (config ServiceConfig) Validate() error {
	if strings.TrimSpace(config.ServiceName) == "" || strings.TrimSpace(config.Version) == "" || strings.TrimSpace(config.InstanceID) == "" {
		return errors.New("service name, version, and instance ID are required")
	}
	switch config.RuntimeMode {
	case "development", "test", "production":
	default:
		return errors.New("runtime mode must be development, test, or production")
	}
	if config.ShutdownTimeout.Duration <= 0 || config.ShutdownTimeout.Duration > 5*time.Minute {
		return errors.New("shutdown timeout must be positive and at most five minutes")
	}
	if _, _, err := net.SplitHostPort(config.OperationsListenAddress); err != nil {
		return errors.New("operations listen address must include a valid host and port")
	}
	if config.BackendSocket != "" {
		if err := platformrpc.ValidatePrivateBackendPath(config.BackendSocket); err != nil {
			return err
		}
		if config.RuntimeMode == "production" && config.BackendSocketGroupID == nil {
			return errors.New("production backend Unix socket group ID is required")
		}
		if _, _, err := config.BackendSocketPermissions(); err != nil {
			return err
		}
	}
	if config.MySQL.DSN != "" {
		if err := config.MySQL.DatabaseConfig().Validate(); err != nil {
			return err
		}
	}
	if config.Telemetry.TraceSampleRatio < 0 || config.Telemetry.TraceSampleRatio > 1 {
		return errors.New("trace sample ratio must be between zero and one")
	}
	if config.Telemetry.OTLPEndpoint != "" {
		endpoint, err := url.Parse(config.Telemetry.OTLPEndpoint)
		if err != nil || endpoint.Host == "" || endpoint.Scheme != "http" && endpoint.Scheme != "https" {
			return errors.New("OTLP endpoint must be an absolute HTTP(S) URL")
		}
		if config.RuntimeMode == "production" && endpoint.Scheme != "https" {
			return errors.New("production OTLP endpoint must use HTTPS")
		}
	}
	if config.RuntimeMode == "production" && config.Auth.DevAuthEnabled {
		return errors.New("development authentication cannot be enabled in production")
	}
	return nil
}

func (config ConfigServerConfig) Validate() error {
	if err := config.ServiceConfig.Validate(); err != nil {
		return err
	}
	if config.ServiceName != "config-server" {
		return errors.New("Config Server service name must be config-server")
	}
	if config.ManagedEnvironment == "" || config.ManagedEnvironment != strings.TrimSpace(config.ManagedEnvironment) {
		return errors.New("Config Server managed environment is required without surrounding whitespace")
	}
	epoch, err := uuid.Parse(config.ServerEpoch)
	if err != nil || epoch == uuid.Nil {
		return errors.New("Config Server server epoch must be a non-zero UUID")
	}
	if config.MySQL.DSN == "" {
		return errors.New("Config Server MySQL DSN is required")
	}
	if config.BackendSocket == "" {
		return errors.New("Config Server backend Unix socket is required")
	}
	if config.RuntimeMode == "production" {
		if err := config.Auth.ValidateRPC(); err != nil {
			return fmt.Errorf("Config Server production RPC authentication: %w", err)
		}
	}
	return nil
}

func (config AuthConfig) ValidateRPC() error {
	if config.DevAuthEnabled {
		return errors.New("development authentication is not a production RPC profile")
	}
	if err := exactConfigValue("Consumer JWT issuer", config.ConsumerJWT.Issuer); err != nil {
		return err
	}
	issuer, err := url.Parse(config.ConsumerJWT.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.Fragment != "" {
		return errors.New("Consumer JWT issuer must be an absolute HTTPS URL")
	}
	if err := exactConfigValue("Consumer JWT audience", config.ConsumerJWT.Audience); err != nil {
		return err
	}
	if err := exactConfigValue("Consumer JWT JWKS URL", config.ConsumerJWT.JWKSURL); err != nil {
		return err
	}
	jwksURL, err := url.Parse(config.ConsumerJWT.JWKSURL)
	if err != nil || jwksURL.Scheme != "https" || jwksURL.Host == "" || jwksURL.User != nil || jwksURL.Fragment != "" {
		return errors.New("Consumer JWT JWKS URL must be an absolute HTTPS URL")
	}
	if config.ConsumerJWT.JWKSCacheTTL.Duration <= 0 || config.ConsumerJWT.JWKSCacheTTL.Duration > 24*time.Hour {
		return errors.New("Consumer JWT JWKS cache TTL must be positive and at most 24 hours")
	}
	if config.ConsumerJWT.HTTPTimeout.Duration <= 0 || config.ConsumerJWT.HTTPTimeout.Duration > 30*time.Second {
		return errors.New("Consumer JWT HTTP timeout must be positive and at most 30 seconds")
	}
	if err := exactConfigValue("Internal JWT issuer", config.InternalJWT.Issuer); err != nil {
		return err
	}
	if err := exactConfigValue("Internal JWT audience", config.InternalJWT.Audience); err != nil {
		return err
	}
	if len(config.InternalJWT.PublicKeyFiles) == 0 || len(config.InternalJWT.PublicKeyFiles) > 32 {
		return errors.New("Internal JWT public key ring must contain between one and 32 keys")
	}
	for keyID, path := range config.InternalJWT.PublicKeyFiles {
		if err := exactConfigValue("Internal JWT key ID", keyID); err != nil || len(keyID) > 128 || strings.ContainsAny(keyID, " \t\r\n") {
			return errors.New("Internal JWT key IDs must be non-empty bounded values without whitespace")
		}
		if path == "" || path != strings.TrimSpace(path) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return errors.New("Internal JWT public key file paths must be clean absolute paths")
		}
	}
	if err := exactConfigList("refresh relay subjects", config.RefreshRelaySubjects, true); err != nil {
		return err
	}
	return exactConfigList("additional PageQuery roles", config.AdditionalPageQueryRoles, false)
}

func exactConfigValue(name, value string) error {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 512 {
		return fmt.Errorf("%s must be a non-empty bounded value without surrounding whitespace", name)
	}
	return nil
}

func exactConfigList(name string, values []string, required bool) error {
	if required && len(values) == 0 {
		return fmt.Errorf("%s must contain at least one value", name)
	}
	if len(values) > 128 {
		return fmt.Errorf("%s may contain at most 128 values", name)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := exactConfigValue(name, value); err != nil {
			return err
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%s contains a duplicate value", name)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func (config MySQLConfig) DatabaseConfig() platformmysql.Config {
	return platformmysql.Config{
		DSN: config.DSN, MaxOpenConns: config.MaxOpenConnections, MaxIdleConns: config.MaxIdleConnections,
		ConnMaxLifetime: config.ConnectionMaxLifetime.Duration, ConnMaxIdleTime: config.ConnectionMaxIdleTime.Duration,
	}
}

func (config ServiceConfig) SafeSummary() string {
	rpcAuthConfigured := config.Auth.ValidateRPC() == nil
	summary := map[string]any{
		"service": config.ServiceName, "version": config.Version, "runtimeMode": config.RuntimeMode, "instanceId": config.InstanceID,
		"operationsListenAddress": config.OperationsListenAddress, "backendSocket": config.BackendSocket,
		"backendSocketMode": config.BackendSocketMode, "backendSocketGroupConfigured": config.BackendSocketGroupID != nil,
		"shutdownTimeout": config.ShutdownTimeout.String(), "mysqlConfigured": config.MySQL.DSN != "",
		"mysqlMaxOpenConnections": config.MySQL.MaxOpenConnections, "traceSampleRatio": config.Telemetry.TraceSampleRatio,
		"otlpConfigured": config.Telemetry.OTLPEndpoint != "", "devAuthEnabled": config.Auth.DevAuthEnabled,
		"rpcAuthConfigured": rpcAuthConfigured, "consumerJwtConfigured": rpcAuthConfigured,
		"consumerIssuerHash": safeConfigHash(config.Auth.ConsumerJWT.Issuer), "consumerAudienceHash": safeConfigHash(config.Auth.ConsumerJWT.Audience),
		"consumerJwksUrlHash": safeConfigHash(config.Auth.ConsumerJWT.JWKSURL), "internalJwtConfigured": rpcAuthConfigured,
		"internalIssuerHash": safeConfigHash(config.Auth.InternalJWT.Issuer), "internalAudienceHash": safeConfigHash(config.Auth.InternalJWT.Audience),
		"internalPublicKeyCount": len(config.Auth.InternalJWT.PublicKeyFiles), "refreshRelaySubjectCount": len(config.Auth.RefreshRelaySubjects),
		"additionalPageQueryRoleCount": len(config.Auth.AdditionalPageQueryRoles),
	}
	encoded, _ := json.Marshal(summary)
	return string(encoded)
}

func safeConfigHash(value string) string {
	if value == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", digest[:8])
}

func (config ConfigServerConfig) SafeSummary() string {
	var summary map[string]any
	_ = json.Unmarshal([]byte(config.ServiceConfig.SafeSummary()), &summary)
	summary["managedEnvironment"] = config.ManagedEnvironment
	summary["serverEpoch"] = config.ServerEpoch
	encoded, _ := json.Marshal(summary)
	return string(encoded)
}

func applyEnvironment(config *ServiceConfig, values []string) error {
	environment := environmentMap(values)
	stringOverrides := map[string]*string{
		"FINCONFIG_SERVICE_NAME": &config.ServiceName, "FINCONFIG_VERSION": &config.Version,
		"FINCONFIG_RUNTIME_MODE": &config.RuntimeMode, "FINCONFIG_INSTANCE_ID": &config.InstanceID,
		"FINCONFIG_OPERATIONS_LISTEN_ADDRESS": &config.OperationsListenAddress, "FINCONFIG_BACKEND_SOCKET": &config.BackendSocket,
		"FINCONFIG_BACKEND_SOCKET_MODE": &config.BackendSocketMode,
		"FINCONFIG_MYSQL_DSN":           &config.MySQL.DSN, "FINCONFIG_OTLP_ENDPOINT": &config.Telemetry.OTLPEndpoint,
		"FINCONFIG_AUTH_CONSUMER_ISSUER":   &config.Auth.ConsumerJWT.Issuer,
		"FINCONFIG_AUTH_CONSUMER_AUDIENCE": &config.Auth.ConsumerJWT.Audience,
		"FINCONFIG_AUTH_CONSUMER_JWKS_URL": &config.Auth.ConsumerJWT.JWKSURL,
		"FINCONFIG_AUTH_INTERNAL_ISSUER":   &config.Auth.InternalJWT.Issuer,
		"FINCONFIG_AUTH_INTERNAL_AUDIENCE": &config.Auth.InternalJWT.Audience,
	}
	for name, target := range stringOverrides {
		if value, exists := environment[name]; exists {
			*target = value
		}
	}
	if value, exists := environment["FINCONFIG_SHUTDOWN_TIMEOUT"]; exists {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("parse FINCONFIG_SHUTDOWN_TIMEOUT: %w", err)
		}
		config.ShutdownTimeout.Duration = parsed
	}
	if value, exists := environment["FINCONFIG_BACKEND_SOCKET_GROUP_ID"]; exists {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("parse FINCONFIG_BACKEND_SOCKET_GROUP_ID: %w", err)
		}
		config.BackendSocketGroupID = &parsed
	}
	for name, target := range map[string]*int{"FINCONFIG_MYSQL_MAX_OPEN_CONNECTIONS": &config.MySQL.MaxOpenConnections, "FINCONFIG_MYSQL_MAX_IDLE_CONNECTIONS": &config.MySQL.MaxIdleConnections} {
		if value, exists := environment[name]; exists {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			*target = parsed
		}
	}
	for name, target := range map[string]*Duration{"FINCONFIG_MYSQL_CONNECTION_MAX_LIFETIME": &config.MySQL.ConnectionMaxLifetime, "FINCONFIG_MYSQL_CONNECTION_MAX_IDLE_TIME": &config.MySQL.ConnectionMaxIdleTime} {
		if value, exists := environment[name]; exists {
			parsed, err := time.ParseDuration(value)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			target.Duration = parsed
		}
	}
	if value, exists := environment["FINCONFIG_TRACE_SAMPLE_RATIO"]; exists {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("parse FINCONFIG_TRACE_SAMPLE_RATIO: %w", err)
		}
		config.Telemetry.TraceSampleRatio = parsed
	}
	if value, exists := environment["FINCONFIG_DEV_AUTH_ENABLED"]; exists {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("parse FINCONFIG_DEV_AUTH_ENABLED: %w", err)
		}
		config.Auth.DevAuthEnabled = parsed
	}
	for name, target := range map[string]*Duration{
		"FINCONFIG_AUTH_CONSUMER_JWKS_CACHE_TTL": &config.Auth.ConsumerJWT.JWKSCacheTTL,
		"FINCONFIG_AUTH_CONSUMER_HTTP_TIMEOUT":   &config.Auth.ConsumerJWT.HTTPTimeout,
	} {
		if value, exists := environment[name]; exists {
			parsed, err := time.ParseDuration(value)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			target.Duration = parsed
		}
	}
	if value, exists := environment["FINCONFIG_AUTH_INTERNAL_PUBLIC_KEY_FILES"]; exists {
		parsed, err := decodeStringMapJSON(value)
		if err != nil {
			return fmt.Errorf("parse FINCONFIG_AUTH_INTERNAL_PUBLIC_KEY_FILES: %w", err)
		}
		config.Auth.InternalJWT.PublicKeyFiles = parsed
	}
	for name, target := range map[string]*[]string{
		"FINCONFIG_AUTH_REFRESH_RELAY_SUBJECTS":      &config.Auth.RefreshRelaySubjects,
		"FINCONFIG_AUTH_ADDITIONAL_PAGE_QUERY_ROLES": &config.Auth.AdditionalPageQueryRoles,
	} {
		if value, exists := environment[name]; exists {
			parsed, err := decodeStringSliceJSON(value)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			*target = parsed
		}
	}
	return nil
}

func decodeStringSliceJSON(value string) ([]string, error) {
	var result []string
	if err := json.Unmarshal([]byte(value), &result); err != nil || result == nil {
		return nil, errors.New("value must be a JSON array of strings")
	}
	return result, nil
}

func decodeStringMapJSON(value string) (map[string]string, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("value must be a JSON object")
	}
	result := make(map[string]string)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("JSON object key must be a string")
		}
		if _, duplicate := result[key]; duplicate {
			return nil, fmt.Errorf("duplicate JSON object key %q", key)
		}
		var path string
		if err := decoder.Decode(&path); err != nil {
			return nil, err
		}
		result[key] = path
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, errors.New("value must end after its JSON object")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("value must contain exactly one JSON object")
	}
	return result, nil
}

func (config ServiceConfig) BackendSocketPermissions() (os.FileMode, int, error) {
	mode, err := platformrpc.ParsePrivateBackendMode(config.BackendSocketMode)
	if err != nil {
		return 0, 0, err
	}
	groupID := os.Getegid()
	if config.BackendSocketGroupID != nil {
		groupID = *config.BackendSocketGroupID
	}
	if err := platformrpc.ValidatePrivateBackendPermissions(mode, groupID); err != nil {
		return 0, 0, err
	}
	return mode, groupID, nil
}

func applyConfigServerEnvironment(config *ConfigServerConfig, values []string) error {
	environment := environmentMap(values)
	if value, exists := environment["FINCONFIG_MANAGED_ENVIRONMENT"]; exists {
		config.ManagedEnvironment = value
	}
	if value, exists := environment["FINCONFIG_SERVER_EPOCH"]; exists {
		config.ServerEpoch = value
	}
	return nil
}

func environmentMap(values []string) map[string]string {
	environment := make(map[string]string, len(values))
	for _, entry := range values {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 2 {
			environment[parts[0]] = parts[1]
		}
	}
	return environment
}

func decodeStrict(reader io.Reader, target any) error {
	if reader == nil {
		return nil
	}
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("load FinConfig YAML: %w", err)
	}
	return rejectTrailingYAML(decoder)
}

func rejectTrailingYAML(decoder *yaml.Decoder) error {
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("load trailing FinConfig YAML: %w", err)
	}
	if len(bytes.TrimSpace([]byte(trailing.Value))) > 0 || len(trailing.Content) > 0 {
		return errors.New("FinConfig YAML must contain exactly one document")
	}
	return nil
}

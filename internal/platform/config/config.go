package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	platformmysql "github.com/asherzj/financial_configuration_center/internal/platform/mysql"
	platformrpc "github.com/asherzj/financial_configuration_center/internal/platform/rpc"
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
	Environment             string          `yaml:"environment"`
	InstanceID              string          `yaml:"instanceId"`
	OperationsListenAddress string          `yaml:"operationsListenAddress"`
	BackendSocket           string          `yaml:"backendSocket"`
	ShutdownTimeout         Duration        `yaml:"shutdownTimeout"`
	MySQL                   MySQLConfig     `yaml:"mysql"`
	Telemetry               TelemetryConfig `yaml:"telemetry"`
	Auth                    AuthConfig      `yaml:"auth"`
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
	DevAuthEnabled bool `yaml:"devAuthEnabled"`
}

func Load(reader io.Reader, environment []string) (ServiceConfig, error) {
	config := defaults()
	if reader != nil {
		decoder := yaml.NewDecoder(reader)
		decoder.KnownFields(true)
		if err := decoder.Decode(&config); err != nil {
			return ServiceConfig{}, fmt.Errorf("load FinConfig YAML: %w", err)
		}
		if err := rejectTrailingYAML(decoder); err != nil {
			return ServiceConfig{}, err
		}
	}
	if err := applyEnvironment(&config, environment); err != nil {
		return ServiceConfig{}, err
	}
	if err := config.Validate(); err != nil {
		return ServiceConfig{}, err
	}
	return config, nil
}

func defaults() ServiceConfig {
	return ServiceConfig{
		Environment: "development", OperationsListenAddress: "127.0.0.1:9090",
		ShutdownTimeout: Duration{30 * time.Second},
		MySQL:           MySQLConfig{MaxOpenConnections: 20, MaxIdleConnections: 10, ConnectionMaxLifetime: Duration{5 * time.Minute}, ConnectionMaxIdleTime: Duration{time.Minute}},
		Telemetry:       TelemetryConfig{TraceSampleRatio: 0.1},
	}
}

func (config ServiceConfig) Validate() error {
	if strings.TrimSpace(config.ServiceName) == "" || strings.TrimSpace(config.Version) == "" || strings.TrimSpace(config.InstanceID) == "" {
		return errors.New("service name, version, and instance ID are required")
	}
	switch config.Environment {
	case "development", "test", "production":
	default:
		return errors.New("environment must be development, test, or production")
	}
	if config.ShutdownTimeout.Duration <= 0 || config.ShutdownTimeout.Duration > 5*time.Minute {
		return errors.New("shutdown timeout must be positive and at most five minutes")
	}
	if _, _, err := net.SplitHostPort(config.OperationsListenAddress); err != nil {
		return errors.New("operations listen address must include a valid host and port")
	}
	if config.BackendSocket != "" {
		if _, err := platformrpc.PrivateBackendServerOptions(config.BackendSocket); err != nil {
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
		if config.Environment == "production" && endpoint.Scheme != "https" {
			return errors.New("production OTLP endpoint must use HTTPS")
		}
	}
	if config.Environment == "production" && config.Auth.DevAuthEnabled {
		return errors.New("development authentication cannot be enabled in production")
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
	summary := map[string]any{
		"service": config.ServiceName, "version": config.Version, "environment": config.Environment, "instanceId": config.InstanceID,
		"operationsListenAddress": config.OperationsListenAddress, "backendSocket": config.BackendSocket,
		"shutdownTimeout": config.ShutdownTimeout.String(), "mysqlConfigured": config.MySQL.DSN != "",
		"mysqlMaxOpenConnections": config.MySQL.MaxOpenConnections, "traceSampleRatio": config.Telemetry.TraceSampleRatio,
		"otlpConfigured": config.Telemetry.OTLPEndpoint != "", "devAuthEnabled": config.Auth.DevAuthEnabled,
	}
	encoded, _ := json.Marshal(summary)
	return string(encoded)
}

func applyEnvironment(config *ServiceConfig, values []string) error {
	environment := make(map[string]string, len(values))
	for _, entry := range values {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 2 {
			environment[parts[0]] = parts[1]
		}
	}
	stringOverrides := map[string]*string{
		"FINCONFIG_SERVICE_NAME": &config.ServiceName, "FINCONFIG_VERSION": &config.Version,
		"FINCONFIG_ENVIRONMENT": &config.Environment, "FINCONFIG_INSTANCE_ID": &config.InstanceID,
		"FINCONFIG_OPERATIONS_LISTEN_ADDRESS": &config.OperationsListenAddress, "FINCONFIG_BACKEND_SOCKET": &config.BackendSocket,
		"FINCONFIG_MYSQL_DSN": &config.MySQL.DSN, "FINCONFIG_OTLP_ENDPOINT": &config.Telemetry.OTLPEndpoint,
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
	return nil
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

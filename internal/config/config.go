package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultConfigPath is the default global configuration filename created by `obsynx init`.
const DefaultConfigPath = "obsynx.yaml"

// MaxCustomMethodsPerService bounds CPU/latency impact from excessive method hooks (Section 9.3).
const MaxCustomMethodsPerService = 25

// MaxAllowedAttributesPerMethod prevents unbounded cardinality from method arguments/returns (Section 9.3).
const MaxAllowedAttributesPerMethod = 5

var forbiddenSensitiveAttributeTokens = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"authorization",
	"api_key",
	"apikey",
	"credit_card",
	"card_number",
	"cvv",
	"ssn",
	"cpf",
	"email",
}

// Config represents the global Control Plane configuration (`obsynx.yaml` at workspace root).
// Individual service instrumentation lives in each project's own `obsynx.yaml` and is discovered automatically.
type Config struct {
	Version   string          `yaml:"version" json:"version"`
	Collector CollectorConfig `yaml:"collector" json:"collector"`
	Policies  PolicyConfig    `yaml:"policies" json:"policies"`
	Services  []ServiceConfig `yaml:"services,omitempty" json:"services,omitempty"`
}

// ProjectFile represents a per-application `obsynx.yaml` located inside a project folder (Section 4.2 & 9.4).
type ProjectFile struct {
	Service struct {
		Name    string `yaml:"name" json:"name"`
		Runtime string `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	} `yaml:"service" json:"service"`
	Instrumentation InstrumentationConfig `yaml:"instrumentation" json:"instrumentation"`
}

// CollectorConfig defines the OpenTelemetry Collector endpoint and optional production TLS/Auth settings.
type CollectorConfig struct {
	OTLPEndpoint  string `yaml:"otlp_endpoint" json:"otlp_endpoint"`
	Protocol      string `yaml:"protocol" json:"protocol"`
	TLSEnabled    bool   `yaml:"tls_enabled,omitempty" json:"tls_enabled,omitempty"`
	CACertPath    string `yaml:"ca_cert_path,omitempty" json:"ca_cert_path,omitempty"`
	AuthHeaderEnv string `yaml:"auth_header_env,omitempty" json:"auth_header_env,omitempty"`
}

// PolicyConfig defines Cost-Aware and Security policies (Sections 10 & 11).
type PolicyConfig struct {
	Sampling   SamplingPolicy   `yaml:"sampling" json:"sampling"`
	Attributes AttributesPolicy `yaml:"attributes" json:"attributes"`
	Limits     LimitsPolicy     `yaml:"limits" json:"limits"`
	Collection CollectionPolicy `yaml:"collection" json:"collection"`
}

// SamplingPolicy configures trace sampling ratio.
type SamplingPolicy struct {
	Strategy string  `yaml:"strategy" json:"strategy"`
	Ratio    float64 `yaml:"ratio" json:"ratio"`
}

// AttributesPolicy controls allowed and denied/redacted telemetry attributes.
type AttributesPolicy struct {
	Allow []string `yaml:"allow" json:"allow"`
	Deny  []string `yaml:"deny" json:"deny"`
}

// LimitsPolicy bounds span cardinality.
type LimitsPolicy struct {
	MaxAttributesPerSpan int `yaml:"max_attributes_per_span" json:"max_attributes_per_span"`
	MaxEventsPerSpan     int `yaml:"max_events_per_span" json:"max_events_per_span"`
}

// CollectionPolicy defines noisy routes to drop.
type CollectionPolicy struct {
	ExcludePaths []string `yaml:"exclude_paths" json:"exclude_paths"`
}

// ServiceConfig declares desired instrumentation for a specific application service (loaded from project obsynx.yaml).
type ServiceConfig struct {
	Name            string                `yaml:"name" json:"name"`
	Runtime         string                `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	SourceFile      string                `yaml:"source_file,omitempty" json:"source_file,omitempty"`
	Instrumentation InstrumentationConfig `yaml:"instrumentation" json:"instrumentation"`
}

// InstrumentationConfig declares automatic and custom method instrumentation rules (Section 4.2 & 9).
type InstrumentationConfig struct {
	Automatic bool         `yaml:"automatic" json:"automatic"`
	Methods   []MethodRule `yaml:"methods,omitempty" json:"methods,omitempty"`
}

// MethodMetricSpec defines a business metric attached to a function/method (Section 9.4),
// including optional extraction of input arguments or return values (`value_from`, `attributes_from`).
type MethodMetricSpec struct {
	Name           string            `yaml:"name,omitempty" json:"name,omitempty"`
	Type           string            `yaml:"type" json:"type"` // counter, histogram, updowncounter, gauge
	Unit           string            `yaml:"unit,omitempty" json:"unit,omitempty"`
	Description    string            `yaml:"description,omitempty" json:"description,omitempty"`
	ValueFrom      string            `yaml:"value_from,omitempty" json:"value_from,omitempty"`           // e.g. "1", "duration_ms", "arg.amount", "arg.1", "return", "return.risk_score"
	AttributesFrom map[string]string `yaml:"attributes_from,omitempty" json:"attributes_from,omitempty"` // e.g. {"payment_status": "return.status"}
}

// UnmarshalYAML allows MethodMetricSpec to be parsed from either a string ("counter") or a YAML map.
func (m *MethodMetricSpec) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		m.Type = strings.ToLower(strings.TrimSpace(value.Value))
		return nil
	}
	type plain MethodMetricSpec
	var raw plain
	if err := value.Decode(&raw); err != nil {
		return err
	}
	raw.Type = strings.ToLower(strings.TrimSpace(raw.Type))
	*m = MethodMetricSpec(raw)
	return nil
}

// MethodRule specifies a business domain method to observe when supported by the runtime adapter.
type MethodRule struct {
	Class             string             `yaml:"class,omitempty" json:"class,omitempty"`
	Method            string             `yaml:"method" json:"method"`
	Tracing           bool               `yaml:"tracing" json:"tracing"`
	Metrics           []MethodMetricSpec `yaml:"metrics,omitempty" json:"metrics,omitempty"`
	CaptureArguments  bool               `yaml:"capture_arguments,omitempty" json:"capture_arguments,omitempty"`
	CaptureReturn     bool               `yaml:"capture_return,omitempty" json:"capture_return,omitempty"`
	AllowedAttributes []string           `yaml:"allowed_attributes,omitempty" json:"allowed_attributes,omitempty"`
}

// DefaultConfig returns a clean global Control Plane configuration (no hardcoded per-API configs).
func DefaultConfig() Config {
	return Config{
		Version: "0.2",
		Collector: CollectorConfig{
			OTLPEndpoint: "http://otel-collector:4317",
			Protocol:     "grpc",
		},
		Policies: PolicyConfig{
			Sampling: SamplingPolicy{
				Strategy: "parent-based",
				Ratio:    0.10,
			},
			Attributes: AttributesPolicy{
				Allow: []string{"service.name", "http.request.method", "http.response.status_code", "payment_status"},
				Deny:  []string{"enduser.id", "user.email", "authorization"},
			},
			Limits: LimitsPolicy{
				MaxAttributesPerSpan: 32,
				MaxEventsPerSpan:     64,
			},
			Collection: CollectionPolicy{
				ExcludePaths: []string{"/health", "/ready"},
			},
		},
	}
}

// Load reads and validates the global OBSynx configuration file and automatically discovers
// per-project `examples/*/obsynx.yaml` files in the workspace.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	rootDir := filepath.Dir(path)
	if err := MergeProjectConfigs(rootDir, &cfg); err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// LoadProjectConfig parses a single project's `obsynx.yaml` file (Section 4.2 & 9.4 format).
func LoadProjectConfig(path string) (ServiceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ServiceConfig{}, err
	}
	var pf ProjectFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return ServiceConfig{}, fmt.Errorf("parse project config %s: %w", path, err)
	}
	if strings.TrimSpace(pf.Service.Name) == "" {
		return ServiceConfig{}, fmt.Errorf("project config %s: service.name is required", path)
	}
	svc := ServiceConfig{
		Name:            pf.Service.Name,
		Runtime:         pf.Service.Runtime,
		SourceFile:      path,
		Instrumentation: pf.Instrumentation,
	}
	if err := validateServiceMethods(svc.Name, svc.Instrumentation.Methods); err != nil {
		return ServiceConfig{}, err
	}
	if err := ValidateProjectSource(filepath.Dir(path), svc); err != nil {
		return ServiceConfig{}, err
	}
	return svc, nil
}

// MergeProjectConfigs scans `examples/*/obsynx.yaml` and populates `cfg.Services` from each project's own YAML.
func MergeProjectConfigs(rootDir string, cfg *Config) error {
	examplesDir := filepath.Join(rootDir, "examples")
	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		return nil
	}

	byName := make(map[string]ServiceConfig, len(cfg.Services))
	for _, s := range cfg.Services {
		byName[s.Name] = s
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projFile := filepath.Join(examplesDir, entry.Name(), "obsynx.yaml")
		if _, statErr := os.Stat(projFile); statErr != nil {
			continue
		}
		svcCfg, loadErr := LoadProjectConfig(projFile)
		if loadErr != nil {
			return loadErr
		}
		byName[svcCfg.Name] = svcCfg
	}

	merged := make([]ServiceConfig, 0, len(byName))
	for _, s := range byName {
		merged = append(merged, s)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Name < merged[j].Name
	})
	cfg.Services = merged
	return nil
}

// Save writes the global configuration to disk in YAML format without duplicating per-project service definitions.
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	globalOnly := cfg
	globalOnly.Services = nil
	data, err := yaml.Marshal(globalOnly)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// Validate checks invariants and Section 9.3 Custom Instrumentation security limits.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Version) == "" {
		return fmt.Errorf("config version is required")
	}
	if strings.TrimSpace(c.Collector.OTLPEndpoint) == "" {
		return fmt.Errorf("collector.otlp_endpoint is required")
	}
	if c.Collector.TLSEnabled && strings.TrimSpace(c.Collector.CACertPath) == "" {
		return fmt.Errorf("collector.ca_cert_path is required when collector.tls_enabled is true")
	}
	if strings.Contains(c.Collector.AuthHeaderEnv, " ") || strings.Contains(c.Collector.AuthHeaderEnv, ":") {
		return fmt.Errorf("collector.auth_header_env must be an environment variable name (e.g. OTEL_AUTH_HEADER), never a raw token")
	}
	if c.Policies.Sampling.Ratio < 0.0 || c.Policies.Sampling.Ratio > 1.0 {
		return fmt.Errorf("policies.sampling.ratio must be between 0.0 and 1.0, got %.2f", c.Policies.Sampling.Ratio)
	}
	if c.Policies.Limits.MaxAttributesPerSpan < 1 {
		return fmt.Errorf("policies.limits.max_attributes_per_span must be >= 1")
	}

	seen := make(map[string]bool)
	for _, svc := range c.Services {
		name := strings.TrimSpace(svc.Name)
		if name == "" {
			return fmt.Errorf("service name cannot be empty")
		}
		if seen[name] {
			return fmt.Errorf("duplicate service name %q", name)
		}
		seen[name] = true

		if err := validateServiceMethods(name, svc.Instrumentation.Methods); err != nil {
			return err
		}
	}

	return nil
}

func validateServiceMethods(serviceName string, methods []MethodRule) error {
	if len(methods) > MaxCustomMethodsPerService {
		return fmt.Errorf(
			"service %s: %d custom methods exceeds maximum allowed (%d) to protect CPU and latency overhead",
			serviceName, len(methods), MaxCustomMethodsPerService,
		)
	}

	for _, m := range methods {
		if strings.TrimSpace(m.Method) == "" {
			return fmt.Errorf("service %s: custom instrumentation method name cannot be empty", serviceName)
		}
		if err := validateMethodSecurity(serviceName, m); err != nil {
			return err
		}
	}
	return nil
}

func validateMethodSecurity(serviceName string, m MethodRule) error {
	for _, metric := range m.Metrics {
		switch metric.Type {
		case "counter", "histogram", "updowncounter", "gauge":
		default:
			return fmt.Errorf(
				"service %s method %s: unsupported metric type %q (allowed: counter, histogram, updowncounter, gauge)",
				serviceName, m.Method, metric.Type,
			)
		}

		if err := validateValueSource(serviceName, m.Method, metric.ValueFrom); err != nil {
			return err
		}

		if len(metric.AttributesFrom) > MaxAllowedAttributesPerMethod {
			return fmt.Errorf(
				"service %s method %s metric %s: attributes_from count (%d) exceeds cardinality limit (%d)",
				serviceName, m.Method, metric.Name, len(metric.AttributesFrom), MaxAllowedAttributesPerMethod,
			)
		}

		for attrKey, expr := range metric.AttributesFrom {
			if err := validateNotSensitive(serviceName, m.Method, attrKey); err != nil {
				return err
			}
			if err := validateValueSource(serviceName, m.Method, expr); err != nil {
				return err
			}
		}
	}

	if (m.CaptureArguments || m.CaptureReturn) && len(m.AllowedAttributes) == 0 {
		return fmt.Errorf(
			"service %s method %s: capture_arguments/capture_return is blocked by default; provide an explicit allowed_attributes list",
			serviceName, m.Method,
		)
	}

	if len(m.AllowedAttributes) > MaxAllowedAttributesPerMethod {
		return fmt.Errorf(
			"service %s method %s: allowed_attributes count (%d) exceeds cardinality limit (%d)",
			serviceName, m.Method, len(m.AllowedAttributes), MaxAllowedAttributesPerMethod,
		)
	}

	for _, attr := range m.AllowedAttributes {
		if err := validateNotSensitive(serviceName, m.Method, attr); err != nil {
			return err
		}
	}

	return nil
}

func validateValueSource(serviceName, methodName, expr string) error {
	trimmed := strings.TrimSpace(expr)
	if trimmed == "" || trimmed == "1" || trimmed == "duration_ms" || trimmed == "return" {
		return nil
	}
	if err := validateNotSensitive(serviceName, methodName, trimmed); err != nil {
		return err
	}
	if strings.HasPrefix(trimmed, "arg.") || strings.HasPrefix(trimmed, "return.") {
		return nil
	}
	return fmt.Errorf(
		"service %s method %s: invalid value/attribute expression %q (allowed: '1', 'duration_ms', 'return', 'arg.<field_or_index>', 'return.<field>')",
		serviceName, methodName, expr,
	)
}

func validateNotSensitive(serviceName, methodName, candidate string) error {
	lower := strings.ToLower(strings.TrimSpace(candidate))
	for _, token := range forbiddenSensitiveAttributeTokens {
		if strings.Contains(lower, token) {
			return fmt.Errorf(
				"service %s method %s: %q is blocked by security policy (sensitive/PII token %q)",
				serviceName, methodName, candidate, token,
			)
		}
	}
	return nil
}

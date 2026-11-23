package orchestrator

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/adilsoncruz/obsynx/internal/capabilities"
	"github.com/adilsoncruz/obsynx/internal/config"
	"github.com/adilsoncruz/obsynx/internal/discovery"
	"github.com/adilsoncruz/obsynx/internal/policy"
)

// Plan represents the read-only recommendation produced by the OBSynx Orchestrator (Section 8 & 10).
type Plan struct {
	CollectorEndpoint string                   `json:"collector_endpoint" yaml:"collector_endpoint"`
	CollectorPresent  bool                     `json:"collector_present" yaml:"collector_present"`
	PoliciesApplied   []string                 `json:"policies_applied" yaml:"policies_applied"`
	Efficiency        policy.EfficiencySummary `json:"efficiency" yaml:"efficiency"`
	Actions           []ServiceAction          `json:"actions" yaml:"actions"`
}

// ServiceAction describes the recommended instrumentation strategy, generated config, business metrics, and limitations for a service.
type ServiceAction struct {
	ServiceName         string            `json:"service_name" yaml:"service_name"`
	Runtime             string            `json:"runtime" yaml:"runtime"`
	ProjectConfigFile   string            `json:"project_config_file,omitempty" yaml:"project_config_file,omitempty"`
	SelectedAdapter     string            `json:"selected_adapter" yaml:"selected_adapter"`
	Strategy            string            `json:"strategy" yaml:"strategy"`
	Status              string            `json:"status" yaml:"status"`
	SuppressEBPFSidecar bool              `json:"suppress_ebpf_sidecar,omitempty" yaml:"suppress_ebpf_sidecar,omitempty"`
	GeneratedEnv        map[string]string `json:"generated_env,omitempty" yaml:"generated_env,omitempty"`
	DerivedMetrics      []string          `json:"derived_metrics,omitempty" yaml:"derived_metrics,omitempty"`
	Requirements        []string          `json:"requirements,omitempty" yaml:"requirements,omitempty"`
	Warnings            []string          `json:"warnings,omitempty" yaml:"warnings,omitempty"`
	FallbackSuggestion  string            `json:"fallback_suggestion,omitempty" yaml:"fallback_suggestion,omitempty"`
	AssistedSnippet     string            `json:"assisted_snippet,omitempty" yaml:"assisted_snippet,omitempty"`
}

// BuildPlan correlates the discovered inventory, desired config, and capability registry without mutating services.
func BuildPlan(inv discovery.Inventory, cfg config.Config, reg *capabilities.Registry) Plan {
	desiredByName := make(map[string]config.ServiceConfig, len(cfg.Services))
	for _, svc := range cfg.Services {
		desiredByName[svc.Name] = svc
	}

	actions := make([]ServiceAction, 0, len(inv.Services))
	duplicatesPrevented := 0

	for _, disc := range inv.Services {
		desired, hasDesired := desiredByName[disc.Name]
		action := planForService(disc, desired, hasDesired, cfg, reg)
		if action.SuppressEBPFSidecar {
			duplicatesPrevented++
		}
		actions = append(actions, action)
	}

	eff := policy.EvaluateEfficiency(cfg, duplicatesPrevented)

	return Plan{
		CollectorEndpoint: cfg.Collector.OTLPEndpoint,
		CollectorPresent:  inv.CollectorPresent,
		PoliciesApplied: []string{
			fmt.Sprintf("sampling: %s (ratio=%.2f)", cfg.Policies.Sampling.Strategy, cfg.Policies.Sampling.Ratio),
			fmt.Sprintf("exclude_paths: %s", strings.Join(cfg.Policies.Collection.ExcludePaths, ", ")),
			fmt.Sprintf("redact_attributes: %s", strings.Join(cfg.Policies.Attributes.Deny, ", ")),
			fmt.Sprintf("span_limits: max_attrs=%d, max_events=%d", cfg.Policies.Limits.MaxAttributesPerSpan, cfg.Policies.Limits.MaxEventsPerSpan),
		},
		Efficiency: eff,
		Actions:    actions,
	}
}

func planForService(
	disc discovery.DiscoveredService,
	desired config.ServiceConfig,
	hasDesired bool,
	cfg config.Config,
	reg *capabilities.Registry,
) ServiceAction {
	candidates := reg.FindByRuntime(disc.Runtime)
	if len(candidates) == 0 {
		return ServiceAction{
			ServiceName:        disc.Name,
			Runtime:            disc.Runtime,
			SelectedAdapter:    "none",
			Strategy:           "manual-sdk",
			Status:             "unsupported-zero-code",
			Warnings:           []string{"No automatic or eBPF adapter registered for runtime " + disc.Runtime},
			FallbackSuggestion: "Use official OpenTelemetry SDK/API for manual instrumentation",
		}
	}

	primary := candidates[0]
	projFile := desired.SourceFile
	if projFile == "" && disc.Evidence != nil {
		projFile = disc.Evidence["project_config"]
	}
	action := ServiceAction{
		ServiceName:       disc.Name,
		Runtime:           disc.Runtime,
		ProjectConfigFile: projFile,
		SelectedAdapter:   primary.ID,
		Strategy:          primary.Strategy,
		Status:            "ready-to-instrument",
		GeneratedEnv: map[string]string{
			"OTEL_SERVICE_NAME":               disc.Name,
			"OTEL_EXPORTER_OTLP_ENDPOINT":     cfg.Collector.OTLPEndpoint,
			"OTEL_EXPORTER_OTLP_PROTOCOL":     cfg.Collector.Protocol,
			"OTEL_METRIC_EXPORT_INTERVAL":     "5000",
			"OTEL_TRACES_SAMPLER":             "parentbased_traceidratio",
			"OTEL_TRACES_SAMPLER_ARG":         fmt.Sprintf("%.2f", cfg.Policies.Sampling.Ratio),
			"OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT": strconv.Itoa(cfg.Policies.Limits.MaxAttributesPerSpan),
			"OTEL_SPAN_EVENT_COUNT_LIMIT":     strconv.Itoa(cfg.Policies.Limits.MaxEventsPerSpan),
		},
		Requirements: primary.Requirements,
	}

	if len(disc.ExistingInstrumentation) > 0 {
		action.Status = "already-instrumented (managed-drift-check)"
	}

	// Phase 5 (Section 10.1): Detect and prevent duplicate instrumentation (Agent/SDK + eBPF sidecar).
	if disc.HasDualInstrumentation() || primary.Strategy != "ebpf" {
		if disc.HasDualInstrumentation() {
			action.SuppressEBPFSidecar = true
			action.Warnings = append(action.Warnings,
				"Cost-Aware Guardrail: Dual instrumentation detected (in-process Agent/SDK + eBPF sidecar). Disabling eBPF sidecar for this service to prevent 2x duplicate span export.",
			)
		}
	}

	switch primary.ID {
	case "opentelemetry-java":
		action.GeneratedEnv["JAVA_TOOL_OPTIONS"] = "-javaagent:/obsynx-agents/jvm/opentelemetry-javaagent.jar -javaagent:/obsynx-agents/jvm/obsynx-jvm-agent.jar"
	case "opentelemetry-nodejs":
		action.GeneratedEnv["NODE_PATH"] = "/obsynx-agents/nodejs/node_modules"
		action.GeneratedEnv["NODE_OPTIONS"] = "--require @opentelemetry/auto-instrumentations-node/register --require /obsynx-agents/nodejs/obsynx-hook.js"
		action.GeneratedEnv["OTEL_EXPORTER_OTLP_ENDPOINT"] = strings.Replace(cfg.Collector.OTLPEndpoint, ":4317", ":4318", 1)
		action.GeneratedEnv["OTEL_EXPORTER_OTLP_PROTOCOL"] = "http/protobuf"
		action.GeneratedEnv["OTEL_METRICS_EXPORTER"] = "otlp"
	case "opentelemetry-python":
		action.GeneratedEnv["OTEL_PYTHON_LOGGING_AUTO_INSTRUMENTATION_ENABLED"] = "true"
		action.GeneratedEnv["OTEL_METRICS_EXPORTER"] = "otlp"
		action.GeneratedEnv["PYTHONPATH"] = "/obsynx-agents/python"
	case "opentelemetry-dotnet":
		action.GeneratedEnv["CORECLR_ENABLE_PROFILING"] = "1"
		action.GeneratedEnv["CORECLR_PROFILER"] = "{918728DD-259F-4A63-ADAC-BB42230AE3B2}"
		action.GeneratedEnv["CORECLR_PROFILER_PATH"] = "/obsynx-agents/dotnet/linux-x64/OpenTelemetry.AutoInstrumentation.Native.so"
		action.GeneratedEnv["DOTNET_STARTUP_HOOKS"] = "/obsynx-agents/dotnet/net/OpenTelemetry.AutoInstrumentation.StartupHook.dll"
		action.GeneratedEnv["DOTNET_ADDITIONAL_DEPS"] = "/obsynx-agents/dotnet/AdditionalDeps"
		action.GeneratedEnv["DOTNET_SHARED_STORE"] = "/obsynx-agents/dotnet/store"
		action.GeneratedEnv["OTEL_EXPORTER_OTLP_ENDPOINT"] = cfg.Collector.OTLPEndpoint
		action.GeneratedEnv["OTEL_EXPORTER_OTLP_PROTOCOL"] = "grpc"
		action.GeneratedEnv["OTEL_METRICS_EXPORTER"] = "otlp"
		action.GeneratedEnv["OTEL_LOGS_EXPORTER"] = "otlp"
	}

	if cfg.Collector.TLSEnabled && cfg.Collector.CACertPath != "" {
		action.GeneratedEnv["OTEL_EXPORTER_OTLP_CERTIFICATE"] = cfg.Collector.CACertPath
	}
	if cfg.Collector.AuthHeaderEnv != "" {
		action.GeneratedEnv["OTEL_EXPORTER_OTLP_HEADERS"] = "${" + cfg.Collector.AuthHeaderEnv + "}"
	}

	if hasDesired && len(desired.Instrumentation.Methods) > 0 {
		applyCustomMethods(primary, disc.Runtime, desired.Instrumentation.Methods, &action)
	}

	return action
}

func applyCustomMethods(adapter capabilities.AdapterSpec, runtime string, methods []config.MethodRule, action *ServiceAction) {
	action.DerivedMetrics = collectDerivedMetrics(methods)

	if !adapter.Capabilities.CustomMethodTracing.Supports() {
		var names []string
		for _, m := range methods {
			names = append(names, m.Method)
		}
		action.Warnings = append(action.Warnings, fmt.Sprintf(
			"Graceful Degradation: adapter %q (%s) has custom_method_tracing=%s; cannot intercept domain methods [%s] without SDK/code instrumentation.",
			adapter.ID, adapter.Strategy, adapter.Capabilities.CustomMethodTracing, strings.Join(names, ", "),
		))
		action.FallbackSuggestion = "Keep eBPF for automatic HTTP/network spans and use OpenTelemetry SDK for function-level spans & metrics (Counter, Histogram, UpDownCounter, Gauge)."
		action.AssistedSnippet = generateAssistedSnippet(runtime, action.ServiceName, methods[0])
		return
	}

	if adapter.ID == "opentelemetry-java" {
		byClass := make(map[string][]string)
		var classOrder []string
		for _, m := range methods {
			if !m.Tracing || strings.TrimSpace(m.Class) == "" {
				continue
			}
			if _, exists := byClass[m.Class]; !exists {
				classOrder = append(classOrder, m.Class)
			}
			byClass[m.Class] = append(byClass[m.Class], m.Method)
		}

		var clauses []string
		for _, cls := range classOrder {
			clauses = append(clauses, fmt.Sprintf("%s[%s]", cls, strings.Join(byClass[cls], ",")))
		}
		if len(clauses) > 0 {
			action.GeneratedEnv["OTEL_INSTRUMENTATION_METHODS_INCLUDE"] = strings.Join(clauses, ";")
		}
	}
}

func collectDerivedMetrics(methods []config.MethodRule) []string {
	var derived []string
	seen := make(map[string]bool)

	for _, m := range methods {
		for _, spec := range m.Metrics {
			metricName := spec.Name
			if metricName == "" {
				metricName = fmt.Sprintf("obsynx.business.%s.%s", m.Method, spec.Type)
			}
			extra := ""
			if spec.ValueFrom != "" {
				extra += fmt.Sprintf(", value_from=%s", spec.ValueFrom)
			}
			if len(spec.AttributesFrom) > 0 {
				var pairs []string
				for k, v := range spec.AttributesFrom {
					pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
				}
				extra += fmt.Sprintf(", attrs_from={%s}", strings.Join(pairs, ","))
			}
			label := fmt.Sprintf("%s [type=%s, method=%s%s]", metricName, spec.Type, m.Method, extra)
			if !seen[label] {
				seen[label] = true
				derived = append(derived, label)
			}
		}
	}
	return derived
}

func generateAssistedSnippet(runtime, serviceName string, m config.MethodRule) string {
	switch strings.ToLower(runtime) {
	case "go", "golang":
		return fmt.Sprintf(`// Assisted OpenTelemetry Go SDK snippet for %s.%s:
tracer := otel.Tracer(%q)
ctx, span := tracer.Start(ctx, %q)
defer span.End()`, serviceName, m.Method, serviceName, m.Method)
	case "jvm", "java", "kotlin":
		return fmt.Sprintf(`// Assisted OpenTelemetry JVM Annotation snippet for %s.%s:
import io.opentelemetry.instrumentation.annotations.WithSpan

@WithSpan(%q)
fun %s(...) { ... }`, m.Class, m.Method, m.Method, m.Method)
	case "nodejs", "node":
		return fmt.Sprintf(`// Assisted OpenTelemetry Node.js SDK snippet for %s.%s:
const { trace } = require('@opentelemetry/api');
const tracer = trace.getTracer(%q);
tracer.startActiveSpan(%q, (span) => { try { ... } finally { span.end(); } });`, serviceName, m.Method, serviceName, m.Method)
	case "python", "py":
		return fmt.Sprintf(`# Assisted OpenTelemetry Python SDK snippet for %s.%s:
from opentelemetry import trace
tracer = trace.get_tracer(%q)
with tracer.start_as_current_span(%q):
    ...`, serviceName, m.Method, serviceName, m.Method)
	default:
		return fmt.Sprintf(`// Use OpenTelemetry SDK Tracer(%q).startActiveSpan(%q) around %s`, serviceName, m.Method, m.Method)
	}
}

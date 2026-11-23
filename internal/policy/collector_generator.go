package policy

import (
	"fmt"
	"math"

	"github.com/adilsoncruz/obsynx/internal/config"
	"gopkg.in/yaml.v3"
)

// EfficiencySummary quantifies the estimated volume and cardinality reduction of OBSynx Cost-Aware policies (Section 10).
type EfficiencySummary struct {
	SamplingRatio               float64  `json:"sampling_ratio" yaml:"sampling_ratio"`
	ExcludedPathsCount          int      `json:"excluded_paths_count" yaml:"excluded_paths_count"`
	RedactedAttributesCount     int      `json:"redacted_attributes_count" yaml:"redacted_attributes_count"`
	MaxAttributesPerSpan        int      `json:"max_attributes_per_span" yaml:"max_attributes_per_span"`
	MaxEventsPerSpan            int      `json:"max_events_per_span" yaml:"max_events_per_span"`
	CollectorQueueSize          int      `json:"collector_queue_size" yaml:"collector_queue_size"`
	EstimatedSpanReductionPct   float64  `json:"estimated_span_reduction_pct" yaml:"estimated_span_reduction_pct"`
	DuplicateInstrumentationFix int      `json:"duplicate_instrumentation_prevented" yaml:"duplicate_instrumentation_prevented"`
	Recommendations             []string `json:"recommendations,omitempty" yaml:"recommendations,omitempty"`
}

// EvaluateEfficiency calculates the expected telemetry volume reduction and produces Cost-Aware recommendations.
func EvaluateEfficiency(cfg config.Config, duplicatesPrevented int) EfficiencySummary {
	// Baseline assumption documented for comparison: health/ready probes represent ~15% of raw HTTP spans in typical microservices.
	noiseShare := 0.0
	if len(cfg.Policies.Collection.ExcludePaths) > 0 {
		noiseShare = 0.15
	}
	retainedAfterNoiseFilter := 1.0 - noiseShare
	retainedAfterSampling := retainedAfterNoiseFilter * cfg.Policies.Sampling.Ratio
	reductionPct := math.Round((1.0-retainedAfterSampling)*1000) / 10

	var recs []string
	if cfg.Policies.Sampling.Ratio > 0.50 {
		recs = append(recs, fmt.Sprintf("High sampling ratio (%.2f): consider <= 0.10 in production to reduce backend storage cost.", cfg.Policies.Sampling.Ratio))
	}
	if len(cfg.Policies.Collection.ExcludePaths) == 0 {
		recs = append(recs, "No exclude_paths configured: add '/health' and '/ready' to drop non-operational liveness spans.")
	}
	if cfg.Policies.Limits.MaxAttributesPerSpan > 64 {
		recs = append(recs, fmt.Sprintf("max_attributes_per_span (%d) is high: consider <= 32 to bound attribute cardinality.", cfg.Policies.Limits.MaxAttributesPerSpan))
	}
	if duplicatesPrevented > 0 {
		recs = append(recs, fmt.Sprintf("Prevented %d duplicate Agent+eBPF instrumentation attachment(s) to avoid 2x span ingestion.", duplicatesPrevented))
	}

	return EfficiencySummary{
		SamplingRatio:               cfg.Policies.Sampling.Ratio,
		ExcludedPathsCount:          len(cfg.Policies.Collection.ExcludePaths),
		RedactedAttributesCount:     len(cfg.Policies.Attributes.Deny),
		MaxAttributesPerSpan:        cfg.Policies.Limits.MaxAttributesPerSpan,
		MaxEventsPerSpan:            cfg.Policies.Limits.MaxEventsPerSpan,
		CollectorQueueSize:          2048,
		EstimatedSpanReductionPct:   reductionPct,
		DuplicateInstrumentationFix: duplicatesPrevented,
		Recommendations:             recs,
	}
}

// RenderCollectorConfig produces a managed OpenTelemetry Collector YAML configuration
// applying OBSynx Cost-Aware, Security, and Phase 4-5 Custom Business Metrics (spanmetrics) policies.
func RenderCollectorConfig(cfg config.Config) ([]byte, error) {
	var spanFilters []string
	for _, path := range cfg.Policies.Collection.ExcludePaths {
		spanFilters = append(spanFilters,
			fmt.Sprintf(`attributes["http.route"] == %q`, path),
			fmt.Sprintf(`attributes["url.path"] == %q`, path),
			fmt.Sprintf(`attributes["http.target"] == %q`, path),
		)
	}

	var redactActions []map[string]string
	for _, key := range cfg.Policies.Attributes.Deny {
		redactActions = append(redactActions, map[string]string{
			"key":    key,
			"action": "delete",
		})
	}

	samplingPct := math.Round(cfg.Policies.Sampling.Ratio * 10000) / 100

	tempoTLS := map[string]any{"insecure": true}
	if cfg.Collector.TLSEnabled {
		tempoTLS = map[string]any{
			"insecure": false,
			"ca_file":  cfg.Collector.CACertPath,
		}
	}

	tempoExporter := map[string]any{
		"endpoint": "tempo:4317",
		"tls":      tempoTLS,
		"sending_queue": map[string]any{
			"enabled":       true,
			"num_consumers": 4,
			"queue_size":    2048,
		},
		"retry_on_failure": map[string]any{
			"enabled":          true,
			"initial_interval": "5s",
			"max_interval":     "30s",
		},
	}
	if cfg.Collector.AuthHeaderEnv != "" {
		tempoExporter["headers"] = map[string]string{
			"authorization": "${env:" + cfg.Collector.AuthHeaderEnv + "}",
		}
	}

	doc := map[string]any{
		"receivers": map[string]any{
			"otlp": map[string]any{
				"protocols": map[string]any{
					"grpc": map[string]any{"endpoint": "0.0.0.0:4317"},
					"http": map[string]any{"endpoint": "0.0.0.0:4318"},
				},
			},
		},
		// Phase 4 (Section 9.4): Official OpenTelemetry spanmetrics connector derives
		// Counter (calls_total) and Histogram (duration) business metrics BEFORE tail/probabilistic sampling
		// so 100% of business calls are counted in metrics even when traces are sampled at 10%.
		"connectors": map[string]any{
			"spanmetrics": map[string]any{
				"namespace": "obsynx.business",
				"histogram": map[string]any{
					"explicit": map[string]any{
						"buckets": []string{"2ms", "5ms", "10ms", "25ms", "50ms", "100ms", "250ms", "500ms", "1s"},
					},
				},
				"dimensions": []map[string]string{
					{"name": "http.request.method"},
					{"name": "http.response.status_code"},
					{"name": "code.function"},
					{"name": "code.namespace"},
				},
			},
		},
		"processors": map[string]any{
			"memory_limiter": map[string]any{
				"check_interval":  "1s",
				"limit_mib":       512,
				"spike_limit_mib": 128,
			},
			"filter/obsynx_exclude_paths": map[string]any{
				"error_mode": "ignore",
				"traces": map[string]any{
					"span": spanFilters,
				},
			},
			"attributes/obsynx_redact": map[string]any{
				"actions": redactActions,
			},
			"probabilistic_sampler/obsynx_cost": map[string]any{
				"sampling_percentage": samplingPct,
			},
			"batch": map[string]any{
				"send_batch_size":     1024,
				"send_batch_max_size": 2048,
				"timeout":             "1s",
			},
		},
		"exporters": map[string]any{
			"otlp/tempo": tempoExporter,
			"prometheus": map[string]any{
				"endpoint": "0.0.0.0:8889",
				"resource_to_telemetry_conversion": map[string]any{
					"enabled": true,
				},
			},
			"debug": map[string]any{
				"verbosity": "basic",
			},
		},
		"extensions": map[string]any{
			"health_check": map[string]any{"endpoint": "0.0.0.0:13133"},
		},
		"service": map[string]any{
			"extensions": []string{"health_check"},
			"telemetry": map[string]any{
				"metrics": map[string]any{"address": "0.0.0.0:8888"},
			},
			"pipelines": map[string]any{
				"traces": map[string]any{
					"receivers":  []string{"otlp"},
					"processors": []string{"memory_limiter", "filter/obsynx_exclude_paths", "attributes/obsynx_redact", "batch"},
					"exporters":  []string{"spanmetrics"},
				},
				"traces/sampled": map[string]any{
					"receivers":  []string{"otlp"},
					"processors": []string{"memory_limiter", "filter/obsynx_exclude_paths", "attributes/obsynx_redact", "probabilistic_sampler/obsynx_cost", "batch"},
					"exporters":  []string{"otlp/tempo", "debug"},
				},
				"metrics": map[string]any{
					"receivers":  []string{"otlp", "spanmetrics"},
					"processors": []string{"memory_limiter", "batch"},
					"exporters":  []string{"prometheus", "debug"},
				},
			},
		},
	}

	return yaml.Marshal(doc)
}

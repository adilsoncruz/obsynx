package policy

import (
	"strings"
	"testing"

	"github.com/adilsoncruz/obsynx/internal/config"
)

func TestEvaluateEfficiencyAndRenderCollectorConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	eff := EvaluateEfficiency(cfg, 1)

	if eff.EstimatedSpanReductionPct < 90.0 {
		t.Fatalf("expected >= 90%% span reduction with 0.10 sampling + exclude_paths, got %.1f%%", eff.EstimatedSpanReductionPct)
	}
	if eff.DuplicateInstrumentationFix != 1 {
		t.Fatalf("expected 1 duplicate instrumentation fix recorded, got %d", eff.DuplicateInstrumentationFix)
	}

	out, err := RenderCollectorConfig(cfg)
	if err != nil {
		t.Fatalf("RenderCollectorConfig failed: %v", err)
	}
	yamlStr := string(out)
	if !strings.Contains(yamlStr, "probabilistic_sampler/obsynx_cost") {
		t.Fatal("expected probabilistic_sampler/obsynx_cost in generated collector config")
	}
	if !strings.Contains(yamlStr, "sending_queue") {
		t.Fatal("expected sending_queue buffer control in generated collector config")
	}
}

func TestEvaluateEfficiencyRecommendationsAndTLS(t *testing.T) {
	// Scenario: High sampling ratio, no exclude paths, high max attributes
	cfg := config.DefaultConfig()
	cfg.Policies.Sampling.Ratio = 0.80
	cfg.Policies.Collection.ExcludePaths = nil
	cfg.Policies.Limits.MaxAttributesPerSpan = 100
	cfg.Collector.TLSEnabled = true
	cfg.Collector.CACertPath = "/etc/ssl/ca.pem"
	cfg.Collector.AuthHeaderEnv = "OTEL_AUTH_SECRET"

	eff := EvaluateEfficiency(cfg, 0)
	if len(eff.Recommendations) < 3 {
		t.Fatalf("expected at least 3 recommendations, got %d: %+v", len(eff.Recommendations), eff.Recommendations)
	}

	out, err := RenderCollectorConfig(cfg)
	if err != nil {
		t.Fatalf("RenderCollectorConfig failed: %v", err)
	}
	yamlStr := string(out)
	if !strings.Contains(yamlStr, "/etc/ssl/ca.pem") {
		t.Fatal("expected CA cert path in collector config")
	}
	if !strings.Contains(yamlStr, "${env:OTEL_AUTH_SECRET}") {
		t.Fatal("expected auth header in collector config")
	}
}

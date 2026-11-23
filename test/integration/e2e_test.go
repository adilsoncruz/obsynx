package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adilsoncruz/obsynx/internal/capabilities"
	"github.com/adilsoncruz/obsynx/internal/config"
	"github.com/adilsoncruz/obsynx/internal/discovery"
	"github.com/adilsoncruz/obsynx/internal/lifecycle"
	"github.com/adilsoncruz/obsynx/internal/orchestrator"
)

func TestEndToEndMultiRuntimeWorkflowAndDriftDetection(t *testing.T) {
	// Discover the real repository workspace (go-api, kotlin-spring-api, nodejs-api, python-api, dotnet-api).
	repoRoot := filepath.Clean("../..")
	inv, err := discovery.Discover(repoRoot)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}

	if len(inv.Services) != 5 {
		t.Fatalf("expected 5 multi-runtime services discovered (go, jvm, nodejs, python, dotnet), got %d", len(inv.Services))
	}

	reg, err := capabilities.LoadRegistry("")
	if err != nil {
		t.Fatalf("LoadRegistry failed: %v", err)
	}

	cfg := config.DefaultConfig()
	_ = config.MergeProjectConfigs(repoRoot, &cfg)
	cfg.Collector.TLSEnabled = true
	cfg.Collector.CACertPath = "/etc/ssl/certs/obsynx-ca.pem"
	cfg.Collector.AuthHeaderEnv = "OTEL_AUTH_HEADER"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected production TLS+Auth config to be valid, got: %v", err)
	}

	plan := orchestrator.BuildPlan(inv, cfg, reg)
	if len(plan.Actions) != 5 {
		t.Fatalf("expected 5 actions in orchestration plan, got %d", len(plan.Actions))
	}

	// Verify runtime-specific env generation across all 5 runtimes.
	byService := make(map[string]orchestrator.ServiceAction)
	for _, act := range plan.Actions {
		byService[act.ServiceName] = act
	}

	if !strings.Contains(byService["nodejs-api"].GeneratedEnv["NODE_OPTIONS"], "@opentelemetry/auto-instrumentations-node/register") {
		t.Fatalf("expected nodejs-api to receive NODE_OPTIONS preload, got: %+v", byService["nodejs-api"].GeneratedEnv)
	}
	if byService["python-api"].GeneratedEnv["OTEL_PYTHON_LOGGING_AUTO_INSTRUMENTATION_ENABLED"] != "true" {
		t.Fatalf("expected python-api to receive OTEL_PYTHON_LOGGING_AUTO_INSTRUMENTATION_ENABLED=true")
	}
	if byService["kotlin-spring-api"].GeneratedEnv["OTEL_EXPORTER_OTLP_CERTIFICATE"] != "/etc/ssl/certs/obsynx-ca.pem" {
		t.Fatalf("expected TLS certificate path injected into agent env")
	}
	if byService["dotnet-api"].GeneratedEnv["CORECLR_ENABLE_PROFILING"] != "1" {
		t.Fatalf("expected dotnet-api to receive CORECLR_ENABLE_PROFILING=1")
	}
	if !strings.Contains(byService["dotnet-api"].GeneratedEnv["DOTNET_STARTUP_HOOKS"], "StartupHook.dll") {
		t.Fatalf("expected dotnet-api to receive DOTNET_STARTUP_HOOKS")
	}

	// Materialize into an isolated temp directory to verify apply, SHA-256 drift detection, and rollback.
	tmpRoot := t.TempDir()
	state, err := lifecycle.Apply(tmpRoot, "obsynx.yaml", cfg, plan)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	if drifts := lifecycle.VerifyIntegrity(tmpRoot, state); len(drifts) != 0 {
		t.Fatalf("expected 0 drifts immediately after apply, got: %v", drifts)
	}

	// Simulate manual tampering with the generated collector config.
	collectorFile := filepath.Join(tmpRoot, lifecycle.CollectorManagedRelPath)
	if err := os.WriteFile(collectorFile, []byte("# tampered config\n"), 0o644); err != nil {
		t.Fatalf("tamper write: %v", err)
	}

	drifts := lifecycle.VerifyIntegrity(tmpRoot, state)
	if len(drifts) == 0 || !strings.Contains(drifts[0], "SHA-256 checksum mismatch") {
		t.Fatalf("expected SHA-256 checksum mismatch drift detection, got: %v", drifts)
	}

	rolledBack, err := lifecycle.Rollback(tmpRoot)
	if err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}
	if rolledBack.Status != "rolled_back" {
		t.Fatalf("expected status rolled_back, got %s", rolledBack.Status)
	}
}

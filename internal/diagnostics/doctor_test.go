package diagnostics

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adilsoncruz/obsynx/internal/capabilities"
	"github.com/adilsoncruz/obsynx/internal/config"
	"github.com/adilsoncruz/obsynx/internal/lifecycle"
	"github.com/adilsoncruz/obsynx/internal/orchestrator"
	"github.com/adilsoncruz/obsynx/internal/policy"
)

func TestDoctorDiagnostics(t *testing.T) {
	cmdCountBefore := RecordCommand()
	if cmdCountBefore == 0 {
		t.Fatal("expected RecordCommand to return > 0")
	}

	reg, err := capabilities.LoadRegistry("")
	if err != nil {
		t.Fatalf("LoadRegistry failed: %v", err)
	}

	tmpDir := t.TempDir()

	// Scenario 1: Missing config, missing collector
	results, metrics := RunDoctor(tmpDir, filepath.Join(tmpDir, "obsynx.yaml"), reg)
	if len(results) == 0 {
		t.Fatal("expected diagnostic results")
	}
	if metrics.AdaptersLoaded != len(reg.List()) {
		t.Fatalf("expected %d adapters loaded, got %d", len(reg.List()), metrics.AdaptersLoaded)
	}

	// Scenario 2: Valid config and collector config present
	validCfg := config.DefaultConfig()
	cfgPath := filepath.Join(tmpDir, "obsynx.yaml")
	if err := config.Save(cfgPath, validCfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	collectorDir := filepath.Join(tmpDir, "deploy", "collector")
	_ = os.MkdirAll(collectorDir, 0o755)
	_ = os.WriteFile(filepath.Join(collectorDir, "otel-collector-config.yaml"), []byte("receivers: {}\n"), 0o644)

	results, _ = RunDoctor(tmpDir, cfgPath, reg)
	var foundCollectorPass, foundConfigPass bool
	for _, r := range results {
		if r.Name == "collector_pipeline_config" && r.Status == "PASS" {
			foundCollectorPass = true
		}
		if r.Name == "obsynx_config" && r.Status == "PASS" {
			foundConfigPass = true
		}
	}
	if !foundCollectorPass || !foundConfigPass {
		t.Fatal("expected PASS for collector and valid config")
	}

	// Scenario 3: Corrupt config
	_ = os.WriteFile(cfgPath, []byte("invalid: [yaml: content"), 0o644)
	results, _ = RunDoctor(tmpDir, cfgPath, reg)
	var foundConfigFail bool
	for _, r := range results {
		if r.Name == "obsynx_config" && r.Status == "FAIL" {
			foundConfigFail = true
		}
	}
	if !foundConfigFail {
		t.Fatal("expected FAIL for invalid config")
	}

	// Scenario 4: Managed state applied with and without drift
	plan := orchestrator.Plan{
		Efficiency: policy.EvaluateEfficiency(validCfg, 0),
	}
	state, err := lifecycle.Apply(tmpDir, "obsynx.yaml", validCfg, plan)
	if err == nil {
		results, _ = RunDoctor(tmpDir, cfgPath, reg)
		var foundStatePass bool
		for _, r := range results {
			if r.Name == "managed_state_integrity" && r.Status == "PASS" {
				foundStatePass = true
			}
		}
		if !foundStatePass {
			t.Fatal("expected PASS for managed state integrity")
		}

		// Tamper to trigger drift
		for _, gf := range state.GeneratedFiles {
			_ = os.WriteFile(filepath.Join(tmpDir, gf), []byte("tampered"), 0o644)
			break
		}
		results, _ = RunDoctor(tmpDir, cfgPath, reg)
		var foundDriftWarn bool
		for _, r := range results {
			if r.Name == "managed_state_integrity" && r.Status == "WARN" {
				foundDriftWarn = true
			}
		}
		if !foundDriftWarn {
			t.Fatal("expected WARN for drifted managed state")
		}
	}
}

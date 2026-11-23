package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adilsoncruz/obsynx/internal/capabilities"
	"github.com/adilsoncruz/obsynx/internal/config"
	"github.com/adilsoncruz/obsynx/internal/discovery"
	"github.com/adilsoncruz/obsynx/internal/orchestrator"
)

func TestApplyAndRollbackLifecycle(t *testing.T) {
	tmpDir := t.TempDir()

	reg, err := capabilities.LoadRegistry("")
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	cfg := config.DefaultConfig()
	inv := discovery.Inventory{
		CollectorPresent: true,
		Services: []discovery.DiscoveredService{
			{Name: "go-api", Runtime: "go"},
			{Name: "kotlin-spring-api", Runtime: "jvm"},
		},
	}

	plan := orchestrator.BuildPlan(inv, cfg, reg)

	appliedState, err := Apply(tmpDir, "obsynx.yaml", cfg, plan)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if appliedState.Status != "applied" {
		t.Fatalf("expected status 'applied', got %q", appliedState.Status)
	}

	for _, rel := range appliedState.GeneratedFiles {
		if _, statErr := os.Stat(filepath.Join(tmpDir, rel)); statErr != nil {
			t.Fatalf("expected generated file %s to exist: %v", rel, statErr)
		}
	}

	rolledState, err := Rollback(tmpDir)
	if err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}
	if rolledState.Status != "rolled_back" {
		t.Fatalf("expected status 'rolled_back', got %q", rolledState.Status)
	}

	if _, statErr := os.Stat(filepath.Join(tmpDir, ComposeOverrideRelPath)); !os.IsNotExist(statErr) {
		t.Fatalf("expected compose override to be removed after rollback")
	}
}

func TestVerifyIntegrityAndDriftBranches(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Uninitialized state returns nil drifts
	uninitDrifts := VerifyIntegrity(tmpDir, ManagedState{Status: "uninitialized"})
	if uninitDrifts != nil {
		t.Fatalf("expected nil drifts for uninitialized, got %v", uninitDrifts)
	}

	reg, _ := capabilities.LoadRegistry("")
	cfg := config.DefaultConfig()
	plan := orchestrator.BuildPlan(discovery.Inventory{}, cfg, reg)

	appliedState, err := Apply(tmpDir, "obsynx.yaml", cfg, plan)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// 2. Immediate integrity check has 0 drifts
	if drifts := VerifyIntegrity(tmpDir, appliedState); len(drifts) != 0 {
		t.Fatalf("expected 0 drifts, got %v", drifts)
	}

	// 3. Delete one generated file -> should detect file missing
	firstFile := appliedState.GeneratedFiles[0]
	_ = os.Remove(filepath.Join(tmpDir, firstFile))

	drifts := VerifyIntegrity(tmpDir, appliedState)
	if len(drifts) == 0 || !strings.Contains(drifts[0], "file missing") {
		t.Fatalf("expected 'file missing' drift, got %v", drifts)
	}

	// 4. Modify second generated file -> should detect checksum mismatch
	secondFile := appliedState.GeneratedFiles[1]
	_ = os.WriteFile(filepath.Join(tmpDir, secondFile), []byte("# modified"), 0o644)

	drifts = VerifyIntegrity(tmpDir, appliedState)
	var foundMismatch bool
	for _, d := range drifts {
		if strings.Contains(d, "checksum mismatch") {
			foundMismatch = true
		}
	}
	if !foundMismatch {
		t.Fatalf("expected checksum mismatch drift, got %v", drifts)
	}

	// 5. Rollback with drifts should mention drift resolution in audit summary
	rolled, err := Rollback(tmpDir)
	if err != nil {
		t.Fatalf("Rollback with drifts failed: %v", err)
	}
	lastAudit := rolled.AuditTrail[len(rolled.AuditTrail)-1]
	if !strings.Contains(lastAudit.Summary, "file drift") {
		t.Fatalf("expected audit summary to mention drift resolution, got %s", lastAudit.Summary)
	}

	// 6. Apply with unsuppressed obi-ebpf sidecar
	ebpfPlan := orchestrator.Plan{
		CollectorEndpoint: "http://collector:4317",
		Actions: []orchestrator.ServiceAction{
			{
				ServiceName:         "pure-go-svc",
				Runtime:             "go",
				SelectedAdapter:     "obi-ebpf",
				Strategy:            "ebpf",
				SuppressEBPFSidecar: false,
			},
		},
	}
	ebpfTmp := t.TempDir()
	ebpfState, err := Apply(ebpfTmp, "obsynx.yaml", cfg, ebpfPlan)
	if err != nil || len(ebpfState.GeneratedFiles) == 0 {
		t.Fatalf("Apply with eBPF sidecar failed: %v", err)
	}
}

func TestLoadStateEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()

	// Missing state file -> returns uninitialized
	st, err := LoadState(tmpDir)
	if err != nil || st.Status != "uninitialized" {
		t.Fatalf("expected uninitialized for missing state, got %+v, err=%v", st, err)
	}

	// Corrupted state file -> returns error
	stateFile := filepath.Join(tmpDir, StateFileRelPath)
	_ = os.MkdirAll(filepath.Dir(stateFile), 0o755)
	_ = os.WriteFile(stateFile, []byte("invalid-json:["), 0o644)

	_, err = LoadState(tmpDir)
	if err == nil {
		t.Fatal("expected error loading corrupted state file")
	}
}

func TestApplyAndRollbackErrors(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, StateFileRelPath)
	_ = os.MkdirAll(filepath.Dir(stateFile), 0o755)
	_ = os.WriteFile(stateFile, []byte("invalid-json:["), 0o644)
	if _, err := Rollback(tmpDir); err == nil {
		t.Fatal("expected Rollback to fail on corrupted state file")
	}

	badDir := filepath.Join(tmpDir, "file-blocking-dir")
	_ = os.WriteFile(badDir, []byte("not a dir"), 0o644)
	cfg := config.DefaultConfig()
	reg, _ := capabilities.LoadRegistry("")
	plan := orchestrator.BuildPlan(discovery.Inventory{}, cfg, reg)
	if _, err := Apply(badDir, "obsynx.yaml", cfg, plan); err == nil {
		t.Fatal("expected Apply to fail when rootDir is a regular file")
	}
}

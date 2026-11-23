package diagnostics

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/adilsoncruz/obsynx/internal/capabilities"
	"github.com/adilsoncruz/obsynx/internal/config"
	"github.com/adilsoncruz/obsynx/internal/lifecycle"
)

// CheckResult describes a single diagnostic verification executed by `obsynx doctor`.
type CheckResult struct {
	Name    string `json:"name" yaml:"name"`
	Status  string `json:"status" yaml:"status"` // PASS, WARN, FAIL
	Details string `json:"details" yaml:"details"`
}

// SelfMetrics tracks operational metrics of the OBSynx Control Plane itself (Section 3.9).
type SelfMetrics struct {
	CommandsExecuted  uint64 `json:"commands_executed" yaml:"commands_executed"`
	AdaptersLoaded    int    `json:"adapters_loaded" yaml:"adapters_loaded"`
	DiscoveredTargets int    `json:"discovered_targets" yaml:"discovered_targets"`
}

var commandsCounter atomic.Uint64

// RecordCommand increments the internal command execution counter.
func RecordCommand() uint64 {
	return commandsCounter.Add(1)
}

// RunDoctor executes environment, capability registry, and collector configuration checks.
func RunDoctor(rootDir, configPath string, reg *capabilities.Registry) ([]CheckResult, SelfMetrics) {
	var results []CheckResult

	// 1. OS & eBPF host capability check
	osCheck := CheckResult{
		Name:    "host_os_ebpf",
		Status:  "WARN",
		Details: fmt.Sprintf("Host OS is %s/%s; OBI/eBPF requires a Linux container/VM kernel (Docker Desktop / OrbStack / Colima).", runtime.GOOS, runtime.GOARCH),
	}
	if runtime.GOOS == "linux" {
		osCheck = CheckResult{
			Name:    "host_os_ebpf",
			Status:  "PASS",
			Details: "Running natively on Linux kernel; eBPF/OBI supported with CAP_BPF.",
		}
	}
	results = append(results, osCheck)

	// 2. Capability Registry check
	adaptersList := reg.List()
	results = append(results, CheckResult{
		Name:    "capability_registry",
		Status:  "PASS",
		Details: fmt.Sprintf("%d adapter contracts loaded and validated.", len(adaptersList)),
	})

	// 3. OpenTelemetry Collector config check
	collectorCfgPath := filepath.Join(rootDir, "deploy", "collector", "otel-collector-config.yaml")
	collectorCheck := CheckResult{
		Name:    "collector_pipeline_config",
		Status:  "WARN",
		Details: "Local Collector configuration not found under deploy/collector/otel-collector-config.yaml.",
	}
	if _, err := os.Stat(collectorCfgPath); err == nil {
		collectorCheck = CheckResult{
			Name:    "collector_pipeline_config",
			Status:  "PASS",
			Details: fmt.Sprintf("Collector config found at %s.", collectorCfgPath),
		}
	}
	results = append(results, collectorCheck)

	// 4. OBSynx configuration file check
	configCheck := CheckResult{
		Name:    "obsynx_config",
		Status:  "WARN",
		Details: fmt.Sprintf("Configuration %s not found (run 'obsynx init' to create it).", configPath),
	}
	if _, err := os.Stat(configPath); err == nil {
		configCheck = CheckResult{
			Name:    "obsynx_config",
			Status:  "PASS",
			Details: fmt.Sprintf("Configuration %s is valid.", configPath),
		}
		if _, loadErr := config.Load(configPath); loadErr != nil {
			configCheck = CheckResult{
				Name:    "obsynx_config",
				Status:  "FAIL",
				Details: fmt.Sprintf("Configuration %s is invalid: %v", configPath, loadErr),
			}
		}
	}
	results = append(results, configCheck)

	// 5. Managed artifacts integrity & drift check (Phase 6)
	if state, err := lifecycle.LoadState(rootDir); err == nil && state.Status == "applied" {
		drifts := lifecycle.VerifyIntegrity(rootDir, state)
		integrityCheck := CheckResult{
			Name:    "managed_state_integrity",
			Status:  "PASS",
			Details: fmt.Sprintf("All %d managed artifact(s) match SHA-256 checksums.", len(state.GeneratedFiles)),
		}
		if len(drifts) > 0 {
			integrityCheck = CheckResult{
				Name:    "managed_state_integrity",
				Status:  "WARN",
				Details: fmt.Sprintf("Configuration drift detected: %s", strings.Join(drifts, "; ")),
			}
		}
		results = append(results, integrityCheck)
	}

	metrics := SelfMetrics{
		CommandsExecuted: commandsCounter.Load(),
		AdaptersLoaded:   len(adaptersList),
	}

	return results, metrics
}

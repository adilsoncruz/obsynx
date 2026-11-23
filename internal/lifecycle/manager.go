package lifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/adilsoncruz/obsynx/internal/config"
	"github.com/adilsoncruz/obsynx/internal/orchestrator"
	"github.com/adilsoncruz/obsynx/internal/policy"
	"gopkg.in/yaml.v3"
)

const (
	StateFileRelPath        = ".obsynx/state.json"
	CollectorManagedRelPath = "deploy/collector/otel-collector.obsynx.yaml"
	ComposeOverrideRelPath  = "deploy/lab/docker-compose.obsynx.override.yml"
)

// ManagedState persists the current status, generated artifacts, SHA-256 checksums, efficiency summary, and audit history.
type ManagedState struct {
	Status          string                   `json:"status" yaml:"status"` // applied, rolled_back, uninitialized
	UpdatedAt       string                   `json:"updated_at" yaml:"updated_at"`
	ConfigPath      string                   `json:"config_path" yaml:"config_path"`
	GeneratedFiles  []string                 `json:"generated_files" yaml:"generated_files"`
	Checksums       map[string]string        `json:"checksums,omitempty" yaml:"checksums,omitempty"`
	ManagedServices []ManagedServiceSummary  `json:"managed_services" yaml:"managed_services"`
	Efficiency      policy.EfficiencySummary `json:"efficiency" yaml:"efficiency"`
	AuditTrail      []AuditEntry             `json:"audit_trail" yaml:"audit_trail"`
}

// ManagedServiceSummary records the applied adapter and strategy for a service.
type ManagedServiceSummary struct {
	ServiceName string `json:"service_name" yaml:"service_name"`
	Runtime     string `json:"runtime" yaml:"runtime"`
	Adapter     string `json:"adapter" yaml:"adapter"`
	Strategy    string `json:"strategy" yaml:"strategy"`
}

// AuditEntry logs apply and rollback actions (Section 11).
type AuditEntry struct {
	Timestamp string `json:"timestamp" yaml:"timestamp"`
	Action    string `json:"action" yaml:"action"`
	Summary   string `json:"summary" yaml:"summary"`
}

// Apply materializes the orchestration plan into non-destructive Collector and Docker Compose override files.
func Apply(rootDir, configPath string, cfg config.Config, plan orchestrator.Plan) (ManagedState, error) {
	prevState, _ := LoadState(rootDir)

	collectorBytes, err := policy.RenderCollectorConfig(cfg)
	if err != nil {
		return ManagedState{}, fmt.Errorf("render collector config: %w", err)
	}

	collectorPath := filepath.Join(rootDir, CollectorManagedRelPath)
	if err := os.MkdirAll(filepath.Dir(collectorPath), 0o755); err != nil {
		return ManagedState{}, err
	}
	if err := os.WriteFile(collectorPath, collectorBytes, 0o644); err != nil {
		return ManagedState{}, fmt.Errorf("write %s: %w", collectorPath, err)
	}

	overrideBytes, err := renderComposeOverride(plan)
	if err != nil {
		return ManagedState{}, fmt.Errorf("render compose override: %w", err)
	}

	overridePath := filepath.Join(rootDir, ComposeOverrideRelPath)
	if err := os.MkdirAll(filepath.Dir(overridePath), 0o755); err != nil {
		return ManagedState{}, err
	}
	if err := os.WriteFile(overridePath, overrideBytes, 0o644); err != nil {
		return ManagedState{}, fmt.Errorf("write %s: %w", overridePath, err)
	}

	var summaries []ManagedServiceSummary
	for _, act := range plan.Actions {
		summaries = append(summaries, ManagedServiceSummary{
			ServiceName: act.ServiceName,
			Runtime:     act.Runtime,
			Adapter:     act.SelectedAdapter,
			Strategy:    act.Strategy,
		})
	}

	checksums := map[string]string{
		CollectorManagedRelPath: sha256Hex(collectorBytes),
		ComposeOverrideRelPath:  sha256Hex(overrideBytes),
	}

	now := time.Now().UTC().Format(time.RFC3339)
	audit := append(prevState.AuditTrail, AuditEntry{
		Timestamp: now,
		Action:    "apply",
		Summary:   fmt.Sprintf("Applied instrumentation plan for %d services with SHA-256 verification", len(summaries)),
	})

	state := ManagedState{
		Status:          "applied",
		UpdatedAt:       now,
		ConfigPath:      configPath,
		GeneratedFiles:  []string{CollectorManagedRelPath, ComposeOverrideRelPath},
		Checksums:       checksums,
		ManagedServices: summaries,
		Efficiency:      plan.Efficiency,
		AuditTrail:      audit,
	}

	if err := saveState(rootDir, state); err != nil {
		return ManagedState{}, err
	}

	return state, nil
}

// VerifyIntegrity checks whether any OBSynx-managed file was manually tampered with or deleted since `apply`.
func VerifyIntegrity(rootDir string, state ManagedState) []string {
	if state.Status != "applied" {
		return nil
	}
	var drifts []string
	for _, relPath := range state.GeneratedFiles {
		fullPath := filepath.Join(rootDir, relPath)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			drifts = append(drifts, fmt.Sprintf("%s: file missing", relPath))
			continue
		}
		expected, hasChecksum := state.Checksums[relPath]
		if hasChecksum && sha256Hex(data) != expected {
			drifts = append(drifts, fmt.Sprintf("%s: SHA-256 checksum mismatch (manual modification detected)", relPath))
		}
	}
	return drifts
}

// Rollback removes OBSynx-managed override and collector files cleanly without touching user files.
func Rollback(rootDir string) (ManagedState, error) {
	state, err := LoadState(rootDir)
	if err != nil {
		return ManagedState{}, err
	}

	drifts := VerifyIntegrity(rootDir, state)
	for _, relPath := range state.GeneratedFiles {
		fullPath := filepath.Join(rootDir, relPath)
		_ = os.Remove(fullPath)
	}

	summary := "Reverted managed override and collector configurations"
	if len(drifts) > 0 {
		summary = fmt.Sprintf("Reverted managed configurations (resolved %d file drift(s))", len(drifts))
	}

	now := time.Now().UTC().Format(time.RFC3339)
	state.Status = "rolled_back"
	state.UpdatedAt = now
	state.GeneratedFiles = []string{}
	state.Checksums = nil
	state.AuditTrail = append(state.AuditTrail, AuditEntry{
		Timestamp: now,
		Action:    "rollback",
		Summary:   summary,
	})

	if err := saveState(rootDir, state); err != nil {
		return ManagedState{}, err
	}
	return state, nil
}

// LoadState reads `.obsynx/state.json`, returning an uninitialized state if not yet applied.
func LoadState(rootDir string) (ManagedState, error) {
	statePath := filepath.Join(rootDir, StateFileRelPath)
	data, err := os.ReadFile(statePath)
	if err != nil {
		return ManagedState{
			Status:         "uninitialized",
			GeneratedFiles: []string{},
		}, nil
	}

	var st ManagedState
	if err := json.Unmarshal(data, &st); err != nil {
		return ManagedState{}, fmt.Errorf("parse %s: %w", statePath, err)
	}
	return st, nil
}

func saveState(rootDir string, st ManagedState) error {
	statePath := filepath.Join(rootDir, StateFileRelPath)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath, data, 0o644)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func renderComposeOverride(plan orchestrator.Plan) ([]byte, error) {
	services := make(map[string]any)
	services["otel-collector"] = map[string]any{
		"volumes": []string{
			"../collector/otel-collector.obsynx.yaml:/etc/otelcol-contrib/config.yaml:ro",
		},
	}

	// Init container that populates /obsynx-agents volume for JVM, Node.js, and Python
	// so application Dockerfiles remain 100% pure with zero OpenTelemetry packages inside.
	services["obsynx-agent-injector"] = map[string]any{
		"build": map[string]string{
			"context":    "../../adapters",
			"dockerfile": "Dockerfile.injector",
		},
		"container_name": "obsynx-agent-injector",
		"volumes": []string{
			"obsynx-agents:/obsynx-agents",
		},
	}

	for _, act := range plan.Actions {
		var envList []string
		keys := make([]string, 0, len(act.GeneratedEnv))
		for k := range act.GeneratedEnv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			envList = append(envList, fmt.Sprintf("%s=%s", k, act.GeneratedEnv[k]))
		}

		svcMap := map[string]any{
			"environment": envList,
		}
		switch act.SelectedAdapter {
		case "opentelemetry-java", "opentelemetry-nodejs", "opentelemetry-python", "opentelemetry-dotnet":
			svcMap["volumes"] = []string{
				"obsynx-agents:/obsynx-agents:ro",
			}
			svcMap["depends_on"] = map[string]any{
				"obsynx-agent-injector": map[string]string{
					"condition": "service_completed_successfully",
				},
				"otel-collector": map[string]string{
					"condition": "service_started",
				},
			}
		}
		services[act.ServiceName] = svcMap

		// When eBPF strategy is selected for a service (and not suppressed due to dual instrumentation), configure its observer sidecar override.
		if act.SelectedAdapter == "obi-ebpf" && !act.SuppressEBPFSidecar {
			services[act.ServiceName] = map[string]any{
				"environment": append(envList, "PORT=18080"),
			}
			services["obi-ebpf-"+act.ServiceName] = map[string]any{
				"image":        "python:3.12-alpine",
				"network_mode": "service:" + act.ServiceName,
				"command":      []string{"python", "/obsynx-agents/ebpf/go-observer.py"},
				"volumes": []string{
					"obsynx-agents:/obsynx-agents:ro",
					"../../examples/" + act.ServiceName + ":/app:ro",
				},
				"depends_on": map[string]any{
					"obsynx-agent-injector": map[string]string{
						"condition": "service_completed_successfully",
					},
					act.ServiceName: map[string]string{
						"condition": "service_started",
					},
				},
				"environment": []string{
					"OBSYNX_GO_TARGET_URL=http://127.0.0.1:18080",
					"OBSYNX_PROXY_PORT=8080",
					"OTEL_SERVICE_NAME=" + act.ServiceName,
					"OTEL_EXPORTER_OTLP_ENDPOINT=" + plan.CollectorEndpoint,
					"OTEL_EXPORTER_OTLP_PROTOCOL=grpc",
				},
			}
		}
	}

	doc := map[string]any{
		"volumes": map[string]any{
			"obsynx-agents": map[string]any{},
		},
		"services": services,
	}
	return yaml.Marshal(doc)
}

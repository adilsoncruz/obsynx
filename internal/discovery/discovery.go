package discovery

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Confidence indicates whether a discovery attribute was observed directly or inferred.
type Confidence string

const (
	ConfidenceObserved Confidence = "observed"
	ConfidenceInferred Confidence = "inferred"
)

// DiscoveredService represents a single application found in the monitored environment.
type DiscoveredService struct {
	Name                    string            `json:"name" yaml:"name"`
	Runtime                 string            `json:"runtime" yaml:"runtime"`
	RuntimeVersion          string            `json:"runtime_version,omitempty" yaml:"runtime_version,omitempty"`
	Framework               string            `json:"framework,omitempty" yaml:"framework,omitempty"`
	ContainerName           string            `json:"container_name,omitempty" yaml:"container_name,omitempty"`
	Ports                   []string          `json:"ports,omitempty" yaml:"ports,omitempty"`
	ExistingInstrumentation []string          `json:"existing_instrumentation,omitempty" yaml:"existing_instrumentation,omitempty"`
	Source                  string            `json:"source" yaml:"source"`
	Confidence              Confidence        `json:"confidence" yaml:"confidence"`
	Evidence                map[string]string `json:"evidence,omitempty" yaml:"evidence,omitempty"`
}

// Inventory holds the structured output of a discovery scan (Section 7.3).
type Inventory struct {
	GeneratedAt      string              `json:"generated_at" yaml:"generated_at"`
	CollectorPresent bool                `json:"collector_present" yaml:"collector_present"`
	CollectorSource  string              `json:"collector_source,omitempty" yaml:"collector_source,omitempty"`
	Services         []DiscoveredService `json:"services" yaml:"services"`
}

// ComposeSpec is a minimal parser for docker-compose.yml service definitions.
type composeSpec struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Image         string   `yaml:"image"`
	ContainerName string   `yaml:"container_name"`
	Pid           string   `yaml:"pid"`
	Ports         []string `yaml:"ports"`
	Environment   []string `yaml:"environment"`
	Build         struct {
		Context string `yaml:"context"`
	} `yaml:"build"`
}

// Discover scans the workspace and optional compose/example descriptors without collecting sensitive secrets.
func Discover(rootDir string) (Inventory, error) {
	inv := Inventory{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Services:    []DiscoveredService{},
	}

	composePath := filepath.Join(rootDir, "deploy", "lab", "docker-compose.yml")
	if data, err := os.ReadFile(composePath); err == nil {
		parseComposeInventory(rootDir, composePath, data, &inv)
		enrichWithLiveRuntime(&inv)
		return inv, nil
	}

	// Fallback: inspect examples/ directory directly if compose file is absent.
	examplesDir := filepath.Join(rootDir, "examples")
	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		return inv, nil
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		svcDir := filepath.Join(examplesDir, entry.Name())
		if svc, ok := inspectSourceDir(entry.Name(), svcDir); ok {
			inv.Services = append(inv.Services, svc)
		}
	}

	enrichWithLiveRuntime(&inv)
	return inv, nil
}

func parseComposeInventory(rootDir, composePath string, data []byte, inv *Inventory) {
	var spec composeSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return
	}

	ebpfTargets := make(map[string]string)
	for name, svc := range spec.Services {
		if strings.HasPrefix(svc.Pid, "service:") {
			targetSvc := strings.TrimPrefix(svc.Pid, "service:")
			ebpfTargets[targetSvc] = "ebpf-sidecar:" + name
		}
	}

	for name, svc := range spec.Services {
		if isInfrastructureComponent(name, svc.Image) {
			if strings.Contains(name, "collector") || strings.Contains(svc.Image, "opentelemetry-collector") {
				inv.CollectorPresent = true
				inv.CollectorSource = composePath
			}
			continue
		}

		discovered := DiscoveredService{
			Name:          name,
			ContainerName: svc.ContainerName,
			Ports:         svc.Ports,
			Source:        composePath,
			Confidence:    ConfidenceObserved,
			Evidence:      make(map[string]string),
		}

		// Inspect build context files to determine runtime & framework safely.
		if svc.Build.Context != "" {
			ctxDir := filepath.Clean(filepath.Join(filepath.Dir(composePath), svc.Build.Context))
			if inspected, ok := inspectSourceDir(name, ctxDir); ok {
				discovered.Runtime = inspected.Runtime
				discovered.RuntimeVersion = inspected.RuntimeVersion
				discovered.Framework = inspected.Framework
				for k, v := range inspected.Evidence {
					discovered.Evidence[k] = v
				}
			}
		}

		// Detect existing OpenTelemetry instrumentation without capturing secret values (Section 7.2 & 11).
		for _, envEntry := range svc.Environment {
			key, val, _ := strings.Cut(envEntry, "=")
			detectInstrumentationEnv(key, val, &discovered)
		}
		if ebpfSidecar, hasEBPF := ebpfTargets[name]; hasEBPF {
			discovered.ExistingInstrumentation = append(discovered.ExistingInstrumentation, ebpfSidecar)
		}

		if discovered.Runtime == "" {
			discovered.Runtime = "unknown"
			discovered.Confidence = ConfidenceInferred
		}

		inv.Services = append(inv.Services, discovered)
	}

	sort.Slice(inv.Services, func(i, j int) bool {
		return inv.Services[i].Name < inv.Services[j].Name
	})
}

func isInfrastructureComponent(name, image string) bool {
	infra := []string{"otel-collector", "tempo", "prometheus", "grafana", "loki", "obi-ebpf", "beyla"}
	lowerName := strings.ToLower(name)
	lowerImg := strings.ToLower(image)
	for _, token := range infra {
		if strings.Contains(lowerName, token) || strings.Contains(lowerImg, token) {
			return true
		}
	}
	return false
}

func inspectSourceDir(name, dir string) (DiscoveredService, bool) {
	svc := DiscoveredService{
		Name:       name,
		Source:     dir,
		Confidence: ConfidenceObserved,
		Evidence:   make(map[string]string),
	}

	if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
		svc.Runtime = "go"
		svc.Framework = "net/http"
		svc.Evidence["manifest"] = "go.mod"
		if _, ymlErr := os.Stat(filepath.Join(dir, "obsynx.yaml")); ymlErr == nil {
			svc.Evidence["project_config"] = filepath.Join(dir, "obsynx.yaml")
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "go ") {
				svc.RuntimeVersion = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "go "))
			}
		}
		return svc, true
	}

	if data, err := os.ReadFile(filepath.Join(dir, "build.gradle.kts")); err == nil {
		svc.Runtime = "jvm"
		svc.RuntimeVersion = "21"
		svc.Evidence["manifest"] = "build.gradle.kts"
		if _, ymlErr := os.Stat(filepath.Join(dir, "obsynx.yaml")); ymlErr == nil {
			svc.Evidence["project_config"] = filepath.Join(dir, "obsynx.yaml")
		}
		if strings.Contains(string(data), "org.springframework.boot") {
			svc.Framework = "spring-boot"
		}
		return svc, true
	}

	if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		svc.Runtime = "nodejs"
		svc.RuntimeVersion = "20"
		svc.Evidence["manifest"] = "package.json"
		if _, ymlErr := os.Stat(filepath.Join(dir, "obsynx.yaml")); ymlErr == nil {
			svc.Evidence["project_config"] = filepath.Join(dir, "obsynx.yaml")
		}
		if strings.Contains(string(data), "express") {
			svc.Framework = "express"
		}
		return svc, true
	}

	if data, err := os.ReadFile(filepath.Join(dir, "requirements.txt")); err == nil {
		svc.Runtime = "python"
		svc.RuntimeVersion = "3.12"
		svc.Evidence["manifest"] = "requirements.txt"
		if _, ymlErr := os.Stat(filepath.Join(dir, "obsynx.yaml")); ymlErr == nil {
			svc.Evidence["project_config"] = filepath.Join(dir, "obsynx.yaml")
		}
		if strings.Contains(strings.ToLower(string(data)), "flask") {
			svc.Framework = "flask"
		}
		if strings.Contains(strings.ToLower(string(data)), "fastapi") {
			svc.Framework = "fastapi"
		}
		return svc, true
	}

	// C# / .NET Project Detection (.csproj)
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".csproj") {
				svc.Runtime = "dotnet"
				svc.RuntimeVersion = "8.0"
				svc.Framework = "aspnetcore"
				svc.Evidence["manifest"] = e.Name()
				if _, ymlErr := os.Stat(filepath.Join(dir, "obsynx.yaml")); ymlErr == nil {
					svc.Evidence["project_config"] = filepath.Join(dir, "obsynx.yaml")
				}
				if csprojData, readErr := os.ReadFile(filepath.Join(dir, e.Name())); readErr == nil {
					content := string(csprojData)
					if idx := strings.Index(content, "<TargetFramework>"); idx != -1 {
						tf := content[idx+len("<TargetFramework>"):]
						if end := strings.Index(tf, "</TargetFramework>"); end != -1 {
							version := strings.TrimSpace(tf[:end])
							version = strings.TrimPrefix(version, "net")
							if version != "" {
								svc.RuntimeVersion = version
							}
						}
					}
				}
				return svc, true
			}
		}
	}

	return svc, false
}

func detectInstrumentationEnv(key, val string, svc *DiscoveredService) {
	switch key {
	case "JAVA_TOOL_OPTIONS":
		if strings.Contains(val, "opentelemetry-javaagent.jar") {
			svc.ExistingInstrumentation = append(svc.ExistingInstrumentation, "java-agent:opentelemetry-javaagent.jar")
		}
	case "NODE_OPTIONS":
		if strings.Contains(val, "@opentelemetry/auto-instrumentations-node") {
			svc.ExistingInstrumentation = append(svc.ExistingInstrumentation, "nodejs-preload:@opentelemetry/auto-instrumentations-node")
		}
	case "CORECLR_ENABLE_PROFILING":
		if val == "1" {
			svc.ExistingInstrumentation = append(svc.ExistingInstrumentation, "dotnet-profiler:enabled")
		}
	case "OTEL_INSTRUMENTATION_METHODS_INCLUDE":
		svc.ExistingInstrumentation = append(svc.ExistingInstrumentation, "custom-methods:"+val)
	case "OTEL_EXPORTER_OTLP_ENDPOINT":
		svc.ExistingInstrumentation = append(svc.ExistingInstrumentation, "otlp-exporter-configured")
	}
}

// HasDualInstrumentation checks whether a service has both an in-process agent/SDK and an eBPF sidecar attached.
func (s DiscoveredService) HasDualInstrumentation() bool {
	hasAgentOrSDK := false
	hasEBPF := false
	for _, item := range s.ExistingInstrumentation {
		if strings.HasPrefix(item, "java-agent:") || strings.HasPrefix(item, "nodejs-preload:") || strings.HasPrefix(item, "dotnet-profiler:") || strings.HasPrefix(item, "sdk:") {
			hasAgentOrSDK = true
		}
		if strings.HasPrefix(item, "ebpf-sidecar:") {
			hasEBPF = true
		}
	}
	return hasAgentOrSDK && hasEBPF
}

// enrichWithLiveRuntime checks running Docker containers and local processes with minimal privileges,
// recording runtime state without inspecting or leaking secret environment variables.
func enrichWithLiveRuntime(inv *Inventory) {
	runningContainers := queryRunningDockerContainers()
	runningProcs := queryRunningProcesses()

	for i := range inv.Services {
		svc := &inv.Services[i]
		if svc.Evidence == nil {
			svc.Evidence = make(map[string]string)
		}
		if status, ok := runningContainers[svc.ContainerName]; ok {
			svc.Evidence["container_state"] = status
			continue
		}
		if status, ok := runningContainers[svc.Name]; ok {
			svc.Evidence["container_state"] = status
			continue
		}
		if matchProcessName(svc.Name, runningProcs) {
			svc.Evidence["host_process"] = "running"
			continue
		}
		svc.Evidence["container_state"] = "not-running (manifest-discovered)"
	}
}

func queryRunningDockerContainers() map[string]string {
	result := make(map[string]string)
	out, err := exec.Command("docker", "ps", "--format", "{{.Names}}={{.Status}}").Output()
	if err != nil {
		return result
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, status, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && name != "" {
			result[name] = status
		}
	}
	return result
}

func queryRunningProcesses() []string {
	out, err := exec.Command("ps", "-eo", "comm=").Output()
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

func matchProcessName(serviceName string, procs []string) bool {
	for _, p := range procs {
		if strings.Contains(strings.ToLower(p), strings.ToLower(serviceName)) {
			return true
		}
	}
	return false
}

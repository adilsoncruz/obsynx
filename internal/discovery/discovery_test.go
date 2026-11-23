package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverLabWorkspace(t *testing.T) {
	inv, err := Discover("../..")
	if err != nil {
		t.Fatalf("unexpected error discovering workspace: %v", err)
	}
	if !inv.CollectorPresent {
		t.Fatal("expected CollectorPresent to be true in lab workspace")
	}
	if len(inv.Services) != 5 {
		t.Fatalf("expected 5 discovered application services, got %d", len(inv.Services))
	}

	var foundDotnet bool
	for _, svc := range inv.Services {
		if svc.Name == "dotnet-api" {
			foundDotnet = true
			if svc.Runtime != "dotnet" {
				t.Fatalf("expected dotnet-api runtime to be 'dotnet', got %q", svc.Runtime)
			}
			if svc.RuntimeVersion != "8.0" {
				t.Fatalf("expected dotnet-api runtime_version to be '8.0', got %q", svc.RuntimeVersion)
			}
			if svc.Framework != "aspnetcore" {
				t.Fatalf("expected dotnet-api framework to be 'aspnetcore', got %q", svc.Framework)
			}
		}
	}
	if !foundDotnet {
		t.Fatal("expected dotnet-api to be found in discovered inventory")
	}
}

func TestDetectInstrumentationEnvAndDualInstrumentation(t *testing.T) {
	var svc DiscoveredService
	detectInstrumentationEnv("JAVA_TOOL_OPTIONS", "-javaagent:opentelemetry-javaagent.jar", &svc)
	detectInstrumentationEnv("NODE_OPTIONS", "--require @opentelemetry/auto-instrumentations-node", &svc)
	detectInstrumentationEnv("CORECLR_ENABLE_PROFILING", "1", &svc)
	detectInstrumentationEnv("OTEL_INSTRUMENTATION_METHODS_INCLUDE", "com.test.Service[execute]", &svc)
	detectInstrumentationEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4317", &svc)

	if len(svc.ExistingInstrumentation) != 5 {
		t.Fatalf("expected 5 instrumentation entries, got %d", len(svc.ExistingInstrumentation))
	}

	if svc.HasDualInstrumentation() {
		t.Fatal("expected false when no eBPF sidecar attached")
	}

	svc.ExistingInstrumentation = append(svc.ExistingInstrumentation, "ebpf-sidecar:obi-ebpf")
	if !svc.HasDualInstrumentation() {
		t.Fatal("expected true when both in-process agent and ebpf sidecar attached")
	}

	// Test process matching
	procs := []string{"/usr/local/bin/dotnet", "node", "python3"}
	if !matchProcessName("dotnet", procs) {
		t.Fatal("expected matchProcessName to match dotnet")
	}
	if matchProcessName("non-existent-app", procs) {
		t.Fatal("expected matchProcessName false for non-existent-app")
	}
}

func TestDiscoverMissingComposeAndExamplesFallback(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Completely empty dir
	inv, err := Discover(tmpDir)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(inv.Services) != 0 {
		t.Fatalf("expected 0 services in empty dir, got %d", len(inv.Services))
	}

	// 2. Dir with examples/ but without docker-compose.yml
	exDir := filepath.Join(tmpDir, "examples", "test-svc")
	_ = os.MkdirAll(exDir, 0o755)
	_ = os.WriteFile(filepath.Join(exDir, "go.mod"), []byte("module test-svc\ngo 1.22\n"), 0o644)

	inv2, err := Discover(tmpDir)
	if err != nil {
		t.Fatalf("Discover fallback failed: %v", err)
	}
	if len(inv2.Services) != 1 || inv2.Services[0].Name != "test-svc" {
		t.Fatalf("expected 1 service from examples fallback, got %+v", inv2.Services)
	}
}

func TestParseComposeInventoryEdgeCases(t *testing.T) {
	composeYAML := `
services:
  otel-collector:
    image: otel/opentelemetry-collector-contrib:0.114.0
  obi-ebpf-sidecar:
    image: custom/ebpf
    pid: "service:my-app"
  my-app:
    image: my-company/app:latest
    container_name: my-app-container
    ports:
      - "8080:8080"
`
	var inv Inventory
	parseComposeInventory("/tmp", "/tmp/docker-compose.yml", []byte(composeYAML), &inv)

	if !inv.CollectorPresent {
		t.Fatal("expected CollectorPresent true")
	}
	if len(inv.Services) != 1 || inv.Services[0].Name != "my-app" {
		t.Fatalf("expected 1 service my-app, got %+v", inv.Services)
	}
	if inv.Services[0].Runtime != "unknown" || inv.Services[0].Confidence != ConfidenceInferred {
		t.Fatalf("expected unknown runtime and inferred confidence, got %+v", inv.Services[0])
	}
	if len(inv.Services[0].ExistingInstrumentation) != 1 || inv.Services[0].ExistingInstrumentation[0] != "ebpf-sidecar:obi-ebpf-sidecar" {
		t.Fatalf("expected attached ebpf sidecar, got %+v", inv.Services[0].ExistingInstrumentation)
	}

	// Corrupt YAML
	var invCorrupt Inventory
	parseComposeInventory("/tmp", "/tmp/docker-compose.yml", []byte("[invalid"), &invCorrupt)
	if len(invCorrupt.Services) != 0 {
		t.Fatal("expected 0 services for corrupt YAML")
	}
}

func TestEnrichWithLiveRuntimeAndInspectSourceDir(t *testing.T) {
	inv := &Inventory{
		Services: []DiscoveredService{
			{Name: "non-existent-svc", ContainerName: "non-existent-container"},
			{Name: "go", ContainerName: "go"},
		},
	}
	enrichWithLiveRuntime(inv)
	if inv.Services[0].Evidence == nil {
		t.Fatal("expected evidence initialized")
	}
	if inv.Services[0].Evidence["container_state"] == "" {
		t.Fatal("expected container_state evidence recorded")
	}

	_ = queryRunningProcesses()
	_ = queryRunningDockerContainers()

	// inspectSourceDir with requirements.txt having fastapi
	tmpDir := t.TempDir()
	reqFile := filepath.Join(tmpDir, "requirements.txt")
	_ = os.WriteFile(reqFile, []byte("fastapi==0.110.0\nuvicorn==0.28.0\n"), 0o644)
	svc, ok := inspectSourceDir("test-svc", tmpDir)
	if !ok || svc.Runtime != "python" || svc.Framework != "fastapi" {
		t.Fatalf("expected python fastapi service, got %+v, ok=%v", svc, ok)
	}

	// empty dir inspect
	emptyDir := t.TempDir()
	_, okEmpty := inspectSourceDir("empty-svc", emptyDir)
	if okEmpty {
		t.Fatal("expected inspectSourceDir false on empty directory")
	}
}

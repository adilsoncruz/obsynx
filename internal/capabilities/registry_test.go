package capabilities

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRegistryAndFindByRuntime(t *testing.T) {
	reg, err := LoadRegistry("")
	if err != nil {
		t.Fatalf("expected embedded registry to load without error, got: %v", err)
	}

	all := reg.List()
	if len(all) < 5 {
		t.Fatalf("expected at least 5 embedded adapters, got %d", len(all))
	}

	jvmAdapters := reg.FindByRuntime("kotlin")
	if len(jvmAdapters) == 0 || jvmAdapters[0].ID != "opentelemetry-java" {
		t.Fatalf("expected primary adapter for kotlin to be opentelemetry-java, got %+v", jvmAdapters)
	}

	goAdapters := reg.FindByRuntime("go")
	if len(goAdapters) == 0 || goAdapters[0].ID != "obi-ebpf" {
		t.Fatalf("expected obi-ebpf adapter for go runtime, got %+v", goAdapters)
	}

	dotnetAdapters := reg.FindByRuntime("csharp")
	if len(dotnetAdapters) == 0 || dotnetAdapters[0].ID != "opentelemetry-dotnet" {
		t.Fatalf("expected opentelemetry-dotnet adapter for csharp runtime, got %+v", dotnetAdapters)
	}

	spec, ok := reg.Get("opentelemetry-dotnet")
	if !ok || spec.Runtime != "dotnet" {
		t.Fatalf("expected to Get opentelemetry-dotnet spec")
	}

	_, ok = reg.Get("non-existent")
	if ok {
		t.Fatal("expected Get non-existent to return false")
	}
}

func TestLevelValidationAndSupports(t *testing.T) {
	validLevels := []Level{LevelSupported, LevelUnsupported, LevelConditional, LevelRestricted}
	for _, l := range validLevels {
		if !l.IsValid() {
			t.Fatalf("expected level %s to be valid", l)
		}
	}
	if Level("invalid").IsValid() {
		t.Fatal("expected invalid level to be invalid")
	}

	if !LevelSupported.Supports() || !LevelConditional.Supports() {
		t.Fatal("expected supported and conditional to return Supports() == true")
	}
	if LevelUnsupported.Supports() || LevelRestricted.Supports() {
		t.Fatal("expected unsupported and restricted to return Supports() == false")
	}
}

func TestAdapterSpecValidation(t *testing.T) {
	spec := AdapterSpec{}
	if err := spec.Validate(); err == nil || err.Error() != "adapter id is required" {
		t.Fatalf("expected id error, got %v", err)
	}

	spec.ID = "test"
	if err := spec.Validate(); err == nil || err.Error() != "adapter test: runtime is required" {
		t.Fatalf("expected runtime error, got %v", err)
	}

	spec.Runtime = "dotnet"
	if err := spec.Validate(); err == nil || err.Error() != "adapter test: strategy is required" {
		t.Fatalf("expected strategy error, got %v", err)
	}

	spec.Strategy = "runtime-preload"
	spec.Capabilities.AutomaticHTTP = "invalid-level"
	if err := spec.Validate(); err == nil {
		t.Fatal("expected invalid capability level error")
	}

	spec.Capabilities = Matrix{
		AutomaticHTTP:         LevelSupported,
		AutomaticDatabase:     LevelSupported,
		AutomaticMessaging:    LevelSupported,
		CustomMethodTracing:   LevelSupported,
		CustomMethodArguments: LevelRestricted,
		CustomReturnValues:    LevelRestricted,
		Metrics:               LevelSupported,
		Traces:                LevelSupported,
		Logs:                  LevelSupported,
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("expected valid spec to pass, got %v", err)
	}
}

func TestNormalizeRuntimeAliases(t *testing.T) {
	cases := map[string]string{
		"java":        "jvm",
		"kotlin":      "jvm",
		"spring":      "jvm",
		"springboot":  "jvm",
		"golang":      "go",
		"go":          "go",
		"node":        "nodejs",
		"nodejs":      "nodejs",
		"js":          "nodejs",
		"typescript":  "nodejs",
		"py":          "python",
		"python":      "python",
		"csharp":      "dotnet",
		"dotnet":      "dotnet",
		".net":        "dotnet",
		"ebpf":        "any-linux-elf",
		"obi":         "any-linux-elf",
		"linux":       "any-linux-elf",
		"rust":        "rust",
	}

	for input, expected := range cases {
		if got := normalizeRuntime(input); got != expected {
			t.Fatalf("normalizeRuntime(%q): expected %q, got %q", input, expected, got)
		}
	}
}

func TestLoadRegistryWithOverrideDir(t *testing.T) {
	tmpDir := t.TempDir()
	overrideSubdir := filepath.Join(tmpDir, "custom")
	_ = os.MkdirAll(overrideSubdir, 0o755)

	validYAML := `
adapter:
  id: custom-adapter
  runtime: custom
  strategy: custom-strategy
  capabilities:
    automatic_http: true
    automatic_database: true
    automatic_messaging: true
    custom_method_tracing: true
    custom_method_arguments: restricted
    custom_return_values: restricted
    metrics: true
    traces: true
    logs: true
`
	if err := os.WriteFile(filepath.Join(overrideSubdir, "capabilities.yaml"), []byte(validYAML), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}

	reg, err := LoadRegistry(tmpDir)
	if err != nil {
		t.Fatalf("LoadRegistry with override failed: %v", err)
	}

	if _, ok := reg.Get("custom-adapter"); !ok {
		t.Fatal("expected custom-adapter to be loaded from overrideDir")
	}

	// Non-existent directory returns reg with embedded
	if r, err := LoadRegistry("/non/existent/path/override"); err != nil || len(r.List()) == 0 {
		t.Fatal("expected non-existent dir to fallback to embedded")
	}

	// File instead of directory returns reg with embedded
	tmpFile := filepath.Join(tmpDir, "some-file.txt")
	_ = os.WriteFile(tmpFile, []byte("hello"), 0o644)
	if r, err := LoadRegistry(tmpFile); err != nil || len(r.List()) == 0 {
		t.Fatal("expected file path to fallback to embedded")
	}

	// Corrupt override file should fail
	corruptDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(corruptDir, "capabilities.yaml"), []byte("invalid-yaml: ["), 0o644)
	if _, err := LoadRegistry(corruptDir); err == nil {
		t.Fatal("expected corrupt YAML to fail LoadRegistry")
	}

	// Invalid spec validation should fail
	invalidDir := t.TempDir()
	invalidSpecYAML := `
adapter:
  id: ""
  runtime: ""
`
	_ = os.WriteFile(filepath.Join(invalidDir, "capabilities.yaml"), []byte(invalidSpecYAML), 0o644)
	if _, err := LoadRegistry(invalidDir); err == nil {
		t.Fatal("expected invalid spec to fail LoadRegistry")
	}
}

package capabilities

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/adilsoncruz/obsynx/adapters"
	"gopkg.in/yaml.v3"
)

// Level represents whether a capability is supported, unsupported, conditional, or restricted.
type Level string

const (
	LevelSupported   Level = "true"
	LevelUnsupported Level = "false"
	LevelConditional Level = "conditional"
	LevelRestricted  Level = "restricted"
)

// IsValid checks if the capability level matches the allowed contract values.
func (l Level) IsValid() bool {
	switch l {
	case LevelSupported, LevelUnsupported, LevelConditional, LevelRestricted:
		return true
	default:
		return false
	}
}

// Supports returns true when the capability is either directly supported or conditional.
func (l Level) Supports() bool {
	return l == LevelSupported || l == LevelConditional
}

// Matrix holds the declared capabilities of an adapter.
type Matrix struct {
	AutomaticHTTP         Level `yaml:"automatic_http" json:"automatic_http"`
	AutomaticDatabase     Level `yaml:"automatic_database" json:"automatic_database"`
	AutomaticMessaging    Level `yaml:"automatic_messaging" json:"automatic_messaging"`
	CustomMethodTracing   Level `yaml:"custom_method_tracing" json:"custom_method_tracing"`
	CustomMethodArguments Level `yaml:"custom_method_arguments" json:"custom_method_arguments"`
	CustomReturnValues    Level `yaml:"custom_return_values" json:"custom_return_values"`
	Metrics               Level `yaml:"metrics" json:"metrics"`
	Traces                Level `yaml:"traces" json:"traces"`
	Logs                  Level `yaml:"logs" json:"logs"`
}

// AdapterSpec describes an instrumentation adapter and its capability contract.
type AdapterSpec struct {
	ID           string   `yaml:"id" json:"id"`
	Runtime      string   `yaml:"runtime" json:"runtime"`
	Strategy     string   `yaml:"strategy" json:"strategy"`
	Version      string   `yaml:"version,omitempty" json:"version,omitempty"`
	Capabilities Matrix   `yaml:"capabilities" json:"capabilities"`
	Requirements []string `yaml:"requirements" json:"requirements"`
	Constraints  []string `yaml:"constraints" json:"constraints"`
	Source       string   `yaml:"-" json:"source"`
}

type adapterDocument struct {
	Adapter AdapterSpec `yaml:"adapter"`
}

// Validate checks that required fields and capability levels conform to the OBSynx contract.
func (a AdapterSpec) Validate() error {
	if strings.TrimSpace(a.ID) == "" {
		return fmt.Errorf("adapter id is required")
	}
	if strings.TrimSpace(a.Runtime) == "" {
		return fmt.Errorf("adapter %s: runtime is required", a.ID)
	}
	if strings.TrimSpace(a.Strategy) == "" {
		return fmt.Errorf("adapter %s: strategy is required", a.ID)
	}

	checks := map[string]Level{
		"automatic_http":          a.Capabilities.AutomaticHTTP,
		"automatic_database":      a.Capabilities.AutomaticDatabase,
		"automatic_messaging":     a.Capabilities.AutomaticMessaging,
		"custom_method_tracing":   a.Capabilities.CustomMethodTracing,
		"custom_method_arguments": a.Capabilities.CustomMethodArguments,
		"custom_return_values":    a.Capabilities.CustomReturnValues,
		"metrics":                 a.Capabilities.Metrics,
		"traces":                  a.Capabilities.Traces,
		"logs":                    a.Capabilities.Logs,
	}

	for field, level := range checks {
		if !level.IsValid() {
			return fmt.Errorf("adapter %s: invalid capability level %q for %s", a.ID, level, field)
		}
	}
	return nil
}

// Registry stores and queries adapter capability contracts.
type Registry struct {
	byID map[string]AdapterSpec
}

// LoadRegistry loads embedded adapter capabilities and overlays any local directory overrides.
func LoadRegistry(overrideDir string) (*Registry, error) {
	reg := &Registry{
		byID: make(map[string]AdapterSpec),
	}

	if err := reg.loadFromFS(adapters.EmbeddedCapabilities, "embedded"); err != nil {
		return nil, err
	}

	if strings.TrimSpace(overrideDir) == "" {
		return reg, nil
	}

	info, err := os.Stat(overrideDir)
	if err != nil || !info.IsDir() {
		return reg, nil
	}

	if err := reg.loadFromFS(os.DirFS(overrideDir), "local:"+overrideDir); err != nil {
		return nil, err
	}

	return reg, nil
}

func (r *Registry) loadFromFS(filesystem fs.FS, sourcePrefix string) error {
	return fs.WalkDir(filesystem, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Base(path) != "capabilities.yaml" {
			return nil
		}

		data, readErr := fs.ReadFile(filesystem, path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}

		var doc adapterDocument
		if unmarshalErr := yaml.Unmarshal(data, &doc); unmarshalErr != nil {
			return fmt.Errorf("parse %s: %w", path, unmarshalErr)
		}

		spec := doc.Adapter
		spec.Source = sourcePrefix + "/" + path
		if valErr := spec.Validate(); valErr != nil {
			return valErr
		}

		r.byID[spec.ID] = spec
		return nil
	})
}

// Get returns an adapter by its ID.
func (r *Registry) Get(id string) (AdapterSpec, bool) {
	spec, ok := r.byID[id]
	return spec, ok
}

// List returns all registered adapters sorted by ID.
func (r *Registry) List() []AdapterSpec {
	result := make([]AdapterSpec, 0, len(r.byID))
	for _, spec := range r.byID {
		result = append(result, spec)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

// FindByRuntime returns all adapters compatible with a given runtime (including fallback Linux/eBPF adapters).
func (r *Registry) FindByRuntime(runtime string) []AdapterSpec {
	normalized := normalizeRuntime(runtime)
	var matches []AdapterSpec

	for _, spec := range r.List() {
		if spec.Runtime == normalized {
			matches = append(matches, spec)
		}
	}

	// eBPF/OBI applies to Linux ELF processes (especially Go or native binaries, or as fallback).
	if ebpfSpec, ok := r.byID["obi-ebpf"]; ok && normalized != "any-linux-elf" {
		matches = append(matches, ebpfSpec)
	}

	return matches
}

func normalizeRuntime(runtime string) string {
	rt := strings.ToLower(strings.TrimSpace(runtime))
	switch rt {
	case "java", "kotlin", "jvm", "spring", "springboot":
		return "jvm"
	case "golang", "go":
		return "go"
	case "node", "nodejs", "js", "typescript":
		return "nodejs"
	case "py", "python":
		return "python"
	case "csharp", "dotnet", ".net":
		return "dotnet"
	case "ebpf", "obi", "linux":
		return "any-linux-elf"
	default:
		return rt
	}
}

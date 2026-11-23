package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/adilsoncruz/obsynx/internal/capabilities"
	"github.com/adilsoncruz/obsynx/internal/config"
	"github.com/adilsoncruz/obsynx/internal/dashboard"
	"github.com/adilsoncruz/obsynx/internal/diagnostics"
	"github.com/adilsoncruz/obsynx/internal/discovery"
	"github.com/adilsoncruz/obsynx/internal/lifecycle"
	"github.com/adilsoncruz/obsynx/internal/orchestrator"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	diagnostics.RecordCommand()

	fs := flag.NewFlagSet("obsynx", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "text", "Output format: text or json")
	configPath := fs.String("config", config.DefaultConfigPath, "Path to obsynx.yaml")
	adaptersDir := fs.String("adapters-dir", "adapters", "Optional directory for local adapter overrides")

	cleanArgs, parsedFormat, parsedCfg, parsedAdapters := extractGlobalFlags(args, *format, *configPath, *adaptersDir)
	if len(cleanArgs) == 0 {
		printUsage(stdout)
		return 0
	}

	reg, err := capabilities.LoadRegistry(parsedAdapters)
	if err != nil {
		fmt.Fprintf(stderr, "error loading capability registry: %v\n", err)
		return 1
	}

	cmd := cleanArgs[0]
	subArgs := cleanArgs[1:]

	switch cmd {
	case "init":
		return handleInit(parsedCfg, parsedFormat, stdout, stderr)
	case "config":
		return handleConfig(subArgs, parsedCfg, parsedFormat, stdout, stderr)
	case "adapters":
		return handleAdapters(subArgs, reg, parsedFormat, stdout, stderr)
	case "discover":
		return handleDiscover(parsedFormat, stdout, stderr)
	case "plan":
		return handlePlan(parsedCfg, reg, parsedFormat, stdout, stderr)
	case "apply":
		return handleApply(parsedCfg, reg, parsedFormat, stdout, stderr)
	case "dashboard":
		return handleDashboard(subArgs, parsedCfg, parsedFormat, stdout, stderr)
	case "status":
		return handleStatus(parsedFormat, stdout, stderr)
	case "rollback":
		return handleRollback(parsedFormat, stdout, stderr)
	case "instrument":
		return handleInstrument(subArgs, parsedCfg, reg, parsedFormat, stdout, stderr)
	case "doctor":
		return handleDoctor(parsedCfg, reg, parsedFormat, stdout)
	case "help", "--help", "-h":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", cmd)
		printUsage(stderr)
		return 1
	}
}

func extractGlobalFlags(args []string, defFormat, defCfg, defAdapters string) ([]string, string, string, string) {
	format := defFormat
	cfg := defCfg
	adaptersDir := defAdapters
	var remaining []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--format" && i+1 < len(args):
			format = args[i+1]
			i++
		case strings.HasPrefix(arg, "--format="):
			format = strings.TrimPrefix(arg, "--format=")
		case arg == "--config" && i+1 < len(args):
			cfg = args[i+1]
			i++
		case strings.HasPrefix(arg, "--config="):
			cfg = strings.TrimPrefix(arg, "--config=")
		case arg == "--adapters-dir" && i+1 < len(args):
			adaptersDir = args[i+1]
			i++
		case strings.HasPrefix(arg, "--adapters-dir="):
			adaptersDir = strings.TrimPrefix(arg, "--adapters-dir=")
		default:
			remaining = append(remaining, arg)
		}
	}
	return remaining, format, cfg, adaptersDir
}

func handleInit(configPath, format string, stdout, stderr io.Writer) int {
	cfg := config.DefaultConfig()
	if err := config.Save(configPath, cfg); err != nil {
		fmt.Fprintf(stderr, "failed to write %s: %v\n", configPath, err)
		return 1
	}
	if format == "json" {
		writeJSON(stdout, map[string]string{"status": "initialized", "config_path": configPath})
		return 0
	}
	fmt.Fprintf(stdout, "Initialized OBSynx configuration at %s\n", configPath)
	return 0
}

func handleConfig(args []string, configPath, format string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "validate" {
		fmt.Fprintln(stderr, "usage: obsynx config validate [service_or_project_yaml] [--config obsynx.yaml]")
		return 1
	}

	// Individual project/service validation mode:
	// e.g. `obsynx config validate kotlin-spring-api` or `obsynx config validate examples/go-api/obsynx.yaml`
	if len(args) >= 2 {
		target := strings.TrimSpace(args[1])
		projPath := resolveProjectConfigTarget(target)
		svc, err := config.LoadProjectConfig(projPath)
		if err != nil {
			fmt.Fprintf(stderr, "[OBSYNX VALIDATION FAILED] %v\n", err)
			return 1
		}
		if format == "json" {
			writeJSON(stdout, map[string]any{
				"valid":   true,
				"service": svc,
			})
			return 0
		}
		fmt.Fprintf(stdout, "[OK] Service %q (%s) is valid — %d function rule(s), arguments & return attributes verified against source code.\n",
			svc.Name, svc.SourceFile, len(svc.Instrumentation.Methods))
		return 0
	}

	// If run inside a project folder where `./obsynx.yaml` is a service-level config, validate it individually.
	if svc, err := config.LoadProjectConfig(configPath); err == nil && svc.Name != "" {
		if format == "json" {
			writeJSON(stdout, map[string]any{"valid": true, "service": svc})
			return 0
		}
		fmt.Fprintf(stdout, "[OK] Service %q (%s) is valid — %d function rule(s) verified against source code.\n",
			svc.Name, svc.SourceFile, len(svc.Instrumentation.Methods))
		return 0
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return 1
	}
	if format == "json" {
		writeJSON(stdout, map[string]any{
			"valid":       true,
			"config_path": configPath,
			"services":    cfg.Services,
		})
		return 0
	}
	fmt.Fprintf(stdout, "Global configuration %s is valid (%d project obsynx.yaml files verified against source code):\n", configPath, len(cfg.Services))
	for _, svc := range cfg.Services {
		fmt.Fprintf(stdout, "  [OK] %-20s (%s) — %d function rule(s), arguments & return attributes verified\n",
			svc.Name, svc.SourceFile, len(svc.Instrumentation.Methods))
	}
	return 0
}

func resolveProjectConfigTarget(target string) string {
	if strings.HasSuffix(target, ".yaml") || strings.HasSuffix(target, ".yml") {
		return target
	}
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		return target + "/obsynx.yaml"
	}
	candidate := "examples/" + target + "/obsynx.yaml"
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return target
}

func handleAdapters(args []string, reg *capabilities.Registry, format string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: obsynx adapters <list|explain> [runtime]")
		return 1
	}

	switch args[0] {
	case "list":
		list := reg.List()
		if format == "json" {
			writeJSON(stdout, list)
			return 0
		}
		fmt.Fprintf(stdout, "%-24s %-15s %-18s %-10s %s\n", "ADAPTER ID", "RUNTIME", "STRATEGY", "VERSION", "CUSTOM METHODS")
		for _, a := range list {
			fmt.Fprintf(stdout, "%-24s %-15s %-18s %-10s %s\n",
				a.ID, a.Runtime, a.Strategy, a.Version, a.Capabilities.CustomMethodTracing)
		}
		return 0

	case "explain":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: obsynx adapters explain <runtime>")
			return 1
		}
		matches := reg.FindByRuntime(args[1])
		if len(matches) == 0 {
			fmt.Fprintf(stderr, "no adapters found for runtime %q\n", args[1])
			return 1
		}
		if format == "json" {
			writeJSON(stdout, matches)
			return 0
		}
		for _, a := range matches {
			fmt.Fprintf(stdout, "Adapter: %s (runtime=%s, strategy=%s, version=%s)\n", a.ID, a.Runtime, a.Strategy, a.Version)
			fmt.Fprintf(stdout, "  Capabilities: http=%s, db=%s, messaging=%s, custom_methods=%s, metrics=%s, traces=%s, logs=%s\n",
				a.Capabilities.AutomaticHTTP,
				a.Capabilities.AutomaticDatabase,
				a.Capabilities.AutomaticMessaging,
				a.Capabilities.CustomMethodTracing,
				a.Capabilities.Metrics,
				a.Capabilities.Traces,
				a.Capabilities.Logs,
			)
			fmt.Fprintf(stdout, "  Requirements: %s\n", strings.Join(a.Requirements, ", "))
			for _, c := range a.Constraints {
				fmt.Fprintf(stdout, "  Constraint:   - %s\n", c)
			}
			fmt.Fprintln(stdout)
		}
		return 0

	default:
		fmt.Fprintf(stderr, "unknown adapters subcommand %q\n", args[0])
		return 1
	}
}

func handleDiscover(format string, stdout, stderr io.Writer) int {
	inv, err := discovery.Discover(".")
	if err != nil {
		fmt.Fprintf(stderr, "discovery error: %v\n", err)
		return 1
	}
	if format == "json" {
		writeJSON(stdout, inv)
		return 0
	}

	fmt.Fprintf(stdout, "Discovery Scan (%s) | Collector Present: %v\n\n", inv.GeneratedAt, inv.CollectorPresent)
	fmt.Fprintf(stdout, "%-20s %-10s %-14s %-12s %s\n", "SERVICE", "RUNTIME", "FRAMEWORK", "CONFIDENCE", "EXISTING INSTRUMENTATION")
	for _, s := range inv.Services {
		existing := "none"
		if len(s.ExistingInstrumentation) > 0 {
			existing = strings.Join(s.ExistingInstrumentation, ", ")
		}
		fmt.Fprintf(stdout, "%-20s %-10s %-14s %-12s %s\n",
			s.Name, s.Runtime, s.Framework, s.Confidence, existing)
	}
	return 0
}

func handlePlan(configPath string, reg *capabilities.Registry, format string, stdout, stderr io.Writer) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		cfg = config.DefaultConfig()
	}
	inv, err := discovery.Discover(".")
	if err != nil {
		fmt.Fprintf(stderr, "discovery failed: %v\n", err)
		return 1
	}

	plan := orchestrator.BuildPlan(inv, cfg, reg)
	if format == "json" {
		writeJSON(stdout, plan)
		return 0
	}

	fmt.Fprintf(stdout, "OBSynx Instrumentation Plan (Read-Only) | Target Collector: %s\n", plan.CollectorEndpoint)
	fmt.Fprintf(stdout, "Cost-Aware Policies: %s\n", strings.Join(plan.PoliciesApplied, " | "))
	fmt.Fprintf(stdout, "Estimated Telemetry Volume Reduction: ~%.1f%% (sampling=%.2f, excluded_paths=%d, redacted_attrs=%d, queue_size=%d)\n",
		plan.Efficiency.EstimatedSpanReductionPct,
		plan.Efficiency.SamplingRatio,
		plan.Efficiency.ExcludedPathsCount,
		plan.Efficiency.RedactedAttributesCount,
		plan.Efficiency.CollectorQueueSize,
	)
	for _, rec := range plan.Efficiency.Recommendations {
		fmt.Fprintf(stdout, "  [Cost Recommendation] %s\n", rec)
	}
	fmt.Fprintln(stdout)

	for _, act := range plan.Actions {
		printServiceAction(stdout, act)
	}
	return 0
}

func handleApply(configPath string, reg *capabilities.Registry, format string, stdout, stderr io.Writer) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		cfg = config.DefaultConfig()
	}
	inv, err := discovery.Discover(".")
	if err != nil {
		fmt.Fprintf(stderr, "discovery failed: %v\n", err)
		return 1
	}

	plan := orchestrator.BuildPlan(inv, cfg, reg)
	state, err := lifecycle.Apply(".", configPath, cfg, plan)
	if err != nil {
		fmt.Fprintf(stderr, "apply failed: %v\n", err)
		return 1
	}

	if format == "json" {
		writeJSON(stdout, state)
		return 0
	}

	fmt.Fprintf(stdout, "OBSynx Apply Completed (%s)\n", state.UpdatedAt)
	fmt.Fprintln(stdout, "Generated Artifacts:")
	for _, f := range state.GeneratedFiles {
		fmt.Fprintf(stdout, "  - %s\n", f)
	}
	fmt.Fprintln(stdout, "\nManaged Services:")
	for _, svc := range state.ManagedServices {
		fmt.Fprintf(stdout, "  - %-20s runtime=%-8s adapter=%-22s strategy=%s\n",
			svc.ServiceName, svc.Runtime, svc.Adapter, svc.Strategy)
	}
	return 0
}

func handleDashboard(args []string, configPath, format string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "generate" {
		fmt.Fprintln(stderr, "usage: obsynx dashboard generate [--output-dir <dir>] [--force]")
		return 1
	}

	fs := flag.NewFlagSet("dashboard generate", flag.ContinueOnError)
	outputDir := fs.String("output-dir", dashboard.DefaultOutputDir, "Directory where Grafana dashboard JSONs will be saved")
	force := fs.Bool("force", false, "Overwrite existing dashboard JSON files")

	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config %s: %v\n", configPath, err)
		return 1
	}

	if len(cfg.Services) == 0 {
		fmt.Fprintln(stdout, "No services declared with custom metrics in obsynx.yaml.")
		return 0
	}

	res, err := dashboard.GenerateFromServices(cfg.Services, dashboard.Options{
		OutputDir: *outputDir,
		Force:     *force,
	})
	if err != nil {
		fmt.Fprintf(stderr, "dashboard generation failed: %v\n", err)
		return 1
	}

	if format == "json" {
		writeJSON(stdout, res)
		return 0
	}

	fmt.Fprintf(stdout, "OBSynx Dashboard Generator (Output: %s)\n", *outputDir)
	if len(res.GeneratedFiles) > 0 {
		fmt.Fprintln(stdout, "Generated Dashboards:")
		for _, gf := range res.GeneratedFiles {
			fmt.Fprintf(stdout, "  [+] %s\n", gf)
		}
	}
	if len(res.SkippedFiles) > 0 {
		fmt.Fprintln(stdout, "Skipped Dashboards (already exist, use --force to overwrite):")
		for _, sf := range res.SkippedFiles {
			fmt.Fprintf(stdout, "  [-] %s\n", sf)
		}
	}
	return 0
}

func handleStatus(format string, stdout, stderr io.Writer) int {
	state, err := lifecycle.LoadState(".")
	if err != nil {
		fmt.Fprintf(stderr, "status error: %v\n", err)
		return 1
	}
	if format == "json" {
		writeJSON(stdout, state)
		return 0
	}

	fmt.Fprintf(stdout, "OBSynx Status: %s (updated_at=%s)\n", strings.ToUpper(state.Status), state.UpdatedAt)
	if len(state.GeneratedFiles) > 0 {
		fmt.Fprintf(stdout, "Managed Files: %s\n", strings.Join(state.GeneratedFiles, ", "))
	}
	if state.Efficiency.EstimatedSpanReductionPct > 0 {
		fmt.Fprintf(stdout, "Cost-Aware Efficiency: ~%.1f%% volume reduction | max_attrs=%d | max_events=%d | queue=%d\n",
			state.Efficiency.EstimatedSpanReductionPct,
			state.Efficiency.MaxAttributesPerSpan,
			state.Efficiency.MaxEventsPerSpan,
			state.Efficiency.CollectorQueueSize,
		)
	}
	if drifts := lifecycle.VerifyIntegrity(".", state); len(drifts) > 0 {
		for _, d := range drifts {
			fmt.Fprintf(stdout, "  [!] Drift Detected: %s\n", d)
		}
	}
	for _, svc := range state.ManagedServices {
		fmt.Fprintf(stdout, "  * %-20s [%s -> %s]\n", svc.ServiceName, svc.Adapter, svc.Strategy)
	}
	return 0
}

func handleRollback(format string, stdout, stderr io.Writer) int {
	state, err := lifecycle.Rollback(".")
	if err != nil {
		fmt.Fprintf(stderr, "rollback failed: %v\n", err)
		return 1
	}
	if format == "json" {
		writeJSON(stdout, state)
		return 0
	}
	fmt.Fprintf(stdout, "OBSynx Rollback Completed (%s) — removed managed overrides.\n", state.UpdatedAt)
	return 0
}

func handleInstrument(args []string, configPath string, reg *capabilities.Registry, format string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: obsynx instrument <list|explain> [service]")
		return 1
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		cfg = config.DefaultConfig()
	}
	inv, err := discovery.Discover(".")
	if err != nil {
		fmt.Fprintf(stderr, "discovery failed: %v\n", err)
		return 1
	}
	plan := orchestrator.BuildPlan(inv, cfg, reg)

	switch args[0] {
	case "list":
		if format == "json" {
			writeJSON(stdout, plan.Actions)
			return 0
		}
		fmt.Fprintf(stdout, "%-20s %-10s %-22s %-16s %s\n", "SERVICE", "RUNTIME", "ADAPTER", "STRATEGY", "STATUS")
		for _, act := range plan.Actions {
			fmt.Fprintf(stdout, "%-20s %-10s %-22s %-16s %s\n",
				act.ServiceName, act.Runtime, act.SelectedAdapter, act.Strategy, act.Status)
		}
		return 0

	case "explain":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: obsynx instrument explain <service>")
			return 1
		}
		target := args[1]
		for _, act := range plan.Actions {
			if act.ServiceName == target {
				if format == "json" {
					writeJSON(stdout, act)
					return 0
				}
				printServiceAction(stdout, act)
				return 0
			}
		}
		fmt.Fprintf(stderr, "service %q not found in discovered inventory\n", target)
		return 1

	default:
		fmt.Fprintf(stderr, "unknown instrument subcommand %q\n", args[0])
		return 1
	}
}

func handleDoctor(configPath string, reg *capabilities.Registry, format string, stdout io.Writer) int {
	checks, metrics := diagnostics.RunDoctor(".", configPath, reg)
	if format == "json" {
		writeJSON(stdout, map[string]any{"checks": checks, "self_metrics": metrics})
		return 0
	}

	fmt.Fprintln(stdout, "OBSynx Doctor Diagnostics:")
	for _, c := range checks {
		fmt.Fprintf(stdout, "  [%s] %-26s %s\n", c.Status, c.Name, c.Details)
	}
	fmt.Fprintf(stdout, "\nOBSynx Control Plane Metrics: adapters_loaded=%d commands_executed=%d\n",
		metrics.AdaptersLoaded, metrics.CommandsExecuted)
	return 0
}

func printServiceAction(stdout io.Writer, act orchestrator.ServiceAction) {
	fmt.Fprintf(stdout, "* Service: %s (runtime=%s)\n", act.ServiceName, act.Runtime)
	if act.ProjectConfigFile != "" {
		fmt.Fprintf(stdout, "  Project YAML: %s\n", act.ProjectConfigFile)
	}
	fmt.Fprintf(stdout, "  Recommended Adapter: %s (strategy=%s) [%s]\n", act.SelectedAdapter, act.Strategy, act.Status)
	keys := make([]string, 0, len(act.GeneratedEnv))
	for k := range act.GeneratedEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(stdout, "  Env: %s=%s\n", k, act.GeneratedEnv[k])
	}
	for _, m := range act.DerivedMetrics {
		fmt.Fprintf(stdout, "  Metric: %s\n", m)
	}
	for _, w := range act.Warnings {
		fmt.Fprintf(stdout, "  [!] %s\n", w)
	}
	if act.FallbackSuggestion != "" {
		fmt.Fprintf(stdout, "  [->] Alternative: %s\n", act.FallbackSuggestion)
	}
	if act.AssistedSnippet != "" {
		fmt.Fprintf(stdout, "  [Code Snippet]:\n    %s\n", strings.ReplaceAll(act.AssistedSnippet, "\n", "\n    "))
	}
	fmt.Fprintln(stdout)
}

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `OBSynx CLI — Universal Observability & Instrumentation Platform

Usage:
  obsynx <command> [arguments] [--format text|json] [--config obsynx.yaml]

Commands:
  init                           Create a baseline obsynx.yaml configuration
  config validate                Validate obsynx.yaml syntax and policies
  adapters list                  List all registered instrumentation adapters
  adapters explain <runtime>     Explain capabilities and constraints for a runtime
  discover                       Discover services, runtimes, and existing instrumentation
  plan                           Generate a read-only instrumentation and policy plan
  apply                          Materialize Collector & Docker Compose instrumentation overrides
  dashboard generate             Generate Grafana dashboards from declared metrics
  status                         Show current managed state and instrumented services
  instrument list                List instrumentation strategies per discovered service
  instrument explain <service>   Explain instrumentation details and warnings for a service
  rollback                       Revert OBSynx-managed overrides and configurations
  doctor                         Diagnose host capabilities, Collector, and OBSynx health`)
}

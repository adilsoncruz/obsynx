package orchestrator

import (
	"strings"
	"testing"

	"github.com/adilsoncruz/obsynx/internal/capabilities"
	"github.com/adilsoncruz/obsynx/internal/config"
	"github.com/adilsoncruz/obsynx/internal/discovery"
)

func TestBuildPlanGeneratesJVMMethodIncludeAndGoGracefulDegradation(t *testing.T) {
	reg, err := capabilities.LoadRegistry("")
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	cfg := config.DefaultConfig()
	_ = config.MergeProjectConfigs("../..", &cfg)
	inv := discovery.Inventory{
		CollectorPresent: true,
		Services: []discovery.DiscoveredService{
			{
				Name:    "kotlin-spring-api",
				Runtime: "jvm",
			},
			{
				Name:    "go-api",
				Runtime: "go",
			},
		},
	}

	plan := BuildPlan(inv, cfg, reg)
	if len(plan.Actions) != 2 {
		t.Fatalf("expected 2 actions in plan, got %d", len(plan.Actions))
	}

	jvmAction := plan.Actions[0]
	includeEnv := jvmAction.GeneratedEnv["OTEL_INSTRUMENTATION_METHODS_INCLUDE"]
	if !strings.Contains(includeEnv, "PaymentService[processPayment]") {
		t.Fatalf("expected JVM action to translate custom method rules to OTEL_INSTRUMENTATION_METHODS_INCLUDE, got %q", includeEnv)
	}
	if len(jvmAction.DerivedMetrics) == 0 {
		t.Fatal("expected JVM action to list derived business metrics (Counter & Histogram via spanmetrics)")
	}

	goAction := plan.Actions[1]
	if goAction.SelectedAdapter != "obi-ebpf" {
		t.Fatalf("expected Go service to select obi-ebpf, got %q", goAction.SelectedAdapter)
	}
	if len(goAction.Warnings) == 0 || !strings.Contains(goAction.Warnings[0], "Graceful Degradation") {
		t.Fatalf("expected Go service with custom method rule to emit Graceful Degradation warning, got %+v", goAction.Warnings)
	}
	if !strings.Contains(goAction.AssistedSnippet, `tracer.Start(ctx, "processPayment")`) {
		t.Fatalf("expected Go service to include assisted OTel Go SDK snippet, got %q", goAction.AssistedSnippet)
	}
	if jvmAction.GeneratedEnv["OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT"] != "32" {
		t.Fatalf("expected OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT=32 injected into agent env, got %q", jvmAction.GeneratedEnv["OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT"])
	}
}

func TestDualInstrumentationSuppressesDuplicateEBPF(t *testing.T) {
	reg, err := capabilities.LoadRegistry("")
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	cfg := config.DefaultConfig()
	_ = config.MergeProjectConfigs("../..", &cfg)
	inv := discovery.Inventory{
		CollectorPresent: true,
		Services: []discovery.DiscoveredService{
			{
				Name:                    "kotlin-spring-api",
				Runtime:                 "jvm",
				ExistingInstrumentation: []string{"java-agent:opentelemetry-javaagent.jar", "ebpf-sidecar:obi-ebpf"},
			},
		},
	}

	plan := BuildPlan(inv, cfg, reg)
	act := plan.Actions[0]
	if !act.SuppressEBPFSidecar {
		t.Fatal("expected SuppressEBPFSidecar=true when service has both Java Agent and eBPF sidecar")
	}
	if plan.Efficiency.DuplicateInstrumentationFix != 1 {
		t.Fatalf("expected Efficiency.DuplicateInstrumentationFix=1, got %d", plan.Efficiency.DuplicateInstrumentationFix)
	}
}

func TestPlanForOtherRuntimesAndTLS(t *testing.T) {
	reg, err := capabilities.LoadRegistry("")
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Collector.TLSEnabled = true
	cfg.Collector.CACertPath = "/ca.pem"
	cfg.Collector.AuthHeaderEnv = "AUTH_TOKEN"

	inv := discovery.Inventory{
		CollectorPresent: true,
		Services: []discovery.DiscoveredService{
			{
				Name:    "node-service",
				Runtime: "nodejs",
			},
			{
				Name:    "py-service",
				Runtime: "python",
			},
			{
				Name:    "dotnet-service",
				Runtime: "dotnet",
			},
			{
				Name:    "unknown-service",
				Runtime: "unknown-runtime-xyz",
			},
		},
	}

	plan := BuildPlan(inv, cfg, reg)
	if len(plan.Actions) != 4 {
		t.Fatalf("expected 4 actions, got %d", len(plan.Actions))
	}

	bySvc := make(map[string]ServiceAction)
	for _, a := range plan.Actions {
		bySvc[a.ServiceName] = a
	}

	// Node.js checks
	nodeAct := bySvc["node-service"]
	if !strings.Contains(nodeAct.GeneratedEnv["NODE_OPTIONS"], "@opentelemetry/auto-instrumentations-node") {
		t.Fatalf("expected nodejs preload in env, got: %+v", nodeAct.GeneratedEnv)
	}
	if nodeAct.GeneratedEnv["OTEL_EXPORTER_OTLP_CERTIFICATE"] != "/ca.pem" {
		t.Fatalf("expected TLS cert in env, got: %+v", nodeAct.GeneratedEnv)
	}
	if nodeAct.GeneratedEnv["OTEL_EXPORTER_OTLP_HEADERS"] != "${AUTH_TOKEN}" {
		t.Fatalf("expected Auth header in env, got: %+v", nodeAct.GeneratedEnv)
	}

	// Python checks
	pyAct := bySvc["py-service"]
	if pyAct.GeneratedEnv["OTEL_PYTHON_LOGGING_AUTO_INSTRUMENTATION_ENABLED"] != "true" {
		t.Fatalf("expected python logging enabled, got: %+v", pyAct.GeneratedEnv)
	}

	// .NET checks
	dotnetAct := bySvc["dotnet-service"]
	if dotnetAct.GeneratedEnv["CORECLR_ENABLE_PROFILING"] != "1" {
		t.Fatalf("expected CORECLR_ENABLE_PROFILING=1, got: %+v", dotnetAct.GeneratedEnv)
	}

	// Unknown runtime with standard registry falls back to ebpf
	unknownAct := bySvc["unknown-service"]
	if unknownAct.SelectedAdapter != "obi-ebpf" {
		t.Fatalf("expected obi-ebpf fallback for unknown runtime, got: %+v", unknownAct)
	}

	// Empty registry tests len(candidates) == 0 branch
	emptyPlan := BuildPlan(discovery.Inventory{
		Services: []discovery.DiscoveredService{{Name: "none-svc", Runtime: "none"}},
	}, cfg, &capabilities.Registry{})
	if emptyPlan.Actions[0].SelectedAdapter != "none" || emptyPlan.Actions[0].Status != "unsupported-zero-code" {
		t.Fatalf("expected none adapter for empty registry, got: %+v", emptyPlan.Actions[0])
	}
}

func TestGenerateAssistedSnippetAllRuntimes(t *testing.T) {
	rule := config.MethodRule{
		Class:  "OrderService",
		Method: "Execute",
	}

	snippetJVM := generateAssistedSnippet("jvm", "svc", rule)
	if !strings.Contains(snippetJVM, "@WithSpan") {
		t.Fatalf("expected JVM snippet, got %s", snippetJVM)
	}

	snippetNode := generateAssistedSnippet("nodejs", "svc", rule)
	if !strings.Contains(snippetNode, "trace.getTracer") {
		t.Fatalf("expected Node snippet, got %s", snippetNode)
	}

	snippetPy := generateAssistedSnippet("python", "svc", rule)
	if !strings.Contains(snippetPy, "tracer.start_as_current_span") {
		t.Fatalf("expected Python snippet, got %s", snippetPy)
	}

	snippetOther := generateAssistedSnippet("other", "svc", rule)
	if !strings.Contains(snippetOther, "OpenTelemetry SDK") {
		t.Fatalf("expected fallback snippet, got %s", snippetOther)
	}
}

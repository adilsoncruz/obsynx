package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	// Ensure working directory is repository root so relative paths work cleanly
	dir, err := os.Getwd()
	if err == nil {
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				_ = os.Chdir(dir)
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	os.Exit(m.Run())
}

func TestCLI_UsageAndHelp(t *testing.T) {
	cases := [][]string{
		{},
		{"help"},
		{"-h"},
		{"--help"},
	}

	for _, tc := range cases {
		var stdout, stderr bytes.Buffer
		code := run(tc, &stdout, &stderr)
		if code != 0 {
			t.Errorf("args %v: expected exit code 0, got %d", tc, code)
		}
		if !strings.Contains(stdout.String(), "OBSynx CLI") {
			t.Errorf("args %v: expected usage output, got: %s", tc, stdout.String())
		}
	}
}

func TestCLI_UnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"unknown-subcommand"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("expected unknown command in stderr, got: %s", stderr.String())
	}
}

func TestCLI_AdaptersList(t *testing.T) {
	t.Run("text format", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"adapters", "list"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "ADAPTER ID") || !strings.Contains(out, "opentelemetry-java") {
			t.Errorf("expected table with adapters, got: %s", out)
		}
	})

	t.Run("json format", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--format=json", "adapters", "list"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, `"id": "opentelemetry-java"`) {
			t.Errorf("expected json with opentelemetry-java, got: %s", out)
		}
	})
}

func TestCLI_AdaptersExplain(t *testing.T) {
	t.Run("known runtime text", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"adapters", "explain", "jvm"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "Adapter: opentelemetry-java") {
			t.Errorf("expected jvm adapter details, got: %s", out)
		}
	})

	t.Run("known runtime json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--format", "json", "adapters", "explain", "jvm"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, `"id": "opentelemetry-java"`) {
			t.Errorf("expected json format, got: %s", out)
		}
	})

	t.Run("fallback eBPF for unknown runtime", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"adapters", "explain", "cobol"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0 (fallback eBPF matches), got %d", code)
		}
		if !strings.Contains(stdout.String(), "obi-ebpf") {
			t.Errorf("expected obi-ebpf fallback, got: %s", stdout.String())
		}
	})

	t.Run("missing runtime arg", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"adapters", "explain"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "usage:") {
			t.Errorf("expected usage in stderr, got: %s", stderr.String())
		}
	})

	t.Run("invalid subcommand", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"adapters", "foo"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1, got %d", code)
		}
	})

	t.Run("no subcommand", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"adapters"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1, got %d", code)
		}
	})
}

func TestCLI_Doctor(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"doctor"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Doctor Diagnostics") {
			t.Errorf("expected doctor diagnostics, got: %s", stdout.String())
		}
	})

	t.Run("json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--format=json", "doctor"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), `"checks":`) {
			t.Errorf("expected json checks, got: %s", stdout.String())
		}
	})
}

func TestCLI_Init(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "obsynx.yaml")

	t.Run("text format", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--config", cfgPath, "init"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Initialized OBSynx configuration") {
			t.Errorf("expected init success message, got: %s", stdout.String())
		}
		if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
			t.Fatalf("config file was not created: %v", err)
		}
	})

	t.Run("json format", func(t *testing.T) {
		cfgJSONPath := filepath.Join(tmpDir, "obsynx-json.yaml")
		var stdout, stderr bytes.Buffer
		code := run([]string{"--config", cfgJSONPath, "--format=json", "init"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), `"status": "initialized"`) {
			t.Errorf("expected json initialized, got: %s", stdout.String())
		}
	})

	t.Run("invalid path fails", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--config", "/nonexistent-folder-abc-xyz/obsynx.yaml", "init"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1 for invalid path, got %d", code)
		}
	})
}

func TestCLI_ConfigValidate(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "obsynx.yaml")

	var stdout, stderr bytes.Buffer
	code := run([]string{"--config", cfgPath, "init"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("failed to init config for test: %v", stderr.String())
	}

	t.Run("valid text", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		c := run([]string{"--config", cfgPath, "config", "validate"}, &out, &errBuf)
		if c != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", c, errBuf.String())
		}
		if !strings.Contains(out.String(), "is valid") {
			t.Errorf("expected valid message, got: %s", out.String())
		}
	})

	t.Run("valid json", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		c := run([]string{"--config", cfgPath, "--format=json", "config", "validate"}, &out, &errBuf)
		if c != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", c, errBuf.String())
		}
		if !strings.Contains(out.String(), `"valid": true`) {
			t.Errorf("expected json valid: true, got: %s", out.String())
		}
	})

	t.Run("invalid config path", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		c := run([]string{"--config", filepath.Join(tmpDir, "nonexistent.yaml"), "config", "validate"}, &out, &errBuf)
		if c != 1 {
			t.Fatalf("expected code 1, got %d", c)
		}
	})

	t.Run("missing validate subcommand", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		c := run([]string{"config"}, &out, &errBuf)
		if c != 1 {
			t.Fatalf("expected code 1, got %d", c)
		}
	})

	t.Run("project target resolution yaml path", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		c := run([]string{"config", "validate", "examples/kotlin-spring-api/obsynx.yaml"}, &out, &errBuf)
		if c != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", c, errBuf.String())
		}
		if !strings.Contains(out.String(), "[OK] Service") {
			t.Errorf("expected valid service output, got: %s", out.String())
		}
	})

	t.Run("project target resolution dir", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		c := run([]string{"config", "validate", "examples/kotlin-spring-api"}, &out, &errBuf)
		if c != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", c, errBuf.String())
		}
	})

	t.Run("project target resolution examples candidate json", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		c := run([]string{"--format=json", "config", "validate", "kotlin-spring-api"}, &out, &errBuf)
		if c != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", c, errBuf.String())
		}
		if !strings.Contains(out.String(), `"valid": true`) {
			t.Errorf("expected valid: true in json, got: %s", out.String())
		}
	})

	t.Run("project target nonexistent", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		c := run([]string{"config", "validate", "nonexistent-project-xyz"}, &out, &errBuf)
		if c != 1 {
			t.Fatalf("expected code 1, got %d", c)
		}
	})
}

func TestCLI_Discover(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"discover"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Discovery Scan") {
			t.Errorf("expected discovery scan header, got: %s", stdout.String())
		}
	})

	t.Run("json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--format=json", "discover"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), `"services":`) {
			t.Errorf("expected json services field, got: %s", stdout.String())
		}
	})
}

func TestCLI_Plan(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"plan"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "Instrumentation Plan") {
			t.Errorf("expected plan output, got: %s", out)
		}
		if !strings.Contains(out, "Estimated Telemetry Volume Reduction") {
			t.Errorf("expected cost-aware efficiency metrics, got: %s", out)
		}
	})

	t.Run("json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--format=json", "plan"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), `"collector_endpoint":`) {
			t.Errorf("expected collector_endpoint in json, got: %s", stdout.String())
		}
	})
}

func TestCLI_Instrument(t *testing.T) {
	t.Run("list text", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"instrument", "list"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "SERVICE") {
			t.Errorf("expected table header, got: %s", stdout.String())
		}
	})

	t.Run("list json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--format=json", "instrument", "list"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "[") {
			t.Errorf("expected json array, got: %s", stdout.String())
		}
	})

	t.Run("explain service text", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"instrument", "explain", "kotlin-spring-api"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "Service: kotlin-spring-api") {
			t.Errorf("expected service explanation, got: %s", out)
		}
	})

	t.Run("explain service json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--format=json", "instrument", "explain", "kotlin-spring-api"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), `"service_name": "kotlin-spring-api"`) {
			t.Errorf("expected json service action, got: %s", stdout.String())
		}
	})

	t.Run("missing subcommand", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"instrument"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1, got %d", code)
		}
	})

	t.Run("unknown subcommand", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"instrument", "foo"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1, got %d", code)
		}
	})

	t.Run("explain missing service", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"instrument", "explain"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1, got %d", code)
		}
	})

	t.Run("explain nonexistent service", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"instrument", "explain", "nonexistent-svc-xyz"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "not found") {
			t.Errorf("expected service not found error, got: %s", stderr.String())
		}
	})
}

func TestCLI_Dashboard(t *testing.T) {
	t.Run("missing args", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"dashboard"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1, got %d", code)
		}
	})

	t.Run("unknown subcommand", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"dashboard", "foo"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1, got %d", code)
		}
	})

	t.Run("generate without metrics", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "obsynx.yaml")
		_ = run([]string{"--config", cfgPath, "init"}, &bytes.Buffer{}, &bytes.Buffer{})

		var stdout, stderr bytes.Buffer
		code := run([]string{"--config", cfgPath, "dashboard", "generate"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "No services declared") {
			t.Errorf("expected no services declared message, got: %s", stdout.String())
		}
	})

	t.Run("generate with services having custom metrics", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "obsynx.yaml")
		outDir := filepath.Join(tmpDir, "dashboards")

		cfgContent := `version: "0.2"
collector:
  otlp_endpoint: http://otel-collector:4317
policies:
  limits:
    max_attributes_per_span: 32
    max_events_per_span: 64
services:
  - name: order-service
    runtime: jvm
    instrumentation:
      automatic: true
      methods:
        - method: processOrder
          derive_metric:
            name: order_processed_total
            type: counter
            description: Total orders processed
`
		if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
			t.Fatalf("failed to write test config: %v", err)
		}

		// Text format
		var stdout, stderr bytes.Buffer
		code := run([]string{"--config", cfgPath, "dashboard", "generate", "--output-dir=" + outDir}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Generated Dashboards") {
			t.Errorf("expected generated dashboards output, got: %s", stdout.String())
		}

		// Second run without force skips
		var stdout2, stderr2 bytes.Buffer
		code = run([]string{"--config", cfgPath, "dashboard", "generate", "--output-dir=" + outDir}, &stdout2, &stderr2)
		if code != 0 {
			t.Fatalf("expected code 0 on rerun, got %d", code)
		}
		if !strings.Contains(stdout2.String(), "Skipped Dashboards") {
			t.Errorf("expected skipped dashboards on second run, got: %s", stdout2.String())
		}

		// JSON format with force
		var stdout3, stderr3 bytes.Buffer
		code = run([]string{"--config", cfgPath, "--format=json", "dashboard", "generate", "--output-dir=" + outDir, "--force"}, &stdout3, &stderr3)
		if code != 0 {
			t.Fatalf("expected code 0 with json force, got %d", code)
		}
		if !strings.Contains(stdout3.String(), `"generated_files":`) {
			t.Errorf("expected json generated_files, got: %s", stdout3.String())
		}
	})

	t.Run("generate with invalid config path", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--config", "/nonexistent/config.yaml", "dashboard", "generate"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1, got %d", code)
		}
	})
}

func TestCLI_Status(t *testing.T) {
	t.Run("status text", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"status"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "OBSynx Status") {
			t.Errorf("expected status header, got: %s", stdout.String())
		}
	})

	t.Run("status json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--format=json", "status"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), `"status":`) {
			t.Errorf("expected status json field, got: %s", stdout.String())
		}
	})
}

func TestCLI_ApplyAndRollbackInTempDir(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(origDir)
	}()

	// 1. Apply text
	var stdout, stderr bytes.Buffer
	code := run([]string{"apply"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("apply failed with code %d. stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "OBSynx Apply Completed") {
		t.Errorf("expected apply completed, got: %s", stdout.String())
	}

	// 2. Status in temp dir
	var stOut, stErr bytes.Buffer
	code = run([]string{"status"}, &stOut, &stErr)
	if code != 0 {
		t.Fatalf("status failed with code %d", code)
	}

	// 3. Rollback text
	var rbOut, rbErr bytes.Buffer
	code = run([]string{"rollback"}, &rbOut, &rbErr)
	if code != 0 {
		t.Fatalf("rollback failed with code %d. stderr: %s", code, rbErr.String())
	}
	if !strings.Contains(rbOut.String(), "OBSynx Rollback Completed") {
		t.Errorf("expected rollback completed, got: %s", rbOut.String())
	}

	// 4. Apply json
	var appJSONOut, appJSONErr bytes.Buffer
	code = run([]string{"--format=json", "apply"}, &appJSONOut, &appJSONErr)
	if code != 0 {
		t.Fatalf("apply json failed with code %d", code)
	}
	if !strings.Contains(appJSONOut.String(), `"status": "applied"`) {
		t.Errorf("expected applied in json, got: %s", appJSONOut.String())
	}

	// 5. Rollback json
	var rbJSONOut, rbJSONErr bytes.Buffer
	code = run([]string{"--format=json", "rollback"}, &rbJSONOut, &rbJSONErr)
	if code != 0 {
		t.Fatalf("rollback json failed with code %d", code)
	}
	if !strings.Contains(rbJSONOut.String(), `"status": "rolled_back"`) {
		t.Errorf("expected rolled_back in json, got: %s", rbJSONOut.String())
	}
}

func TestCLI_FlagVariations(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--adapters-dir=adapters", "--config=obsynx.yaml", "--adapters-dir", "adapters", "doctor"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
}

func TestCLI_ConfigValidateSingleProjectFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--config", "examples/kotlin-spring-api/obsynx.yaml", "config", "validate"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "is valid") {
		t.Errorf("expected is valid in output, got: %s", stdout.String())
	}

	var jsonOut, jsonErr bytes.Buffer
	code = run([]string{"--format=json", "--config", "examples/kotlin-spring-api/obsynx.yaml", "config", "validate"}, &jsonOut, &jsonErr)
	if code != 0 {
		t.Fatalf("expected code 0 in json mode, got %d", code)
	}
	if !strings.Contains(jsonOut.String(), `"valid": true`) {
		t.Errorf("expected valid: true in json, got: %s", jsonOut.String())
	}
}

func TestCLI_ConfigValidateGlobalFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--config", "obsynx.yaml", "config", "validate"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Global configuration obsynx.yaml is valid") {
		t.Errorf("expected global configuration valid, got: %s", stdout.String())
	}
}

func TestCLI_DashboardInvalidFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"dashboard", "generate", "--nonexistent-flag-xyz"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected code 1, got %d", code)
	}
}

func TestCLI_CorruptAdaptersDirFails(t *testing.T) {
	tmpDir := t.TempDir()
	badYAML := filepath.Join(tmpDir, "capabilities.yaml")
	_ = os.WriteFile(badYAML, []byte("adapter:\n  id: [broken"), 0o644)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--adapters-dir", tmpDir, "doctor"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected code 1 for corrupt adapter, got %d", code)
	}
	if !strings.Contains(stderr.String(), "error loading capability registry") {
		t.Errorf("expected error in stderr, got: %s", stderr.String())
	}
}

func TestCLI_MainSubprocess(t *testing.T) {
	if os.Getenv("BE_TEST_MAIN") == "1" {
		os.Args = []string{"obsynx", "help"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestCLI_MainSubprocess")
	cmd.Env = append(os.Environ(), "BE_TEST_MAIN=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected main to exit 0, got err: %v, out: %s", err, string(out))
	}
}

func TestCLI_PlanWithRecommendations(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "obsynx.yaml")
	cfgContent := `version: "0.2"
collector:
  otlp_endpoint: http://otel-collector:4317
policies:
  sampling:
    ratio: 1.0
  limits:
    max_attributes_per_span: 128
    max_events_per_span: 64
`
	_ = os.WriteFile(cfgPath, []byte(cfgContent), 0o644)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--config", cfgPath, "plan"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "Cost Recommendation") {
		t.Errorf("expected cost recommendations in output, got: %s", stdout.String())
	}
}

func TestCLI_ApplyPrintsManagedServices(t *testing.T) {
	tmpDir := t.TempDir()
	origWd, _ := os.Getwd()
	_ = os.Chdir(tmpDir)
	defer func() {
		_ = os.Chdir(origWd)
	}()

	// Create an example service in tmpDir
	svcDir := filepath.Join(tmpDir, "examples", "order-svc")
	_ = os.MkdirAll(svcDir, 0o755)
	_ = os.WriteFile(filepath.Join(svcDir, "go.mod"), []byte("module order-svc\ngo 1.22\n"), 0o644)

	var stdout, stderr bytes.Buffer
	code := run([]string{"apply"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Managed Services:") || !strings.Contains(stdout.String(), "order-svc") {
		t.Errorf("expected Managed Services output, got: %s", stdout.String())
	}
}

func TestCLI_ErrorBranches(t *testing.T) {
	// 1. plan with nonexistent config falls back to default
	var stdout1, stderr1 bytes.Buffer
	code := run([]string{"--config", "/nonexistent/obsynx.yaml", "plan"}, &stdout1, &stderr1)
	if code != 0 {
		t.Fatalf("expected code 0 with default fallback, got %d", code)
	}

	// 2. instrument list with nonexistent config falls back to default
	var stdout2, stderr2 bytes.Buffer
	code = run([]string{"--config", "/nonexistent/obsynx.yaml", "instrument", "list"}, &stdout2, &stderr2)
	if code != 0 {
		t.Fatalf("expected code 0 with default fallback, got %d", code)
	}

	// 3. status error on corrupted state
	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, ".obsynx", "state.json")
	_ = os.MkdirAll(filepath.Dir(stateFile), 0o755)
	_ = os.WriteFile(stateFile, []byte("bad-json:["), 0o644)
	origWd, _ := os.Getwd()
	_ = os.Chdir(tmpDir)
	defer func() {
		_ = os.Chdir(origWd)
	}()

	var stOut, stErr bytes.Buffer
	code = run([]string{"status"}, &stOut, &stErr)
	if code != 1 {
		t.Fatalf("expected status code 1 on corrupted state file, got %d", code)
	}

	var rbOut, rbErr bytes.Buffer
	code = run([]string{"rollback"}, &rbOut, &rbErr)
	if code != 1 {
		t.Fatalf("expected rollback code 1 on corrupted state file, got %d", code)
	}

	// 4. discover with existing instrumentation
	composeDir := filepath.Join(tmpDir, "deploy", "lab")
	_ = os.MkdirAll(composeDir, 0o755)
	composeFile := filepath.Join(composeDir, "docker-compose.yml")
	composeContent := `services:
  my-app:
    image: my-company/app:latest
    environment:
      - CORECLR_ENABLE_PROFILING=1
`
	_ = os.WriteFile(composeFile, []byte(composeContent), 0o644)
	var discOut, discErr bytes.Buffer
	code = run([]string{"discover"}, &discOut, &discErr)
	if code != 0 {
		t.Fatalf("expected discover code 0, got %d", code)
	}
	if !strings.Contains(discOut.String(), "dotnet-profiler:enabled") {
		t.Errorf("expected existing instrumentation in discover output, got: %s", discOut.String())
	}
}

package dashboard_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adilsoncruz/obsynx/internal/config"
	"github.com/adilsoncruz/obsynx/internal/dashboard"
)

func TestGenerateFromServices(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "obsynx-dash-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	services := []config.ServiceConfig{
		{
			Name:    "payments-api",
			Runtime: "nodejs",
			Instrumentation: config.InstrumentationConfig{
				Automatic: true,
				Methods: []config.MethodRule{
					{
						Method: "processPayment",
						Metrics: []config.MethodMetricSpec{
							{
								Name:        "payments.processed.total",
								Type:        "counter",
								Description: "Total de pagamentos",
							},
							{
								Name:        "payments.processing.duration",
								Type:        "histogram",
								Unit:        "ms",
								Description: "Duracao do pagamento",
							},
						},
					},
					{
						Method: "calculateRisk",
						Metrics: []config.MethodMetricSpec{
							{
								Name:        "customer.risk.score",
								Type:        "gauge",
								Description: "Score de risco",
							},
							{
								Name: "in_flight",
								Type: "updowncounter",
							},
							{
								Name: "custom_fallback",
								Type: "other_unknown",
							},
						},
					},
				},
			},
		},
	}

	opts := dashboard.Options{
		OutputDir: tmpDir,
		Force:     false,
	}

	// First run: should generate file
	res, err := dashboard.GenerateFromServices(services, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.GeneratedFiles) != 1 {
		t.Fatalf("expected 1 generated file, got %d", len(res.GeneratedFiles))
	}

	expectedFile := filepath.Join(tmpDir, "obsynx-payments-api.json")
	if _, err := os.Stat(expectedFile); err != nil {
		t.Fatalf("expected file %s to exist: %v", expectedFile, err)
	}

	// Second run without force: should skip existing file
	res2, err := dashboard.GenerateFromServices(services, opts)
	if err != nil {
		t.Fatalf("unexpected error on second run: %v", err)
	}
	if len(res2.SkippedFiles) != 1 {
		t.Fatalf("expected 1 skipped file, got %d", len(res2.SkippedFiles))
	}

	// Third run with force: should regenerate
	opts.Force = true
	res3, err := dashboard.GenerateFromServices(services, opts)
	if err != nil {
		t.Fatalf("unexpected error on third run: %v", err)
	}
	if len(res3.GeneratedFiles) != 1 {
		t.Fatalf("expected 1 regenerated file with force, got %d", len(res3.GeneratedFiles))
	}

	// Run with empty OutputDir (falls back to DefaultOutputDir in tmp)
	tmpSub := filepath.Join(tmpDir, "default-sub")
	emptyOpts := dashboard.Options{
		OutputDir: tmpSub,
		Force:     true,
	}
	if _, err := dashboard.GenerateFromServices(services, emptyOpts); err != nil {
		t.Fatalf("GenerateFromServices with custom sub failed: %v", err)
	}
}

func TestGenerateFromServicesErrors(t *testing.T) {
	tmpDir := t.TempDir()
	origWd, _ := os.Getwd()
	_ = os.Chdir(tmpDir)
	defer func() {
		_ = os.Chdir(origWd)
	}()

	services := []config.ServiceConfig{{Name: "test-svc"}}
	res, err := dashboard.GenerateFromServices(services, dashboard.Options{OutputDir: ""})
	if err != nil {
		t.Fatalf("expected default output dir to succeed, got %v", err)
	}
	if len(res.GeneratedFiles) != 1 {
		t.Fatalf("expected 1 file generated in default output dir")
	}

	badDir := filepath.Join(tmpDir, "blocked-file")
	_ = os.WriteFile(badDir, []byte("content"), 0644)
	if _, err := dashboard.GenerateFromServices(services, dashboard.Options{OutputDir: badDir}); err == nil {
		t.Fatal("expected error when output dir is a regular file")
	}
}

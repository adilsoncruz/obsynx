package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDefaultConfigIsValid(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected DefaultConfig to be valid, got: %v", err)
	}
}

func TestInvalidSamplingRatioFailsValidation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Policies.Sampling.Ratio = 1.5
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected sampling ratio > 1.0 to fail validation")
	}
}

func TestCaptureArgumentsWithoutAllowlistBlocked(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Services = []ServiceConfig{
		{
			Name:    "test-svc",
			Runtime: "jvm",
			Instrumentation: InstrumentationConfig{
				Automatic: true,
				Methods: []MethodRule{
					{
						Method:           "processPayment",
						CaptureArguments: true,
					},
				},
			},
		},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "blocked by default") {
		t.Fatalf("expected capture_arguments without allowlist to fail validation, got: %v", err)
	}
}

func TestSensitiveAttributeInAllowlistBlocked(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Services = []ServiceConfig{
		{
			Name:    "test-svc",
			Runtime: "jvm",
			Instrumentation: InstrumentationConfig{
				Automatic: true,
				Methods: []MethodRule{
					{
						Method:            "processPayment",
						CaptureArguments:  true,
						AllowedAttributes: []string{"customer.credit_card"},
					},
				},
			},
		},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "sensitive/PII token") {
		t.Fatalf("expected sensitive attribute token to be blocked, got: %v", err)
	}
}

func TestSensitiveValueFromExpressionBlocked(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Services = []ServiceConfig{
		{
			Name:    "test-svc",
			Runtime: "nodejs",
			Instrumentation: InstrumentationConfig{
				Automatic: true,
				Methods: []MethodRule{
					{
						Method: "processPayment",
						Metrics: []MethodMetricSpec{
							{
								Name:      "payments.leak",
								Type:      "counter",
								ValueFrom: "arg.password",
							},
						},
					},
				},
			},
		},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "sensitive/PII token") {
		t.Fatalf("expected sensitive value_from expression to be blocked, got: %v", err)
	}
}

func TestSourceValidatorDetectsMissingFunctionAndInvalidArgsAndAmbiguity(t *testing.T) {
	tmpDir := t.TempDir()
	code := `package main

type PaymentReq struct {
	Amount float64 ` + "`json:\"amount\"`" + `
}
type PaymentResp struct {
	Status string ` + "`json:\"status\"`" + `
}

type OrderService struct{}
type BillingService struct{}

func (o *OrderService) Execute(req PaymentReq) PaymentResp { return PaymentResp{} }
func (b *BillingService) Execute(req PaymentReq) PaymentResp { return PaymentResp{} }
func UniquePay(req PaymentReq) PaymentResp { return PaymentResp{} }
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(code), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	// 1. Non-existent function must fail
	err := ValidateProjectSource(tmpDir, ServiceConfig{
		Name: "go-test",
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{Method: "NonExistentFunc"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not found in project source code") {
		t.Fatalf("expected missing function error, got: %v", err)
	}

	// 2. Two functions with the same name in different classes/receivers without 'class' must fail with ambiguity error
	err = ValidateProjectSource(tmpDir, ServiceConfig{
		Name: "go-test",
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{Method: "Execute"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "ambiguous method") {
		t.Fatalf("expected ambiguous method error when two classes define Execute, got: %v", err)
	}

	// 3. Disambiguating with 'class: OrderService' must succeed
	err = ValidateProjectSource(tmpDir, ServiceConfig{
		Name: "go-test",
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{
				Class:  "OrderService",
				Method: "Execute",
				Metrics: []MethodMetricSpec{{
					Name:           "orders.amount",
					Type:           "counter",
					ValueFrom:      "arg.amount",
					AttributesFrom: map[string]string{"payment_status": "return.status"},
				}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("expected disambiguated OrderService.Execute with valid arg.amount & return.status to pass, got: %v", err)
	}

	// 4. Non-existent argument field (arg.non_existent) must fail
	err = ValidateProjectSource(tmpDir, ServiceConfig{
		Name: "go-test",
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{
				Method: "UniquePay",
				Metrics: []MethodMetricSpec{{
					Name:      "orders.bad_arg",
					Type:      "counter",
					ValueFrom: "arg.non_existent_field",
				}},
			}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not found in parameters") {
		t.Fatalf("expected non-existent argument field to fail, got: %v", err)
	}

	// 5. Non-existent return field (return.non_existent) must fail
	err = ValidateProjectSource(tmpDir, ServiceConfig{
		Name: "go-test",
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{
				Method: "UniquePay",
				Metrics: []MethodMetricSpec{{
					Name:           "orders.bad_ret",
					Type:           "counter",
					ValueFrom:      "1",
					AttributesFrom: map[string]string{"payment_status": "return.invalid_prop"},
				}},
			}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not found on return type") {
		t.Fatalf("expected non-existent return field to fail, got: %v", err)
	}
}

func TestSourceValidatorCSharpSupport(t *testing.T) {
	tmpDir := t.TempDir()
	csCode := `
namespace Payments;

public record PaymentRequest(decimal Amount, string Method);
public record PaymentResponse(string Status, decimal Amount, string Method, string TransactionId);

public class PaymentService
{
    public PaymentResponse ProcessPayment(decimal amount, string method)
    {
        return new PaymentResponse("APPROVED", amount, method, "TXN-123");
    }
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "Program.cs"), []byte(csCode), 0o644); err != nil {
		t.Fatalf("write Program.cs: %v", err)
	}

	// 1. Valid C# service method, arg, and return field
	svc := ServiceConfig{
		Name:    "dotnet-api",
		Runtime: "dotnet",
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{
				Class:  "PaymentService",
				Method: "ProcessPayment",
				Metrics: []MethodMetricSpec{{
					Name:           "payments.amount",
					Type:           "counter",
					ValueFrom:      "arg.amount",
					AttributesFrom: map[string]string{"status": "return.Status"},
				}},
			}},
		},
	}
	if err := ValidateProjectSource(tmpDir, svc); err != nil {
		t.Fatalf("expected valid C# validation to pass, got: %v", err)
	}

	// 2. Non-existent C# method must fail
	badMethodSvc := svc
	badMethodSvc.Instrumentation.Methods = []MethodRule{{
		Class:  "PaymentService",
		Method: "UnknownMethod",
	}}
	if err := ValidateProjectSource(tmpDir, badMethodSvc); err == nil {
		t.Fatal("expected non-existent C# method to fail")
	}

	// 3. Invalid argument field in C# must fail
	badArgSvc := svc
	badArgSvc.Instrumentation.Methods = []MethodRule{{
		Class:  "PaymentService",
		Method: "ProcessPayment",
		Metrics: []MethodMetricSpec{{
			Name:      "payments.bad",
			Type:      "counter",
			ValueFrom: "arg.unknown_arg",
		}},
	}}
	if err := ValidateProjectSource(tmpDir, badArgSvc); err == nil {
		t.Fatal("expected invalid C# argument to fail")
	}

	// 4. Invalid return field in C# must fail
	badRetSvc := svc
	badRetSvc.Instrumentation.Methods = []MethodRule{{
		Class:  "PaymentService",
		Method: "ProcessPayment",
		Metrics: []MethodMetricSpec{{
			Name:           "payments.bad",
			Type:           "counter",
			ValueFrom:      "1",
			AttributesFrom: map[string]string{"status": "return.NonExistentField"},
		}},
	}}
	if err := ValidateProjectSource(tmpDir, badRetSvc); err == nil {
		t.Fatal("expected invalid C# return field to fail")
	}
}

func TestConfigLoadSaveAndMerge(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "obsynx.yaml")

	// 1. Save and Load
	cfg := DefaultConfig()
	if err := Save(cfgPath, cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	loaded, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Version != cfg.Version {
		t.Fatalf("expected version %s, got %s", cfg.Version, loaded.Version)
	}

	// 2. Load non-existent file
	if _, err := Load(filepath.Join(tmpDir, "missing.yaml")); err == nil {
		t.Fatal("expected error loading missing file")
	}

	// 3. Load corrupt YAML
	corruptPath := filepath.Join(tmpDir, "corrupt.yaml")
	_ = os.WriteFile(corruptPath, []byte("invalid: yaml: ["), 0o644)
	if _, err := Load(corruptPath); err == nil {
		t.Fatal("expected error loading corrupt file")
	}

	// 4. Save to invalid path
	if err := Save("/non/existent/path/obsynx.yaml", cfg); err == nil {
		t.Fatal("expected error saving to invalid path")
	}

	// 5. MergeProjectConfigs with examples directory
	examplesDir := filepath.Join(tmpDir, "examples", "sub-svc")
	_ = os.MkdirAll(examplesDir, 0o755)
	projYAML := `
service:
  name: sub-svc
  runtime: nodejs
instrumentation:
  methods:
    - method: doWork
`
	projFile := filepath.Join(examplesDir, "obsynx.yaml")
	_ = os.WriteFile(projFile, []byte(projYAML), 0o644)
	jsFile := filepath.Join(examplesDir, "index.js")
	_ = os.WriteFile(jsFile, []byte("function doWork() { return { status: 'OK' }; }"), 0o644)

	var targetCfg Config
	if err := MergeProjectConfigs(tmpDir, &targetCfg); err != nil {
		t.Fatalf("MergeProjectConfigs failed: %v", err)
	}
	if len(targetCfg.Services) != 1 || targetCfg.Services[0].Name != "sub-svc" {
		t.Fatalf("expected sub-svc merged into targetCfg, got %+v", targetCfg.Services)
	}

	// 6. LoadProjectConfig edge cases
	if _, err := LoadProjectConfig(filepath.Join(tmpDir, "missing-proj.yaml")); err == nil {
		t.Fatal("expected missing project config to fail")
	}
	badProjFile := filepath.Join(tmpDir, "bad-proj.yaml")
	_ = os.WriteFile(badProjFile, []byte("service: {}"), 0o644)
	if _, err := LoadProjectConfig(badProjFile); err == nil {
		t.Fatal("expected project config without service.name to fail")
	}
}

func TestValidationRules(t *testing.T) {
	// Missing collector endpoint
	cfg := DefaultConfig()
	cfg.Collector.OTLPEndpoint = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected empty collector endpoint to fail")
	}

	// Negative limits
	cfg = DefaultConfig()
	cfg.Policies.Limits.MaxAttributesPerSpan = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected 0 MaxAttributesPerSpan to fail")
	}

	// TLS enabled without CA cert
	cfg = DefaultConfig()
	cfg.Collector.TLSEnabled = true
	cfg.Collector.CACertPath = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected TLS enabled without CA cert to fail")
	}

	// Auth header with space or colon
	cfg = DefaultConfig()
	cfg.Collector.AuthHeaderEnv = "Bearer secret_token"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected raw auth token in AuthHeaderEnv to fail")
	}

	// Duplicate service names
	cfg = DefaultConfig()
	cfg.Services = []ServiceConfig{
		{Name: "duplicate-svc"},
		{Name: "duplicate-svc"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected duplicate service name to fail")
	}

	// Empty method name
	cfg = DefaultConfig()
	cfg.Services = []ServiceConfig{
		{
			Name: "test-svc",
			Instrumentation: InstrumentationConfig{
				Methods: []MethodRule{{Method: ""}},
			},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected empty method name to fail")
	}

	// Attribute from sensitive
	cfg = DefaultConfig()
	cfg.Services = []ServiceConfig{
		{
			Name: "test-svc",
			Instrumentation: InstrumentationConfig{
				Methods: []MethodRule{{
					Method: "doIt",
					Metrics: []MethodMetricSpec{{
						Name:           "leak",
						Type:           "counter",
						AttributesFrom: map[string]string{"pwd": "arg.password"},
					}},
				}},
			},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected sensitive attribute_from to fail")
	}

	// Unsupported metric type
	cfg = DefaultConfig()
	cfg.Services = []ServiceConfig{
		{
			Name: "test-svc",
			Instrumentation: InstrumentationConfig{
				Methods: []MethodRule{{
					Method: "doIt",
					Metrics: []MethodMetricSpec{{
						Name: "bad_type",
						Type: "unsupported_metric_type",
					}},
				}},
			},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsupported metric type to fail")
	}

	// Invalid value_from expression
	cfg = DefaultConfig()
	cfg.Services = []ServiceConfig{
		{
			Name: "test-svc",
			Instrumentation: InstrumentationConfig{
				Methods: []MethodRule{{
					Method: "doIt",
					Metrics: []MethodMetricSpec{{
						Name:      "bad_expr",
						Type:      "counter",
						ValueFrom: "invalid_syntax_not_arg_or_return",
					}},
				}},
			},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid value_from expression to fail")
	}

	// CaptureReturn without allowed_attributes
	cfg = DefaultConfig()
	cfg.Services = []ServiceConfig{
		{
			Name: "test-svc",
			Instrumentation: InstrumentationConfig{
				Methods: []MethodRule{{
					Method:        "doIt",
					CaptureReturn: true,
				}},
			},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected CaptureReturn without allowed_attributes to fail")
	}

	// Exceeding cardinality limit in attributes_from
	tooManyAttrs := make(map[string]string)
	for i := 0; i < 20; i++ {
		tooManyAttrs[fmt.Sprintf("attr_%d", i)] = "1"
	}
	cfg = DefaultConfig()
	cfg.Services = []ServiceConfig{
		{
			Name: "test-svc",
			Instrumentation: InstrumentationConfig{
				Methods: []MethodRule{{
					Method: "doIt",
					Metrics: []MethodMetricSpec{{
						Name:           "overflow",
						Type:           "counter",
						AttributesFrom: tooManyAttrs,
					}},
				}},
			},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected attributes_from exceeding limit to fail")
	}

	// Exceeding cardinality limit in allowed_attributes
	var tooManyAllowed []string
	for i := 0; i < 20; i++ {
		tooManyAllowed = append(tooManyAllowed, fmt.Sprintf("attr_%d", i))
	}
	cfg = DefaultConfig()
	cfg.Services = []ServiceConfig{
		{
			Name: "test-svc",
			Instrumentation: InstrumentationConfig{
				Methods: []MethodRule{{
					Method:            "doIt",
					CaptureArguments:  true,
					AllowedAttributes: tooManyAllowed,
				}},
			},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected allowed_attributes exceeding limit to fail")
	}
}

func TestSourceValidatorMultiLanguage(t *testing.T) {
	tmpDir := t.TempDir()

	// Kotlin
	ktCode := `
package com.test
data class Order(val id: String, val amount: Double)
class OrderService {
    fun process(order: Order): String = "OK"
}
`
	_ = os.WriteFile(filepath.Join(tmpDir, "Order.kt"), []byte(ktCode), 0o644)

	// JS
	jsCode := `
function executePayment(amount, method) {
    return {
        status: "APPROVED",
        id: "123"
    };
}
`
	_ = os.WriteFile(filepath.Join(tmpDir, "payment.js"), []byte(jsCode), 0o644)

	// Python
	pyCode := `
class UserService:
    def get_user(self, user_id):
        return {"user_id": user_id, "active": True}
`
	_ = os.WriteFile(filepath.Join(tmpDir, "user.py"), []byte(pyCode), 0o644)

	// Validate Kotlin
	ktSvc := ServiceConfig{
		Name: "kt-svc",
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{
				Class:  "OrderService",
				Method: "process",
				Metrics: []MethodMetricSpec{{
					Name:      "orders.amount",
					ValueFrom: "arg.amount",
				}},
			}},
		},
	}
	if err := ValidateProjectSource(tmpDir, ktSvc); err != nil {
		t.Fatalf("expected Kotlin validation to pass: %v", err)
	}

	// Validate JS
	jsSvc := ServiceConfig{
		Name: "js-svc",
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{
				Method: "executePayment",
				Metrics: []MethodMetricSpec{{
					Name:           "payments.amount",
					ValueFrom:      "arg.amount",
					AttributesFrom: map[string]string{"status": "return.status"},
				}},
			}},
		},
	}
	if err := ValidateProjectSource(tmpDir, jsSvc); err != nil {
		t.Fatalf("expected JS validation to pass: %v", err)
	}

	// Validate Python
	pySvc := ServiceConfig{
		Name: "py-svc",
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{
				Class:  "UserService",
				Method: "get_user",
				Metrics: []MethodMetricSpec{{
					Name:           "users.active",
					ValueFrom:      "arg.user_id",
					AttributesFrom: map[string]string{"active": "return.active"},
				}},
			}},
		},
	}
	if err := ValidateProjectSource(tmpDir, pySvc); err != nil {
		t.Fatalf("expected Python validation to pass: %v", err)
	}

	// Argument index bounds check (e.g. arg.0 vs arg.99)
	numArgSvc := ServiceConfig{
		Name: "num-arg-svc",
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{
				Method: "executePayment",
				Metrics: []MethodMetricSpec{{
					Name:      "arg0",
					ValueFrom: "arg.0",
				}},
			}},
		},
	}
	if err := ValidateProjectSource(tmpDir, numArgSvc); err != nil {
		t.Fatalf("expected arg.0 to pass: %v", err)
	}

	badNumArgSvc := ServiceConfig{
		Name: "num-arg-svc",
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{
				Method: "executePayment",
				Metrics: []MethodMetricSpec{{
					Name:      "arg99",
					ValueFrom: "arg.99",
				}},
			}},
		},
	}
	if err := ValidateProjectSource(tmpDir, badNumArgSvc); err == nil {
		t.Fatal("expected arg.99 out of bounds to fail")
	}

	// Non-existent directory returns nil
	if err := ValidateProjectSource("/non/existent/dir", ktSvc); err != nil {
		t.Fatalf("expected non-existent dir to return nil: %v", err)
	}
}

func TestMethodMetricSpecUnmarshalYAML(t *testing.T) {
	// 1. Shorthand string "counter"
	yamlScalar := `
name: payments.total
type: counter
`
	var spec1 MethodMetricSpec
	if err := yaml.Unmarshal([]byte(yamlScalar), &spec1); err != nil {
		t.Fatalf("unmarshal scalar failed: %v", err)
	}
	if spec1.Type != "counter" {
		t.Fatalf("expected type counter, got %s", spec1.Type)
	}

	// 2. Full struct
	yamlFull := `
name: payments.total
type: COUNTER
unit: BRL
value_from: arg.amount
`
	var spec2 MethodMetricSpec
	if err := yaml.Unmarshal([]byte(yamlFull), &spec2); err != nil {
		t.Fatalf("unmarshal full failed: %v", err)
	}
	if spec2.Name != "payments.total" || spec2.Type != "counter" {
		t.Fatalf("expected normalized counter, got %+v", spec2)
	}

	// 3. Invalid YAML
	if err := yaml.Unmarshal([]byte("[invalid"), &spec1); err == nil {
		t.Fatal("expected invalid YAML to fail unmarshal")
	}
}

func TestGoSourceIndexingWithPointersAndTags(t *testing.T) {
	tmpDir := t.TempDir()

	// Subdirs to be skipped
	_ = os.MkdirAll(filepath.Join(tmpDir, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(tmpDir, "bin", "ignored.go"), []byte("package ignored\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(tmpDir, "obj"), 0o755)
	_ = os.WriteFile(filepath.Join(tmpDir, "obj", "ignored.go"), []byte("package ignored\n"), 0o644)

	code := `package testpkg

type PtrReq struct {
	Amount float64 ` + "`json:\"amount,omitempty\"`" + `
	Extra  string  ` + "`xml:\"extra\"`" + `
}
type PtrResp struct {
	Status string ` + "`json:\"status\"`" + `
}

func ProcessPtr(req *PtrReq) *PtrResp {
	return &PtrResp{Status: "OK"}
}
`
	_ = os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(code), 0o644)

	svc := ServiceConfig{
		Name: "ptr-svc",
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{
				Method: "ProcessPtr",
				Metrics: []MethodMetricSpec{{
					Name:           "ptr.amount",
					Type:           "counter",
					ValueFrom:      "arg.amount",
					AttributesFrom: map[string]string{"status": "return.status"},
				}},
			}},
		},
	}

	if err := ValidateProjectSource(tmpDir, svc); err != nil {
		t.Fatalf("expected pointer indexing to succeed, got: %v", err)
	}
}

func TestMethodMetricSpecUnmarshalYAMLExtended(t *testing.T) {
	yamlData := `
method: doWork
metrics:
  - counter
`
	var rule MethodRule
	if err := yaml.Unmarshal([]byte(yamlData), &rule); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(rule.Metrics) != 1 || rule.Metrics[0].Type != "counter" {
		t.Fatalf("expected metric type 'counter', got %+v", rule.Metrics)
	}

	invalidData := `
method: doWork
metrics:
  - [nested, list]
`
	if err := yaml.Unmarshal([]byte(invalidData), &rule); err == nil {
		t.Fatal("expected error on invalid metric YAML")
	}
}

func TestConfigLoadAndSaveErrors(t *testing.T) {
	tmpDir := t.TempDir()

	// Missing config
	if _, err := Load(filepath.Join(tmpDir, "missing.yaml")); err == nil {
		t.Fatal("expected Load to fail on missing config")
	}

	// Corrupt YAML
	corruptFile := filepath.Join(tmpDir, "corrupt.yaml")
	_ = os.WriteFile(corruptFile, []byte("invalid:[yaml"), 0o644)
	if _, err := Load(corruptFile); err == nil {
		t.Fatal("expected Load to fail on corrupt YAML")
	}

	// Corrupt project config in examples
	exDir := filepath.Join(tmpDir, "examples", "bad-svc")
	_ = os.MkdirAll(exDir, 0o755)
	_ = os.WriteFile(filepath.Join(exDir, "obsynx.yaml"), []byte("invalid:[yaml"), 0o644)
	validRoot := filepath.Join(tmpDir, "obsynx.yaml")
	_ = Save(validRoot, DefaultConfig())
	if _, err := Load(validRoot); err == nil {
		t.Fatal("expected Load to fail when project config is corrupted")
	}

	// Save with invalid config
	invalidCfg := DefaultConfig()
	invalidCfg.Policies.Sampling.Ratio = 5.0
	if err := Save(filepath.Join(tmpDir, "invalid.yaml"), invalidCfg); err == nil {
		t.Fatal("expected Save to fail when config is invalid")
	}

	// Save to unwritable path
	blockedPath := filepath.Join(tmpDir, "blocked", "sub", "config.yaml")
	if err := Save(blockedPath, DefaultConfig()); err == nil {
		t.Fatal("expected Save to fail when directory does not exist")
	}
}

func TestLoadProjectConfigErrors(t *testing.T) {
	tmpDir := t.TempDir()

	// Missing file
	if _, err := LoadProjectConfig(filepath.Join(tmpDir, "missing.yaml")); err == nil {
		t.Fatal("expected LoadProjectConfig to fail on missing file")
	}

	// Corrupt YAML
	badFile := filepath.Join(tmpDir, "bad.yaml")
	_ = os.WriteFile(badFile, []byte("bad:[yaml"), 0o644)
	if _, err := LoadProjectConfig(badFile); err == nil {
		t.Fatal("expected LoadProjectConfig to fail on bad YAML")
	}

	// Missing service.name
	noNameFile := filepath.Join(tmpDir, "noname.yaml")
	_ = os.WriteFile(noNameFile, []byte("version: '0.2'\n"), 0o644)
	if _, err := LoadProjectConfig(noNameFile); err == nil {
		t.Fatal("expected LoadProjectConfig to fail when service.name is empty")
	}
}

func TestValidateProjectSourceErrors(t *testing.T) {
	// 1. Method does not exist in indexed source
	tmpDir1 := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmpDir1, "dummy.go"), []byte("package dummy\nfunc Hello() {}\n"), 0o644)
	svc := ServiceConfig{
		Name: "missing-method-svc",
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{Method: "DoesNotExist"}},
		},
	}
	if err := ValidateProjectSource(tmpDir1, svc); err == nil {
		t.Fatal("expected error when method does not exist in indexed source")
	}

	// 2. Ambiguous method in multiple classes
	tmpDir := t.TempDir()
	code := `package testpkg

type ServiceA struct{}
func (s *ServiceA) Process() {}

type ServiceB struct{}
func (s *ServiceB) Process() {}
`
	_ = os.WriteFile(filepath.Join(tmpDir, "services.go"), []byte(code), 0o644)
	ambiguousSvc := ServiceConfig{
		Name:       "ambiguous-svc",
		SourceFile: filepath.Join(tmpDir, "obsynx.yaml"),
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{
				Method: "Process", // no class specified!
			}},
		},
	}
	if err := ValidateProjectSource(tmpDir, ambiguousSvc); err == nil {
		t.Fatal("expected ambiguous method error when multiple classes define it")
	}
}

func TestConfigValidationAndIndexingEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. LoadProjectConfig when validateServiceMethods fails (empty method name)
	badMethodFile := filepath.Join(tmpDir, "bad-method.yaml")
	badMethodYAML := `version: '0.2'
service:
  name: bad-method-svc
instrumentation:
  methods:
    - method: ""
`
	_ = os.WriteFile(badMethodFile, []byte(badMethodYAML), 0o644)
	if _, err := LoadProjectConfig(badMethodFile); err == nil {
		t.Fatal("expected LoadProjectConfig to fail on empty method name")
	}

	// 2. LoadProjectConfig with excessive method rules (>25)
	var methodsYAML strings.Builder
	methodsYAML.WriteString("version: '0.2'\nservice:\n  name: excessive-method-svc\ninstrumentation:\n  methods:\n")
	for i := 0; i < 26; i++ {
		methodsYAML.WriteString(fmt.Sprintf("    - method: method%d\n", i))
	}
	excessiveFile := filepath.Join(tmpDir, "excessive-methods.yaml")
	_ = os.WriteFile(excessiveFile, []byte(methodsYAML.String()), 0o644)
	if _, err := LoadProjectConfig(excessiveFile); err == nil {
		t.Fatal("expected LoadProjectConfig to fail on excessive methods (>25)")
	}

	// 3. validateMethodSecurity with unsupported metric type
	invalidMetricTypeFile := filepath.Join(tmpDir, "invalid-metric-type.yaml")
	invalidMetricTypeYAML := `version: '0.2'
service:
  name: invalid-metric-svc
instrumentation:
  methods:
    - method: doWork
      metrics:
        - name: bad_metric
          type: unsupported_unknown_type
`
	_ = os.WriteFile(invalidMetricTypeFile, []byte(invalidMetricTypeYAML), 0o644)
	if _, err := LoadProjectConfig(invalidMetricTypeFile); err == nil {
		t.Fatal("expected error on unsupported metric type")
	}

	// 4. validateValueSource with bare 'arg'
	bareArgFile := filepath.Join(tmpDir, "bare-arg.yaml")
	bareArgYAML := `version: '0.2'
service:
  name: bare-arg-svc
instrumentation:
  methods:
    - method: doWork
      metrics:
        - name: bad_arg_metric
          type: counter
          value_from: arg
`
	_ = os.WriteFile(bareArgFile, []byte(bareArgYAML), 0o644)
	if _, err := LoadProjectConfig(bareArgFile); err == nil {
		t.Fatal("expected error on bare 'arg' value_from")
	}

	// 5. checkSourceExpr with unknown property
	goCode := `package testpkg
type Req struct {
	Count int ` + "`json:\"count\"`" + `
}
func Handler(r *Req) {}
`
	_ = os.WriteFile(filepath.Join(tmpDir, "handler.go"), []byte(goCode), 0o644)
	unknownPropSvc := ServiceConfig{
		Name: "unknown-prop-svc",
		SourceFile: filepath.Join(tmpDir, "obsynx.yaml"),
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{
				Method: "Handler",
				Metrics: []MethodMetricSpec{{
					Name: "req.unknown",
					Type: "counter",
					ValueFrom: "arg.unknown_field",
				}},
			}},
		},
	}
	if err := ValidateProjectSource(tmpDir, unknownPropSvc); err == nil {
		t.Fatal("expected error when expression references unknown property")
	}

	// 6. MergeProjectConfigs when cfg.Services already has items
	cfg := DefaultConfig()
	cfg.Services = []ServiceConfig{{Name: "pre-existing-svc"}}
	_ = MergeProjectConfigs(tmpDir, &cfg)
	var foundPreExisting bool
	for _, s := range cfg.Services {
		if s.Name == "pre-existing-svc" {
			foundPreExisting = true
		}
	}
	if !foundPreExisting {
		t.Fatal("expected pre-existing service preserved in MergeProjectConfigs")
	}

	// 7. Validate with invalid service method
	invalidSvcCfg := DefaultConfig()
	invalidSvcCfg.Services = []ServiceConfig{{
		Name: "invalid-svc",
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{Method: ""}},
		},
	}}
	if err := invalidSvcCfg.Validate(); err == nil {
		t.Fatal("expected cfg.Validate to fail with invalid service method")
	}
}

func TestSourceValidatorASTEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Go file with const/var, non-struct type alias, unnamed parameters, map parameter, and a corrupt Go file
	goCode := `package testpkg

const MyConst = "foo"
var MyVar = 123
type MyInt int
type Worker interface{ Work() }

func UnnamedParams(int, map[string]string) {}
`
	_ = os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(goCode), 0o644)
	_ = os.WriteFile(filepath.Join(tmpDir, "corrupt.go"), []byte("package bad!!!;;;"), 0o644)

	// 2. Python file with dynamic return and unknown property
	pyCode := `def calculate():
    return {"result": 42}
`
	_ = os.WriteFile(filepath.Join(tmpDir, "calc.py"), []byte(pyCode), 0o644)

	svc := ServiceConfig{
		Name: "ast-svc",
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{
				Method: "calculate",
				Metrics: []MethodMetricSpec{{
					Name: "calc.unknown",
					Type: "counter",
					AttributesFrom: map[string]string{"unknown": "return.unknown_attr"},
				}},
			}},
		},
	}
	if err := ValidateProjectSource(tmpDir, svc); err == nil {
		t.Fatal("expected error on unknown return attribute")
	}

	// 3. hasFieldCaseInsensitive
	fields := map[string]bool{"order_id": true}
	if !hasFieldCaseInsensitive(fields, "orderId") {
		t.Fatal("expected orderId to match order_id case-insensitively")
	}

	// 4. Load config that fails Validate
	invalidRatioFile := filepath.Join(tmpDir, "invalid-ratio.yaml")
	_ = os.WriteFile(invalidRatioFile, []byte("version: '0.2'\npolicies:\n  sampling:\n    ratio: 5.0\n"), 0o644)
	if _, err := Load(invalidRatioFile); err == nil {
		t.Fatal("expected Load to fail when validation fails")
	}
}

func TestMoreConfigAndValidatorEdges(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. ValidateProjectSource when len(symbols) == 0 (dir has no source code)
	emptySrcDir := filepath.Join(tmpDir, "empty-src")
	_ = os.MkdirAll(emptySrcDir, 0o755)
	_ = os.WriteFile(filepath.Join(emptySrcDir, "README.md"), []byte("# Notes\n"), 0o644)
	svc := ServiceConfig{Name: "empty-sym-svc"}
	if err := ValidateProjectSource(emptySrcDir, svc); err != nil {
		t.Fatalf("expected nil when len(symbols)==0, got %v", err)
	}

	// 2. Package-level function in multiple files without receiver (m.Class == "")
	f1 := filepath.Join(tmpDir, "f1.go")
	f2 := filepath.Join(tmpDir, "f2.go")
	_ = os.WriteFile(f1, []byte("package testpkg\nfunc RunJob() {}\n"), 0o644)
	_ = os.WriteFile(f2, []byte("package testpkg\nfunc RunJob() {}\n"), 0o644)
	multiFileSvc := ServiceConfig{
		Name: "multi-file-svc",
		Instrumentation: InstrumentationConfig{
			Methods: []MethodRule{{Method: "RunJob"}},
		},
	}
	if err := ValidateProjectSource(tmpDir, multiFileSvc); err == nil {
		t.Fatal("expected ambiguous error for multi-file package-level function")
	}

	// 3. LoadProjectConfig fails ValidateProjectSource
	projYAML := `version: '0.2'
service:
  name: proj-with-bad-source
instrumentation:
  methods:
    - method: RunJob
`
	projFile := filepath.Join(tmpDir, "obsynx.yaml")
	_ = os.WriteFile(projFile, []byte(projYAML), 0o644)
	if _, err := LoadProjectConfig(projFile); err == nil {
		t.Fatal("expected LoadProjectConfig to fail when ValidateProjectSource fails")
	}
}

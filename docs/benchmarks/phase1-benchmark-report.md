# Phase 1 — Benchmark and OpenTelemetry Validation Report

**Date:** 2026-11-18  
**Environment:** Local Docker Compose (`deploy/lab/docker-compose.yml`)

## 1. Comparison Scenarios

| Runtime | Scenario | Setup Time | HTTP Coverage | DB Coverage | Business Method Coverage (`calculateCustomerRisk`) | Avg CPU | Memory RSS | Latency p50 / p95 / p99 |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **Kotlin/Spring** | 1. No instrumentation | Baseline | ❌ | ❌ | ❌ | *To measure* | *To measure* | *To measure* |
| **Kotlin/Spring** | 2. OTel Java Agent (Auto) | ~2 min (env vars) | ✅ | ✅ (JDBC H2) | ❌ | *To measure* | *To measure* | *To measure* |
| **Kotlin/Spring** | 3. OTel Java Agent + `METHODS_INCLUDE` | ~3 min | ✅ | ✅ (JDBC H2) | ✅ (`PaymentService`, `CustomerRiskService`) | *To measure* | *To measure* | *To measure* |
| **Go API** | 1. No instrumentation | Baseline | ❌ | ❌ | ❌ | *To measure* | *To measure* | *To measure* |
| **Go API** | 2. OBI / eBPF (`--profile ebpf`) | ~3 min (privileged + pid) | ✅ | N/A | ❌ (eBPF limitation) | *To measure* | *To measure* | *To measure* |

## 2. Cost-Aware Policies Validated in Collector

Configured in [`deploy/collector/otel-collector-config.yaml`](../../deploy/collector/otel-collector-config.yaml):
1. **Drop operational noise (`filter/drop_health`):** Spans matching `/health` are dropped prior to exporting to Tempo.
2. **Redact sensitive attributes (`attributes/redact_sensitive`):** Automatic removal of `http.request.header.authorization`, `enduser.id`, and `user.email`.

## 3. Gaps Identified for OBSynx Core (Phase 2)

1. **Discovery and recommendation:** Operators must know if Go binaries have DWARF symbols or if JVM instances already have `-javaagent`.
2. **Declarative translation:** For JVM, `OTEL_INSTRUMENTATION_METHODS_INCLUDE` expects `package.Class[method1,method2]`; OBSynx automatically compiles declarative high-level YAML into this variable.
3. **Explaining boundaries (Graceful Degradation):** When users request custom method tracing (`calculateCustomerRisk`) in Go with eBPF only, OBSynx alerts that adapter `obi-ebpf` has `custom_method_tracing: false` and recommends SDK or build-time instrumentation.

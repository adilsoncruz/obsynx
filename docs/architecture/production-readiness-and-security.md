# OBSynx — Production Readiness, Multi-Runtime Support & Security Guide

**Version:** 0.2 (Phase 6)  
**Date:** 2026-11-18

---

## 1. Consolidated Multi-Runtime Matrix (Phase 6)

| Runtime | Adapter ID | Zero-Code Strategy | Injection Managed by OBSynx | Custom Business Methods |
| :--- | :--- | :--- | :--- | :--- |
| **JVM (Java/Kotlin)** | `opentelemetry-java` | `java-agent` | `JAVA_TOOL_OPTIONS=-javaagent:...` | ✅ `OTEL_INSTRUMENTATION_METHODS_INCLUDE` + `spanmetrics` |
| **Go (Linux ELF)** | `obi-ebpf` | `ebpf` | Sidecar `grafana/beyla` (`pid: service:<name>`) | ⚠️ Graceful Degradation -> Assisted Go SDK snippet / proxy |
| **Node.js** | `opentelemetry-nodejs` | `runtime-preload` | `NODE_OPTIONS=--require @opentelemetry/auto-instrumentations-node/register` | ⚠️ Assisted Node.js `@opentelemetry/api` snippet / proxy |
| **Python** | `opentelemetry-python` | `runtime-preload` | `opentelemetry-instrument` + `OTEL_PYTHON_LOGGING_AUTO_INSTRUMENTATION_ENABLED=true` | ⚠️ Assisted Python `trace.get_tracer()` snippet / hook |
| **.NET** | `opentelemetry-dotnet` | `runtime-preload` | `CORECLR_ENABLE_PROFILING=1` | ⚠️ `OTEL_DOTNET_AUTO_TRACES_ADDITIONAL_SOURCES` |

---

## 2. Security, TLS, Authentication, and Privacy

1. **TLS and Authentication without Plaintext Secrets:**
   - In `obsynx.yaml`, the `collector` block supports `tls_enabled: true`, `ca_cert_path`, and `auth_header_env: OTEL_AUTH_HEADER`.
   - The OBSynx validator strictly blocks raw tokens in `obsynx.yaml`; only environment variable references are accepted and injected via `${env:OTEL_AUTH_HEADER}` in the Collector.
2. **Default Argument and PII Redaction Guard:**
   - Parameter and return value extraction requires explicit `allowed_attributes` declaration (maximum 5 attributes per method) and strictly forbids sensitive tokens (`password`, `token`, `secret`, `authorization`, `credit_card`, `cpf`, `ssn`, `email`).
3. **SHA-256 Integrity Verification & Safe Rollback:**
   - `obsynx apply` computes and stores the SHA-256 checksum of every generated artifact in `.obsynx/state.json`.
   - `obsynx status` and `obsynx doctor` verify configuration drift if files are edited out-of-band.
   - `obsynx rollback` cleanly removes only managed artifacts and records the event in the audit trail (`audit_trail`).

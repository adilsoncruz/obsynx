# ADR 004: GitOps for Observability & Decoupled Telemetry Governance

- **Status:** Accepted
- **Date:** 2026-11-23

---

## 1. Context
In the baseline design (Phases 1 to 6), OBSYNX achieved **Zero-Code Instrumentation**, eliminating telemetry SDK dependencies from application source code (`pom.xml`, `package.json`, `go.mod`).

However, when the declarative file `obsynx.yaml` is packaged inside the microservice repository or container image (`/app/obsynx.yaml`), an operational coupling persists:
* To add, alter, or remove a business metric (e.g., a new attribute or histogram boundary tuning), the engineering team must open a PR in the application repo, run the application CI pipeline, build a new Docker image tag, and execute a production release.
* This operational friction discourages telemetry experimentation and increases the MTTR (Mean Time to Remediation) for observability definitions.

---

## 2. Decision

1. **Decoupled Application Lifecycle:**  
   Adopt the **GitOps Observability** model, where declarative telemetry rules and business metrics are maintained in a dedicated central governance repository (`obsynx-telemetry-governance`), completely separated from application source code.
2. **External Configuration Injection:**  
   In Kubernetes, `obsynx.yaml` is injected via a `ConfigMap` mounted at `/etc/obsynx/obsynx.yaml` through the standard environment variable `OBSYNX_CONFIG_PATH`.
3. **Governance via CODEOWNERS:**  
   Each service squad maintains exclusive ownership over their service directory (`services/<service-name>/obsynx.yaml`), while the Platform team governs global compliance (`global/obsynx.yaml`).
4. **Shift-Left Validation in Telemetry CI:**  
   The GitOps repository runs `obsynx config validate` on every Pull Request, proactively barring PII violations, excess attributes, and cardinality spikes prior to merging.
5. **Gradual Evolution to OpAMP (Open Agent Management Protocol):**  
   Establish the CNCF OpAMP standard as the target protocol for remote distribution and hot-reloading of in-memory interceptors without pod restarts.

---

## 3. Consequences and Trade-offs

### Positive Consequences:
* **On-Demand Metrics in Minutes:** Add or recalibrate metrics without touching application container builds or release pipelines.
* **Centralized Audit Trail:** Complete Git history tracking who changed telemetry policies, sampling ratios, and business metrics.
* **Uniform Security & Compliance:** Centralized PII filtering and sampling enforcement evaluated automatically across the entire organization.

### Accepted Trade-offs:
* **Version Drift:** Risk that a developer declares a rule for a method existing only on development branches before it reaches production. Mitigated via boot-time symbol verification (AST/DWARF/Reflection) and graceful agent fallbacks.
* **Additional Repository Overhead:** Introduces a centralized governance repository requiring dedicated CI/CD pipelines and permission management.

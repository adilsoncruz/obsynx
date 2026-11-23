# GitOps for Observability: Enterprise Architecture & Governance Guide

**Version:** 1.0  
**Status:** Approved (Ref: ADR-004)  
**Target Runtimes:** Kubernetes, AWS EKS, AWS Lambda (Serverless)

---

## 1. Executive Summary

Traditional application observability tightly couples monitoring logic to software release cycles. Adding, tuning, or deprecating business metrics frequently requires developers to modify source code (`pom.xml`, `package.json`, `go.mod`), open application pull requests, trigger CI/CD builds, and redeploy container images.

**OBSYNX GitOps Observability** decouples telemetry governance from application lifecycles. By combining **Zero-Code in-memory instrumentation** (Java ByteBuddy, Node.js V8 hooks, Python `sitecustomize`, eBPF) with **declarative GitOps pipelines**, telemetry rules are managed as centralized configuration assets versioned in Git and injected dynamically into production workloads.

```text
┌─────────────────────────────────┐       ┌─────────────────────────────────┐
│     Application Source Repo     │       │   Telemetry Governance Repo     │
│   (Pristine code, zero SDKs)    │       │    (obsynx.yaml per service)    │
└────────────────┬────────────────┘       └────────────────┬────────────────┘
                 │ Release (v2.1.0)                        │ PR + PII Scan
                 ▼                                         ▼
   ┌───────────────────────────┐             ┌───────────────────────────┐
   │    Immutable App Image    │             │  K8s ConfigMap / AppConfig│
   └─────────────┬─────────────┘             └─────────────┬─────────────┘
                 │                                         │
                 └───────────────────┬─────────────────────┘
                                     ▼
                      ┌─────────────────────────────┐
                      │    Production Pod / Task    │
                      │ • OBSYNX_CONFIG_PATH        │
                      │ • Zero-Code In-Memory Hook  │
                      └──────────────┬──────────────┘
                                     │ OTLP Export
                                     ▼
                         [ OTel Collector Cluster ]
```

---

## 2. Trade-Off Analysis: Local Config vs. GitOps vs. Dynamic OpAMP

Choosing the right configuration delivery model involves operational and regulatory trade-offs:

| Evaluation Dimension | 1. Local Config (`/app/obsynx.yaml`) | 2. GitOps (External ConfigMap/Layer) | 3. Dynamic OpAMP (Hot-Reload) |
| :--- | :--- | :--- | :--- |
| **YAML Location** | Inside application repository | Centralized governance repository | OpAMP Control Plane / GitOps bridge |
| **Application Release Required?** | **Yes** (commit + build + deploy) | **No** (configuration sync only) | **No** (instant dynamic update) |
| **Pod / Task Restart Required?** | **Yes** | **Yes** (rollout restart or mount reload) | **No** (hot in-memory retransformation) |
| **Time to Activation** | 10 to 30 minutes | 1 to 2 minutes | **Sub-second (~2 seconds)** |
| **Infrastructure Overhead** | **None** (fully self-contained) | **Minimal** (native K8s ConfigMap) | **High** (requires stateful OpAMP server) |
| **Auditability & Compliance** | Distributed across app repos | **Centralized Git history (`git blame`)** | Variable (requires custom audit log) |
| **Shift-Left PII Prevention** | App CI dependent | **Enforced centrally on PR** | Requires runtime inspector checks |
| **Enterprise Recommendation** | Local dev & Docker Compose | **Corporate Production Standard** | Edge debugging / diagnostic sessions |

### Why Enterprises Prefer GitOps:
* **Regulatory Compliance (SOX, PCI-DSS, LGPD/GDPR):** Prohibits direct runtime mutations in production. Every rule change must undergo four-eyes review via Pull Requests.
* **Shift-Left PII Gateways:** CI linters prevent capturing sensitive data (`password`, `ssn`, `credit_card`) before configs reach production.
* **Segregation of Duties (SoD):** SRE and Compliance engineers modify telemetry rules without requiring push rights to core service repositories.

---

## 3. Configuration Resolution & Precedence Hierarchy

Workloads often contain a default `obsynx.yaml` bundled in the Docker image for local testing, alongside an externally injected configuration from GitOps. OBSYNX resolves configurations using a strict precedence order:

```text
                           ┌──────────────────────────────┐
                           │ OBSYNX Agent Startup Boot   │
                           └──────────────┬───────────────┘
                                          │
                         Is OBSYNX_CONFIG_PATH defined?
                                ├─── YES ───► Read external file (GitOps / ConfigMap)
                                │             [Overrides internal image config]
                                └─── NO  ───► Fallback to /app/obsynx.yaml
                                              [Local container baseline]
```

1. **Priority 1 (`OBSYNX_CONFIG_PATH` Environment Variable):**  
   If set (e.g., `/etc/obsynx/obsynx.yaml` or `/opt/obsynx-agents/obsynx.yaml`), the agent reads this file exclusively. Any packaged file inside `/app/obsynx.yaml` is ignored.
2. **Priority 2 (Default Local Path `/app/obsynx.yaml`):**  
   If the environment variable is absent, the agent defaults to `/app/obsynx.yaml` for local development and non-orchestrated environments.

---

## 4. Multi-Platform Deployment Patterns

### Pattern A: Standard Kubernetes
Uses an Init Container to stage agents and a mounted `ConfigMap` managed by ArgoCD or Flux:

```yaml
spec:
  volumes:
    - name: obsynx-agents
      emptyDir: {}
    - name: obsynx-rules
      configMap:
        name: payment-service-obsynx-rules
  initContainers:
    - name: obsynx-agent-injector
      image: ghcr.io/obsynx/obsynx-injector:v0.1.0
      command: ["cp", "-r", "/agents/.", "/obsynx-agents/"]
      volumeMounts:
        - name: obsynx-agents
          mountPath: /obsynx-agents
  containers:
    - name: service
      image: myregistry/payment-service:v1.0.0
      env:
        - name: OBSYNX_CONFIG_PATH
          value: "/etc/obsynx/obsynx.yaml"
        - name: JAVA_TOOL_OPTIONS
          value: "-javaagent:/obsynx-agents/jvm/obsynx-jvm-agent.jar"
        - name: OTEL_EXPORTER_OTLP_ENDPOINT
          value: "http://otel-collector.observability.svc:4317"
      volumeMounts:
        - name: obsynx-agents
          mountPath: /obsynx-agents
          readOnly: true
        - name: obsynx-rules
          mountPath: /etc/obsynx
          readOnly: true
```

### Pattern B: AWS EKS
In AWS EKS, deployment follows the same Kubernetes declarative model with the option of pulling injector images from private AWS ECR and exporting telemetry to an OpenTelemetry Collector DaemonSet or AWS Distro for OpenTelemetry (ADOT).

### Pattern C: Serverless (AWS Lambda)
Serverless functions do not support persistent volume mounts. Telemetry rules and agents are provided via **AWS Lambda Layers** and configured through environment variables:

```yaml
PaymentFunction:
  Type: AWS::Serverless::Function
  Properties:
    Handler: index.handler
    Runtime: nodejs20.x
    Layers:
      - !Ref ObsynxTelemetryLayer
    Environment:
      Variables:
        NODE_OPTIONS: "--require /opt/obsynx-agents/nodejs/obsynx-hook.js"
        OBSYNX_CONFIG_PATH: "/opt/obsynx-agents/obsynx.yaml"
        OBSYNX_FLUSH_ON_FREEZE: "true"
        OTEL_EXPORTER_OTLP_ENDPOINT: "http://otel-collector.internal:4318"
```
* The `OBSYNX_FLUSH_ON_FREEZE=true` flag signals the bundled Lambda Extension to flush metrics before execution freeze.
* Updates to `obsynx.yaml` can be deployed via Lambda Layer version bumps, AWS AppConfig, or S3 fetch without modifying application code.

---

## 5. Shift-Left CI Verification Pipeline

The centralized GitOps repository must enforce automated validation on every pull request:

```text
Pull Request Opened
       │
       ├── Step 1: obsynx config validate <service/obsynx.yaml>
       │           (Validates schema, method qualifiers, syntax)
       │
       ├── Step 2: Automated PII Keyword Scanner
       │           (Blocks extraction of passwords, tokens, card data, SSNs)
       │
       └── Step 3: CODEOWNERS Peer Review Approval
                   (Ensures domain squad and SRE review before merge)
```

Refer to [examples/gitops-telemetry-governance/](file:///Users/adilsoncruz/orca/workspaces/obsynx/gitops-observability-docs/examples/gitops-telemetry-governance/) for an enterprise reference implementation.

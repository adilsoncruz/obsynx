# OBSYNX GitOps Telemetry Governance Reference Repository

This repository demonstrates how to manage observability, business metrics extraction, and OpenTelemetry instrumentation rules using **GitOps and Zero-Code injection**.

---

## 🎯 Architecture at a Glance

* **Application Repositories:** Contain pure business logic (`pom.xml`, `package.json`, `go.mod` without OTel SDK dependencies).
* **This Governance Repository:** Houses all `obsynx.yaml` telemetry extraction declarations, managed via Pull Requests, peer reviews, and automated CI validation.
* **Delivery Engine:**
  * **Kubernetes / AWS EKS:** Sourced via ArgoCD or Flux and mounted as a native `ConfigMap` into Pods.
  * **Serverless (AWS Lambda):** Delivered via Lambda Layers or AWS AppConfig with `OBSYNX_FLUSH_ON_FREEZE=true`.

---

## 📂 Directory Structure

```text
.
├── .github/
│   ├── CODEOWNERS                     # Domain squads own their respective telemetry folder
│   └── workflows/
│       └── validate-telemetry.yaml    # CI shift-left: schema check + PII scanning
└── services/
    ├── payment-service/               # Kubernetes / AWS EKS example (JVM)
    │   ├── obsynx.yaml
    │   └── k8s-configmap.yaml
    └── order-service-serverless/      # AWS Lambda example (Node.js)
        ├── obsynx.yaml
        └── lambda-sam.yaml
```

---

## 🔒 Automated Shift-Left CI Verification

Every pull request triggers `.github/workflows/validate-telemetry.yaml` to ensure:
1. **Schema Integrity:** Verifies that function names, arguments, return fields, and metric types are valid.
2. **PII Prevention:** Scans declared metric extraction rules for sensitive keywords (`password`, `token`, `creditcard`, `cpf`, `ssn`) and aborts the pull request if detected.
3. **Four-Eyes Governance:** `CODEOWNERS` ensures both the squad owning the service and the central observability platform team approve rule additions.

---

## ⚙️ Configuration Precedence

1. **`OBSYNX_CONFIG_PATH` (GitOps / ConfigMap):** Highest priority. If defined in the container environment, this file is loaded, completely overriding any local file.
2. **`/app/obsynx.yaml` (Local Fallback):** Read only if `OBSYNX_CONFIG_PATH` is absent (ideal for local Docker development).

---

## 🚀 Validating Rules Locally

You can validate these rules locally using the OBSYNX CLI:

```bash
# Validate payment-service rules:
obsynx config validate services/payment-service/obsynx.yaml

# Validate order-service serverless rules:
obsynx config validate services/order-service-serverless/obsynx.yaml
```

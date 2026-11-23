# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.1.x   | :white_check_mark: |


## Reporting a Vulnerability

Security and privacy are foundational pillars of OBSYNX. Because OBSYNX operates in the data path and controls telemetry policies, preventing credential leaks, PII exposure, and privilege escalation is paramount.

If you discover a security vulnerability or potential PII leak mechanism in OBSYNX:

1. **Do not open a public issue.**
2. Report the vulnerability privately via GitHub Security Advisories at https://github.com/adilsoncruz/obsynx/security/advisories/new or contact the maintainer directly.
3. Include detailed reproduction steps, sample `obsynx.yaml` if applicable, and expected vs observed behavior.

## Security Guarantees & Non-Goals

* **Sensitive Data Redaction**: OBSYNX proactively blocks sensitive tokens (`password`, `token`, `secret`, `cpf`, `ssn`, `api_key`, `email`) in method argument capture by default.
* **Overhead Protection**: Custom method hooks are bounded to protect CPU and memory stability.
* **Secrets in Logs**: OBSYNX CLI and diagnostics never print raw environment secret values.

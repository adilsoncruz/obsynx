# ADR 002: Native OpenTelemetry and Control Plane / Data Plane Separation

- **Status:** Accepted
- **Date:** 2026-11-18

## Context
Building proprietary protocols, agents, or collectors would duplicate the OpenTelemetry ecosystem, increase maintenance overhead, and introduce vendor lock-in risks.

## Decision
1. **Reuse before build:** The Data Plane exclusively uses official OpenTelemetry components (OTel Collector, OTLP, OTel Java Agent, OBI/eBPF, OTel SDKs, and Semantic Conventions).
2. **Separated Control Plane:** OBSynx operates strictly within the Control Plane (discovery, planning, configuration generation, cost/redaction policies, and diagnostics), outside the critical path of telemetry packets.

## Consequences
- Zero overhead from OBSynx Core on live OTLP traffic latency.
- Immediate out-of-the-box compatibility with any OTLP-compliant backend (Grafana Tempo, Prometheus, Loki, Datadog, New Relic).

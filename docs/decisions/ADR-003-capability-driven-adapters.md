# ADR 003: Capability-Driven Architecture (Capability-Driven Adapters)

- **Status:** Accepted
- **Date:** 2026-11-18

## Context
Runtimes and instrumentation strategies possess asymmetric capabilities (e.g., the Java Agent intercepts business methods at bytecode level via `OTEL_INSTRUMENTATION_METHODS_INCLUDE`, whereas OBI/eBPF observes kernel/network protocols without arbitrary domain function visibility unless explicit symbols/uprobes are available).

## Decision
Each adapter must declare an explicit capability contract (`schemas/adapter-capability.schema.yaml`) with values `true`, `false`, `conditional`, or `restricted`, alongside `requirements` and `constraints`.

## Consequences
- The Orchestrator never promises "universal zero-code" where there is no technical foundation.
- Enables *graceful degradation*: when a capability is unsupported by the zero-code adapter, OBSynx informs the user of the constraint and suggests the supported alternative (e.g., SDK snippet or compile-time injection).

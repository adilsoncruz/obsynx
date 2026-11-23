# ADR 001: OBSynx Core and CLI in Go with Runtime-Agnostic Architecture

- **Status:** Accepted
- **Date:** 2026-11-18

## Context
OBSynx functions as a Control Plane for discovery, strategy selection, and instrumentation orchestration across heterogeneous Linux and container environments (JVM, Go, Node.js, Python, .NET).

## Decision
Implement the **OBSynx Core** and **CLI (`obsynx`)** in **Go**, maintaining strict separation between the core engine and runtime-specific adapters.

## Consequences
- Single static binary distribution with zero runtime dependencies on the host or container.
- Efficient inspection of Linux processes (`/proc`), containers, and seamless integration with Cloud Native ecosystem tooling.
- The Core contains no hardcoded language-specific rules; every decision queries metadata from the `Capability Registry`.

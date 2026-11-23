---
name: New Runtime Adapter Proposal
about: Propose support for a new language, framework, or runtime
title: "[ADAPTER] "
labels: ["adapter", "proposal"]
assignees: ""
---

**Target Runtime & Versions**
- Language / Platform:
- Supported Versions (e.g., Ruby 3.2+, Rust 1.75+):
- Primary Frameworks:

**Zero-Code Interception Mechanism**
Describe how instrumentation will be attached without altering user code:
- [ ] Dynamic Bytecode / Agent preloading
- [ ] Runtime startup hook / environment variable
- [ ] eBPF Uprobes / Kernel-level tracing
- [ ] Other (describe):

**OpenTelemetry Upstream Support**
Links to official OpenTelemetry SDK/agent for this runtime:

**Capability Matrix Feasibility**
- Automatic HTTP: [Supported / Conditional / Unsupported]
- Automatic Database: [Supported / Conditional / Unsupported]
- Custom Method Hooks: [Supported / Conditional / Unsupported]

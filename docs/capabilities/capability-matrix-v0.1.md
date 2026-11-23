# OBSynx — Capability Matrix & Supported Versions

**Status:** Production & Multi-Runtime Baseline (Phases 1 to 6)  
**Updated:** 2026-11-23  

---

## 1. Supported Runtime Versions Matrix

| Language / Runtime | Supported Versions | Recommended Version | Interception Mechanism | Technical Constraints |
| :--- | :--- | :--- | :--- | :--- |
| **☕ JVM (Java / Kotlin)** | **Java 8, 11, 17, 21, 22+** | **Java 17 & 21 (LTS)** | Byte Buddy (`JAVA_TOOL_OPTIONS`) | Transparent Virtual Threads support on Java 21+. Zero GC impact. |
| **⚡ Node.js (TS / JS)** | **v18.x, v20.x, v22.x+** | **v20.x & v22.x (LTS)** | V8 Proxy Trap (`NODE_OPTIONS`) | Requires ES6 Proxy support. Compatible with CommonJS and ESM. |
| **🐍 Python** | **3.9, 3.10, 3.11, 3.12, 3.13** | **3.11 & 3.12** | `sitecustomize.py` (`PYTHONPATH`) | Native CPython bootstrap hook. Synchronous and `asyncio` support. |
| **🔷 .NET (C# / F#)** | **.NET 6, 7, 8, 9+** | **.NET 8 (LTS)** | CoreCLR Profiler + StartupHook | Zero `.csproj` dependencies. Injected via `CORECLR_PROFILER` & `DOTNET_STARTUP_HOOKS`. |
| **🐹 Go** | **Go 1.20, 1.21, 1.22, 1.23+** | **Go 1.21+** | eBPF Uprobes or Local Proxy | Static Go ELF binary cannot be stripped of symbols (`-ldflags="-s -w"`). In Fargate/Lambda, falls back to Local Proxy. |

> 📖 **Technical Architecture Guide:** For a deep dive into runtime memory hooks, argument extraction (`arg.*`), return value capture (`return.*`), and latency measurement for each runtime, see the [Runtime Function Interception Technical Guide](../architecture/runtime-function-interception.md).

---

## 2. Comparison of Evaluated Laboratory Strategies

| Capability | JVM (`opentelemetry-java` Agent) | Go / Linux (`obi-ebpf`) | Go (`compile-time` / `SDK`) |
| :--- | :--- | :--- | :--- |
| **Code Modification** | None (`JAVA_TOOL_OPTIONS`) | None (eBPF uprobe/kprobe) | Yes (SDK or Build tool) |
| **HTTP / REST Spans** | ✅ Complete | ✅ Complete | ✅ Complete |
| **Database / JDBC / SQL** | ✅ Complete (H2/Postgres/etc.) | ⚠️ Conditional (network SQL protocols) | ✅ Complete |
| **Business Methods (`processPayment`)** | ✅ Supported via `OTEL_INSTRUMENTATION_METHODS_INCLUDE` | ❌ Not generically supported by network eBPF | ✅ Supported via SDK/Build |
| **Method Arguments / Return Capture** | 🔒 Restricted (requires extension) | ❌ Not supported | ⚠️ Manual via SDK (PII risk) |
| **Required Permissions** | Application user | `CAP_BPF` / `CAP_PERFMON` / `pid: host/service` | Application user |
| **Restart Required** | Yes (for `-javaagent` at boot) | No (attaches to running process) | Yes (recompile + deploy) |

> 💡 **Technical Clarification on Go Business Methods:**  
> Generic network eBPF tracers (such as the baseline `obi-ebpf` adapter) are limited to kernel-level HTTP/SQL socket traffic. To intercept custom business functions (`processPayment`) and extract business metrics from arguments and return values in Go, OBSynx provides three mechanisms documented in the [Runtime Function Interception Guide](../architecture/runtime-function-interception.md):
> 1. Surgical ELF symbol table probes via **`go-observer`** (uprobe/uretprobe targeting the internal Go 1.17+ ABI);
> 2. Declarative build-time AST injection (**`obsynx build`**);
> 3. Transparent Local Proxy (**`:18080`**) for unprivileged serverless containers (Fargate/Cloud Run).

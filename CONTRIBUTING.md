# Contributing to OBSYNX

Thank you for your interest in contributing to **OBSYNX**! We welcome community contributions to adapters, core policy engines, documentation, and tooling.

---

## Code of Conduct

All participants in the OBSYNX project must adhere to the [Code of Conduct](CODE_OF_CONDUCT.md).

---

## Development Prerequisites

* **Go**: 1.23 or newer
* **Docker / Docker Compose**: for local integration and lab testing
* **Git**: for version control and worktree isolation

---

## Local Development Workflow

### 1. Clone & Set Up

```bash
git clone https://github.com/adilsoncruz/obsynx.git
cd obsynx
go mod download
```

### 2. Run Tests & Coverage Gate

OBSYNX enforces a strict **>=95.0% statement coverage** gate in CI. All unit tests must pass without regressions:

```bash
# Run all tests with coverage
go test -v -coverprofile=coverage.out ./...

# Verify overall coverage percentage
go tool cover -func=coverage.out | grep total:
```

### 3. Build the CLI

```bash
go build -o bin/obsynx ./cmd/obsynx
./bin/obsynx doctor
```

---

## Proposing New Runtime Adapters

OBSYNX is designed to be runtime-agnostic. New adapters (e.g. Rust, PHP, Ruby) must follow the **Adapter Contract** (Section 6 of the architecture guide):

1. **Declarative Capabilities**: Declare runtime support in `adapters/<adapter-id>/capabilities.yaml` adhering to `schemas/capabilities.schema.json`.
2. **Zero-Code Hook**: The adapter must be injectable purely via runtime flags, startup hooks, or bytecode preloading without application source code modification.
3. **Reference Example**: Provide a runnable microservice example under `examples/<runtime>-api/`.
4. **AST Validator Support**: If the language supports custom method tracing, add an AST indexer in `internal/config/source_validator.go`.

---

## Submitting Pull Requests

1. **Branch Naming**:
   * `feat/<feature-name>` for new features or adapters
   * `fix/<bug-name>` for bug fixes
   * `docs/<topic>` for documentation improvements
2. **Commit Convention**: Follow [Conventional Commits](https://www.conventionalcommits.org/):
   * `feat(...)`: new functionality or adapter
   * `fix(...)`: bug fix
   * `test(...)`: test additions or coverage improvements
   * `docs(...)`: documentation changes
   * `refactor(...)`: non-behavioral code restructuring
3. **Pull Request Checklist**:
   * Complete the [PULL_REQUEST_TEMPLATE](.github/PULL_REQUEST_TEMPLATE.md).
   * Ensure tests pass and total project coverage remains `>=95%`.
   * Keep PRs atomic, focused, and well-described.

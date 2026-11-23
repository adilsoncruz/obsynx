#!/usr/bin/env sh
set -eu

# OBSynx Agent Injector Entrypoint
TARGET_DIR="${TARGET_DIR:-/obsynx-agents}"
APP_DIR="${APP_DIR:-/app}"
RUNTIME="${OBSYNX_RUNTIME:-}"

mkdir -p "$TARGET_DIR"

# Auto-detection if OBSYNX_RUNTIME is not explicitly provided
if [ -z "$RUNTIME" ] && [ -d "$APP_DIR" ]; then
  if [ -f "$APP_DIR/pom.xml" ] || [ -f "$APP_DIR/build.gradle" ] || [ -f "$APP_DIR/build.gradle.kts" ]; then
    RUNTIME="jvm"
  elif [ -f "$APP_DIR/package.json" ]; then
    RUNTIME="nodejs"
  elif [ -f "$APP_DIR/requirements.txt" ] || [ -f "$APP_DIR/pyproject.toml" ]; then
    RUNTIME="python"
  elif ls "$APP_DIR"/*.csproj 1>/dev/null 2>&1; then
    RUNTIME="dotnet"
  elif [ -f "$APP_DIR/go.mod" ]; then
    RUNTIME="go"
  fi
fi

echo "==> OBSynx Injector: populating $TARGET_DIR (selected runtime: ${RUNTIME:-all})..."

case "$RUNTIME" in
  jvm)
    mkdir -p "$TARGET_DIR/jvm"
    cp -r /opt/obsynx/adapters/opentelemetry-java/* "$TARGET_DIR/jvm/" 2>/dev/null || true
    ;;
  nodejs)
    mkdir -p "$TARGET_DIR/nodejs"
    cp -r /opt/obsynx/adapters/opentelemetry-nodejs/* "$TARGET_DIR/nodejs/" 2>/dev/null || true
    ;;
  python)
    mkdir -p "$TARGET_DIR/python"
    cp -r /opt/obsynx/adapters/opentelemetry-python/* "$TARGET_DIR/python/" 2>/dev/null || true
    ;;
  dotnet)
    mkdir -p "$TARGET_DIR/dotnet"
    cp -r /opt/obsynx/adapters/opentelemetry-dotnet/* "$TARGET_DIR/dotnet/" 2>/dev/null || true
    ;;
  go)
    mkdir -p "$TARGET_DIR/ebpf"
    cp -r /opt/obsynx/adapters/obi-ebpf/* "$TARGET_DIR/ebpf/" 2>/dev/null || true
    ;;
  *)
    # Copy all adapters if runtime is not isolated
    cp -r /opt/obsynx/adapters/* "$TARGET_DIR/" 2>/dev/null || true
    ;;
esac

echo "==> OBSynx Injector: completed successfully."

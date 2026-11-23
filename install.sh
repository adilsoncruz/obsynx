#!/usr/bin/env bash
set -euo pipefail

# OBSynx Universal CLI Installer
# Usage: curl -sSL https://raw.githubusercontent.com/adilsoncruz/obsynx/main/install.sh | bash

REPO="adilsoncruz/obsynx"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

echo "==> Installing OBSynx CLI..."

# Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux*)  OS="linux" ;;
  darwin*) OS="darwin" ;;
  *) echo "Unsupported operating system: $OS" && exit 1 ;;
esac

# Detect Architecture
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" && exit 1 ;;
esac

echo "==> Detected platform: ${OS}/${ARCH}"

TAG="${VERSION:-}"
if [ -z "$TAG" ]; then
  if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    TAG=$(gh release view --repo "$REPO" --json tagName -q .tagName 2>/dev/null || true)
  elif [ -n "${GITHUB_TOKEN:-}" ]; then
    LATEST_JSON=$(curl -sSL -H "Authorization: token ${GITHUB_TOKEN}" "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null || true)
    TAG=$(echo "$LATEST_JSON" | grep -o '"tag_name": *"[^"]*"' | head -n 1 | cut -d '"' -f 4 || true)
  else
    LATEST_JSON=$(curl -sSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null || true)
    TAG=$(echo "$LATEST_JSON" | grep -o '"tag_name": *"[^"]*"' | head -n 1 | cut -d '"' -f 4 || true)
  fi
fi

if [ -z "$TAG" ]; then
  TAG="v0.1.0"
  echo "==> Using default release tag: ${TAG}"
fi

VERSION_CLEAN="${TAG#v}"
TARBALL="obsynx_${VERSION_CLEAN}_${OS}_${ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${TAG}/${TARBALL}"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "==> Downloading ${TARBALL} (${TAG})..."

if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
  gh release download "$TAG" --repo "$REPO" -p "$TARBALL" -D "$TMP_DIR"
elif [ -n "${GITHUB_TOKEN:-}" ]; then
  curl -sSL -H "Authorization: token ${GITHUB_TOKEN}" -o "${TMP_DIR}/${TARBALL}" "$DOWNLOAD_URL"
else
  curl -sSL -f -o "${TMP_DIR}/${TARBALL}" "$DOWNLOAD_URL" || {
    echo "ERROR: Failed to download ${DOWNLOAD_URL}."
    echo "If the repository is private, export GITHUB_TOKEN or authenticate via GitHub CLI (gh):"
    echo "  export GITHUB_TOKEN=your_token"
    echo "  curl -sSL ... | bash"
    exit 1
  }
fi

echo "==> Extracting binary..."
tar -xzf "${TMP_DIR}/${TARBALL}" -C "$TMP_DIR"

echo "==> Installing to ${INSTALL_DIR}..."
mkdir -p "$INSTALL_DIR" 2>/dev/null || true

if [ -w "$INSTALL_DIR" ]; then
  mv "${TMP_DIR}/obsynx" "${INSTALL_DIR}/obsynx"
  chmod +x "${INSTALL_DIR}/obsynx"
elif command -v sudo >/dev/null 2>&1; then
  echo "Elevated permissions required to write to ${INSTALL_DIR}"
  sudo mv "${TMP_DIR}/obsynx" "${INSTALL_DIR}/obsynx"
  sudo chmod +x "${INSTALL_DIR}/obsynx"
else
  FALLBACK_DIR="${HOME}/.local/bin"
  mkdir -p "$FALLBACK_DIR"
  mv "${TMP_DIR}/obsynx" "${FALLBACK_DIR}/obsynx"
  chmod +x "${FALLBACK_DIR}/obsynx"
  INSTALL_DIR="$FALLBACK_DIR"
  echo "==> Installed to ${FALLBACK_DIR}. Ensure it is in your PATH."
fi

echo "==> OBSynx CLI installed successfully!"
"${INSTALL_DIR}/obsynx" --help | head -n 3

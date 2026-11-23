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

# Determine Version
GITHUB_API_HEADER=()
if [ -n "${GITHUB_TOKEN:-}" ]; then
  GITHUB_API_HEADER=(-H "Authorization: token ${GITHUB_TOKEN}")
fi

TAG="${VERSION:-}"
if [ -z "$TAG" ]; then
  LATEST_JSON=$(curl -sSL "${GITHUB_API_HEADER[@]}" "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null || true)
  TAG=$(echo "$LATEST_JSON" | grep -o '"tag_name": *"[^"]*"' | head -n 1 | cut -d '"' -f 4 || true)
fi

if [ -z "$TAG" ]; then
  TAG="v0.1.0"
  echo "==> Could not detect latest tag automatically, using fallback: ${TAG}"
fi

VERSION_CLEAN="${TAG#v}"
TARBALL="obsynx_${VERSION_CLEAN}_${OS}_${ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${TAG}/${TARBALL}"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "==> Downloading ${TARBALL} from ${TAG}..."
if [ -n "${GITHUB_TOKEN:-}" ]; then
  curl -sSL -H "Authorization: token ${GITHUB_TOKEN}" -o "${TMP_DIR}/${TARBALL}" "$DOWNLOAD_URL"
else
  curl -sSL -f -o "${TMP_DIR}/${TARBALL}" "$DOWNLOAD_URL" || {
    echo "ERROR: Failed to download ${DOWNLOAD_URL}."
    echo "If the repository is private, export GITHUB_TOKEN before running:"
    echo "  export GITHUB_TOKEN=your_token"
    echo "  curl -sSL ... | bash"
    exit 1
  }
fi

echo "==> Extracting binary..."
tar -xzf "${TMP_DIR}/${TARBALL}" -C "$TMP_DIR"

echo "==> Installing to ${INSTALL_DIR}..."
if [ -w "$INSTALL_DIR" ]; then
  mv "${TMP_DIR}/obsynx" "${INSTALL_DIR}/obsynx"
  chmod +x "${INSTALL_DIR}/obsynx"
else
  echo "Elevated permissions required to write to ${INSTALL_DIR}"
  sudo mv "${TMP_DIR}/obsynx" "${INSTALL_DIR}/obsynx"
  sudo chmod +x "${INSTALL_DIR}/obsynx"
fi

echo "==> OBSynx CLI installed successfully!"
"${INSTALL_DIR}/obsynx" --help | head -n 3

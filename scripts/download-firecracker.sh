#!/bin/bash
# Run this script with: go generate ./...

set -euo pipefail

FIRECRACKER_VERSION="v1.13.1"
BASE_URL="https://github.com/firecracker-microvm/firecracker/releases/download"

# Determine architecture
case $(uname -m) in
  x86_64) ARCH="x86_64" ;;
  aarch64) ARCH="aarch64" ;;
  arm64) ARCH="aarch64" ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

# Create binaries directory
mkdir -p binaries

FIRECRACKER_PATH="binaries/firecracker"

# Skip if already exists
if [[ -f "$FIRECRACKER_PATH" ]]; then
  echo "Firecracker binary already exists, skipping download"
else
  echo "Downloading Firecracker binary for $ARCH..."

  # Fallback to tarball
  TARBALL_URL="$BASE_URL/$FIRECRACKER_VERSION/firecracker-$FIRECRACKER_VERSION-$ARCH.tgz"
  echo "Downloading tarball: $TARBALL_URL"

  TEMP_TAR=$(mktemp)
  trap "rm -f $TEMP_TAR" EXIT

  curl -L -f -o "$TEMP_TAR" "$TARBALL_URL"

  # Extract firecracker binary from tarball
  tar -tzf "$TEMP_TAR" | grep -E "firecracker-v.*-$ARCH\$" | head -1 | xargs tar -xzf "$TEMP_TAR" -O > "$FIRECRACKER_PATH"

  # Make executable
  chmod +x "$FIRECRACKER_PATH"

  echo "Successfully downloaded firecracker binary to $FIRECRACKER_PATH"
fi

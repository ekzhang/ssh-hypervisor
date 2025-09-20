#!/bin/bash
set -euo pipefail

# Determine architecture
case $(uname -m) in
  x86_64) ARCH="x86_64" ;;
  aarch64) ARCH="aarch64" ;;
  arm64) ARCH="aarch64" ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

# Create binaries directory
mkdir -p binaries

VMLINUX_PATH="binaries/vmlinux"

# Skip if already exists
if [[ -f "$VMLINUX_PATH" ]]; then
  echo "vmlinux kernel already exists, skipping download"
  exit 0
fi

echo "Downloading vmlinux kernel for $ARCH..."

# Use Amazon Linux microVM kernel (officially supported by Firecracker)
# Based on kernel v6.1 which is supported until 2026-09-02
case $ARCH in
  x86_64)
    KERNEL_URL="https://s3.amazonaws.com/spec.ccfc.min/img/quickstart_guide/x86_64/kernels/vmlinux.bin"
    ;;
  aarch64)
    KERNEL_URL="https://s3.amazonaws.com/spec.ccfc.min/img/quickstart_guide/aarch64/kernels/vmlinux.bin"
    ;;
  *)
    echo "Error: No kernel available for architecture $ARCH" >&2
    exit 1
    ;;
esac

if curl -L -f -o "$VMLINUX_PATH" "$KERNEL_URL" 2>/dev/null; then
  echo "vmlinux kernel download successful"
else
  echo "Error: Failed to download vmlinux kernel from $KERNEL_URL" >&2
  echo "You may need to build or provide your own kernel at $VMLINUX_PATH" >&2
  exit 1
fi

echo "Successfully downloaded vmlinux kernel to $VMLINUX_PATH"

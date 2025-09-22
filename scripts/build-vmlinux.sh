#!/bin/bash
set -euo pipefail

# Determine architecture
case $(uname -m) in
  x86_64) ARCH="x86_64" ;;
  aarch64) ARCH="arm64" ;;
  arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

# Create binaries directory
mkdir -p binaries

VMLINUX_PATH="binaries/vmlinux"
KERNEL_VERSION="6.16.8"
KERNEL_URL="https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-${KERNEL_VERSION}.tar.xz"
KERNEL_DIR="linux-${KERNEL_VERSION}"

# Skip if already exists
if [[ -f "$VMLINUX_PATH" ]]; then
  echo "vmlinux kernel already exists, skipping build"
  exit 0
fi

# Cleanup function
cleanup() {
  if [[ -n "${KERNEL_DIR:-}" && -d "$KERNEL_DIR" ]]; then
    rm -rf "$KERNEL_DIR"
  fi
  if [[ -f "linux-${KERNEL_VERSION}.tar.xz" ]]; then
    rm -f "linux-${KERNEL_VERSION}.tar.xz"
  fi
}
trap cleanup EXIT

echo "Building vmlinux kernel v${KERNEL_VERSION} for $ARCH..."

# Check for required build tools
if ! command -v make >/dev/null 2>&1; then
  echo "Error: make not found. Install build-essential or equivalent" >&2
  exit 1
fi

if ! command -v gcc >/dev/null 2>&1; then
  echo "Error: gcc not found. Install build-essential or equivalent" >&2
  exit 1
fi

# Download kernel source
echo "Downloading kernel source..."
if ! curl -L -f -o "linux-${KERNEL_VERSION}.tar.xz" "$KERNEL_URL"; then
  echo "Error: Failed to download kernel from $KERNEL_URL" >&2
  exit 1
fi

# Extract kernel source
echo "Extracting kernel source..."
tar -xf "linux-${KERNEL_VERSION}.tar.xz"
cd "$KERNEL_DIR"

# Create minimal config for Firecracker
echo "Creating minimal kernel config..."
case $ARCH in
  x86_64)
    make tinyconfig
    ;;
  arm64)
    make ARCH=arm64 tinyconfig
    ;;
esac

# Minimal config fragment
cat > fc-minimal.config <<'EOF'
CONFIG_EXPERT=y
CONFIG_EMBEDDED=y
CONFIG_MODULES=n

# CPU/virt guest
CONFIG_PARAVIRT=y
CONFIG_KVM_GUEST=y              # x86; harmless on arm64 if unmet

# Console on ttyS0
CONFIG_TTY=y
CONFIG_SERIAL_8250=y
CONFIG_SERIAL_8250_CONSOLE=y
CONFIG_PRINTK=y

# Block + filesystems + initrd/devtmpfs
CONFIG_BLOCK=y
CONFIG_BLK_DEV_INITRD=y
CONFIG_DEVTMPFS=y
CONFIG_DEVTMPFS_MOUNT=y
CONFIG_TMPFS=y
CONFIG_EXT4_FS=y

# VirtIO (Firecracker uses MMIO, not PCI)
CONFIG_VIRTIO=y
CONFIG_VIRTIO_MMIO=y
CONFIG_VIRTIO_BLK=y
CONFIG_VIRTIO_NET=y

# Basic networking
CONFIG_NET=y
CONFIG_INET=y
CONFIG_NET_CORE=y

# Size/optimization
CONFIG_CC_OPTIMIZE_FOR_SIZE=y

# Chop out desktop/server bloat
CONFIG_PCI=n
CONFIG_DRM=n
CONFIG_HID=n
CONFIG_INPUT=n
CONFIG_SOUND=n
CONFIG_MEDIA_SUPPORT=n
CONFIG_USB=n
CONFIG_THERMAL=n
CONFIG_I2C=n
CONFIG_SPI=n
CONFIG_EFI=n
CONFIG_FW_LOADER=n
EOF

# Merge the fragment and auto-accept minimal defaults
./scripts/kconfig/merge_config.sh -m .config fc-minimal.config

case $ARCH in
  x86_64)
    make olddefconfig
    ;;
  arm64)
    make ARCH=arm64 olddefconfig
    ;;
esac

# Build kernel (Firecracker wants uncompressed ELF vmlinux)
echo "Building kernel (small, Firecracker-friendly)…"
case $ARCH in
  x86_64)
    make -j"$(nproc)" vmlinux
    ;;
  arm64)
    make ARCH=arm64 CROSS_COMPILE=aarch64-linux-gnu- -j"$(nproc)" vmlinux
    ;;
esac

# Copy vmlinux to binaries directory
echo "Copying vmlinux to binaries directory..."
cp vmlinux "../$VMLINUX_PATH"

echo "Successfully built vmlinux kernel v${KERNEL_VERSION} at $VMLINUX_PATH"

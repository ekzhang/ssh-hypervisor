#!/bin/bash
# Used to build kernels, prebuilt outputs can be fetched with download-vmlinux.sh

set -e

JOBS="${JOBS:-$(nproc)}"
OUTDIR="${PWD}/out"
LINUX_TAG="microvm-kernel-6.1.150-12.277.amzn2023"

# Detect architecture
ARCH=$(uname -m)
case "${ARCH}" in
  x86_64)
    KERNEL_ARCH="x86_64"
    FC_CFG_URL="https://raw.githubusercontent.com/firecracker-microvm/firecracker/v1.13.1/resources/guest_configs/microvm-kernel-ci-x86_64-6.1.config"
    KERNEL_TARGET="vmlinux"
    KERNEL_OUTPUT="vmlinux"
    ;;
  arm64|aarch64)
    KERNEL_ARCH="aarch64"
    FC_CFG_URL="https://raw.githubusercontent.com/firecracker-microvm/firecracker/v1.13.1/resources/guest_configs/microvm-kernel-ci-aarch64-6.1.config"
    KERNEL_TARGET="Image"
    KERNEL_OUTPUT="arch/arm64/boot/Image"
    ;;
  *)
    echo "Unsupported architecture: ${ARCH}"
    exit 1
    ;;
esac

sudo apt-get update
sudo apt-get install -y build-essential bc bison flex libssl-dev \
  libelf-dev dwarves wget curl xz-utils cpio git

mkdir -p "${OUTDIR}"

if [ ! -d "linux" ]; then
  echo "[*] Cloning Amazon Linux kernel repository (tag ${LINUX_TAG})..."
  git clone --depth 1 --branch "${LINUX_TAG}" https://github.com/amazonlinux/linux.git
fi

cd linux

echo "[*] Fetching Firecracker ${KERNEL_ARCH} microvm config..."
curl -fsSL "${FC_CFG_URL}" -o .config

# Make sure no stale prompts block us
make olddefconfig

echo "[*] Building ${KERNEL_TARGET} for ${KERNEL_ARCH} (this can take a while)…"
make -j"${JOBS}" "${KERNEL_TARGET}"

echo "[*] Collecting artifacts in ${OUTDIR}"
cp -v "${KERNEL_OUTPUT}" "${OUTDIR}/${KERNEL_TARGET}-$(make -s kernelrelease)"
cp -v .config "${OUTDIR}/config-$(make -s kernelrelease)"
cp -v System.map "${OUTDIR}/System.map-$(make -s kernelrelease)"

cat <<EOF

✅ Done!

Artifacts:
  ${OUTDIR}/${KERNEL_TARGET}-$(make -s kernelrelease)
  ${OUTDIR}/config-$(make -s kernelrelease)

EOF

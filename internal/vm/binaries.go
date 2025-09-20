package vm

import _ "embed"

//go:generate ../../scripts/download-firecracker.sh
//go:generate ../../scripts/download-vmlinux.sh

//go:embed binaries/firecracker
var firecrackerBinary []byte

//go:embed binaries/vmlinux
var vmlinuxBinary []byte

// GetFirecrackerBinary returns the embedded firecracker binary
func GetFirecrackerBinary() []byte {
	return firecrackerBinary
}

// GetVmlinuxBinary returns the embedded vmlinux kernel
func GetVmlinuxBinary() []byte {
	return vmlinuxBinary
}

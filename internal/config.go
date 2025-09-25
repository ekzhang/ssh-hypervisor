package internal

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// Config holds all configuration options for the ssh-hypervisor
type Config struct {
	Port             int    // SSH server port
	HostKey          string // Path to SSH host key
	VMCIDR           string // CIDR block for VM IP addresses
	VMMemory         int    // VM memory in MB
	VMCPUs           int    // Number of VM CPUs
	MaxConcurrentVMs int    // Maximum number of concurrent VMs (0 = unlimited)
	DataDir          string // Directory for VM snapshots and data
	Rootfs           string // Path to rootfs image
	AllowInternet    bool   // Allow VMs to access the Internet
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	// Validate port
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}

	// Validate CIDR
	_, ipNet, err := net.ParseCIDR(c.VMCIDR)
	if err != nil {
		return fmt.Errorf("invalid VM CIDR: %v", err)
	}
	if ipNet.IP.To4() == nil {
		return fmt.Errorf("only IPv4 CIDR is supported")
	}

	// Check if CIDR is large enough (at least /28 for 14 usable IPs)
	ones, _ := ipNet.Mask.Size()
	if ones > 28 {
		return fmt.Errorf("VM CIDR must be /28 or larger to accommodate multiple VMs")
	}

	// Validate VM resources
	if c.VMMemory < 64 {
		return fmt.Errorf("VM memory must be at least 64 MB")
	}
	if c.VMCPUs < 1 {
		return fmt.Errorf("VM must have at least 1 CPU")
	}
	if c.MaxConcurrentVMs < 0 {
		return fmt.Errorf("max concurrent VMs cannot be negative (use 0 for unlimited)")
	}

	// Ensure data directory exists
	if err := os.MkdirAll(c.DataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %v", err)
	}

	// Generate host key path if not provided
	if c.HostKey == "" {
		c.HostKey = filepath.Join(c.DataDir, "ssh_host_key")
	}

	// Validate rootfs image
	if c.Rootfs == "" {
		return fmt.Errorf("rootfs image path is required")
	}
	if _, err := os.Stat(c.Rootfs); os.IsNotExist(err) {
		return fmt.Errorf("rootfs image not found: %s", c.Rootfs)
	}

	return nil
}

// GetVMIPRange returns the usable IP range for VMs
func (c *Config) GetVMIPRange() (*net.IPNet, error) {
	_, ipNet, err := net.ParseCIDR(c.VMCIDR)
	if err != nil {
		return nil, err
	}
	return ipNet, nil
}

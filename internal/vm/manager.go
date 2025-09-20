package vm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/ekzhang/ssh-hypervisor/internal"
)

// VM represents a single Firecracker microVM instance
type VM struct {
	ID           string
	UserID       string
	IP           net.IP
	SocketPath   string
	PIDFile      string
	process      *exec.Cmd
	config       *internal.Config
	dataDir      string
}

// Manager manages the lifecycle of Firecracker VMs
type Manager struct {
	config *internal.Config
	vms    map[string]*VM
	ipPool *IPPool
}

// NewManager creates a new VM manager
func NewManager(config *internal.Config) (*Manager, error) {
	ipNet, err := config.GetVMIPRange()
	if err != nil {
		return nil, fmt.Errorf("failed to parse VM IP range: %w", err)
	}

	ipPool, err := NewIPPool(ipNet)
	if err != nil {
		return nil, fmt.Errorf("failed to create IP pool: %w", err)
	}

	return &Manager{
		config: config,
		vms:    make(map[string]*VM),
		ipPool: ipPool,
	}, nil
}

// CreateVM creates and starts a new VM for the given user
func (m *Manager) CreateVM(ctx context.Context, userID string, firecrackerBinary []byte, vmlinuxBinary []byte) (*VM, error) {
	vmID := generateVMID(userID)

	// Allocate IP address
	ip, err := m.ipPool.Allocate()
	if err != nil {
		return nil, fmt.Errorf("failed to allocate IP: %w", err)
	}

	// Create VM data directory
	vmDataDir := filepath.Join(m.config.DataDir, vmID)
	if err := os.MkdirAll(vmDataDir, 0755); err != nil {
		m.ipPool.Release(ip)
		return nil, fmt.Errorf("failed to create VM data directory: %w", err)
	}

	vm := &VM{
		ID:         vmID,
		UserID:     userID,
		IP:         ip,
		SocketPath: filepath.Join(vmDataDir, "firecracker.sock"),
		PIDFile:    filepath.Join(vmDataDir, "firecracker.pid"),
		config:     m.config,
		dataDir:    vmDataDir,
	}

	// Write Firecracker binary to disk
	firecrackerPath := filepath.Join(vmDataDir, "firecracker")
	if err := os.WriteFile(firecrackerPath, firecrackerBinary, 0755); err != nil {
		m.ipPool.Release(ip)
		return nil, fmt.Errorf("failed to write firecracker binary: %w", err)
	}

	// Write vmlinux kernel to disk
	vmlinuxPath := filepath.Join(vmDataDir, "vmlinux")
	if err := os.WriteFile(vmlinuxPath, vmlinuxBinary, 0644); err != nil {
		m.ipPool.Release(ip)
		return nil, fmt.Errorf("failed to write vmlinux kernel: %w", err)
	}

	// Start the VM
	if err := vm.Start(ctx); err != nil {
		m.ipPool.Release(ip)
		return nil, fmt.Errorf("failed to start VM: %w", err)
	}

	m.vms[vmID] = vm
	return vm, nil
}

// GetVM returns the VM for a given user ID
func (m *Manager) GetVM(userID string) (*VM, bool) {
	vmID := generateVMID(userID)
	vm, exists := m.vms[vmID]
	return vm, exists
}

// DestroyVM stops and removes a VM
func (m *Manager) DestroyVM(vmID string) error {
	vm, exists := m.vms[vmID]
	if !exists {
		return fmt.Errorf("VM %s not found", vmID)
	}

	if err := vm.Stop(); err != nil {
		return fmt.Errorf("failed to stop VM: %w", err)
	}

	m.ipPool.Release(vm.IP)
	delete(m.vms, vmID)

	return nil
}

// Start starts the Firecracker process for this VM
func (vm *VM) Start(ctx context.Context) error {
	// Remove existing socket
	os.Remove(vm.SocketPath)

	firecrackerPath := filepath.Join(vm.dataDir, "firecracker")

	// Create Firecracker command
	vm.process = exec.CommandContext(ctx, firecrackerPath,
		"--api-sock", vm.SocketPath,
	)

	// Set up logging
	logFile, err := os.Create(filepath.Join(vm.dataDir, "firecracker.log"))
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}

	vm.process.Stdout = logFile
	vm.process.Stderr = logFile

	// Start the process
	if err := vm.process.Start(); err != nil {
		return fmt.Errorf("failed to start firecracker process: %w", err)
	}

	// Write PID file
	if err := os.WriteFile(vm.PIDFile, []byte(fmt.Sprintf("%d", vm.process.Process.Pid)), 0644); err != nil {
		vm.process.Process.Kill()
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	// Wait for API socket to be ready
	if err := vm.waitForSocket(5 * time.Second); err != nil {
		vm.process.Process.Kill()
		return fmt.Errorf("firecracker API socket not ready: %w", err)
	}

	// Configure the VM via API
	if err := vm.configure(); err != nil {
		vm.process.Process.Kill()
		return fmt.Errorf("failed to configure VM: %w", err)
	}

	return nil
}

// Stop stops the Firecracker process
func (vm *VM) Stop() error {
	if vm.process != nil && vm.process.Process != nil {
		if err := vm.process.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}
		vm.process.Wait()
	}

	// Clean up files
	os.Remove(vm.SocketPath)
	os.Remove(vm.PIDFile)

	return nil
}

// waitForSocket waits for the Firecracker API socket to be ready
func (vm *VM) waitForSocket(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for API socket")
		default:
			if _, err := os.Stat(vm.SocketPath); err == nil {
				// Socket file exists, try to connect
				conn, err := net.Dial("unix", vm.SocketPath)
				if err == nil {
					conn.Close()
					return nil
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// configure configures the VM via the Firecracker API
func (vm *VM) configure() error {
	// Configure machine resources
	machineConfig := map[string]interface{}{
		"vcpu_count":   vm.config.VMCPUs,
		"mem_size_mib": vm.config.VMMemory,
	}

	if err := vm.putAPI("/machine-config", machineConfig); err != nil {
		return fmt.Errorf("failed to configure machine: %w", err)
	}

	// Configure boot source (kernel)
	vmlinuxPath := filepath.Join(vm.dataDir, "vmlinux")
	bootSource := map[string]interface{}{
		"kernel_image_path": vmlinuxPath,
		"boot_args":         "console=ttyS0 reboot=k panic=1 pci=off",
	}

	if err := vm.putAPI("/boot-source", bootSource); err != nil {
		return fmt.Errorf("failed to configure boot source: %w", err)
	}

	// Configure root drive (rootfs)
	drive := map[string]interface{}{
		"drive_id":        "rootfs",
		"path_on_host":    vm.config.Rootfs,
		"is_root_device":  true,
		"is_read_only":    false,
	}

	if err := vm.putAPI("/drives/rootfs", drive); err != nil {
		return fmt.Errorf("failed to configure root drive: %w", err)
	}

	// TODO: Add network interface configuration

	return nil
}

// putAPI makes a PUT request to the Firecracker API
func (vm *VM) putAPI(endpoint string, data interface{}) error {
	conn, err := net.Dial("unix", vm.SocketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to API socket: %w", err)
	}
	defer conn.Close()

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	request := fmt.Sprintf("PUT %s HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
		endpoint, len(jsonData), string(jsonData))

	if _, err := conn.Write([]byte(request)); err != nil {
		return fmt.Errorf("failed to write request: %w", err)
	}

	// Read response
	response := make([]byte, 4096)
	n, err := conn.Read(response)
	if err != nil && err != io.EOF {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Basic response validation (should be 2xx status)
	responseStr := string(response[:n])
	if len(responseStr) < 12 || responseStr[9] != '2' {
		return fmt.Errorf("API request failed: %s", responseStr)
	}

	return nil
}

// generateVMID generates a VM ID based on user ID
func generateVMID(userID string) string {
	return fmt.Sprintf("vm-%s", userID)
}
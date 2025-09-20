package vm

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ekzhang/ssh-hypervisor/internal"
	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"github.com/sirupsen/logrus"
)

// VM represents a single Firecracker microVM instance
type VM struct {
	ID         string
	UserID     string
	IP         net.IP
	SocketPath string
	PIDFile    string
	machine    *firecracker.Machine
	config     *internal.Config
	dataDir    string
	logger     *logrus.Entry
}

// Manager manages the lifecycle of Firecracker VMs
type Manager struct {
	config *internal.Config
	vms    map[string]*VM
	ipPool *IPPool
	logger logrus.FieldLogger
}

// NewManager creates a new VM manager
func NewManager(config *internal.Config, logger logrus.FieldLogger) (*Manager, error) {
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
		logger: logger,
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
		logger:     m.logger.WithField("vm_id", vmID),
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

	vmlinuxPath := filepath.Join(vm.dataDir, "vmlinux")
	firecrackerPath := filepath.Join(vm.dataDir, "firecracker")

	// Create machine configuration
	cfg := firecracker.Config{
		SocketPath:      vm.SocketPath,
		KernelImagePath: vmlinuxPath,
		KernelArgs:      "console=ttyS0 reboot=k panic=1 pci=off",
		Drives: []models.Drive{
			{
				DriveID:      firecracker.String("rootfs"),
				IsRootDevice: firecracker.Bool(true),
				IsReadOnly:   firecracker.Bool(false),
				PathOnHost:   firecracker.String(vm.config.Rootfs),
			},
		},
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(int64(vm.config.VMCPUs)),
			MemSizeMib: firecracker.Int64(int64(vm.config.VMMemory)),
		},
		// TODO: Add network interface configuration
	}

	// Create a custom command that uses our embedded firecracker binary
	cmd := exec.CommandContext(ctx, firecrackerPath, "--api-sock", vm.SocketPath)

	machine, err := firecracker.NewMachine(
		ctx, cfg,
		firecracker.WithProcessRunner(cmd),
		firecracker.WithLogger(vm.logger),
	)
	if err != nil {
		return fmt.Errorf("failed to create machine: %w", err)
	}
	vm.machine = machine

	// Start the machine
	if err := machine.Start(ctx); err != nil {
		return fmt.Errorf("failed to start machine: %w", err)
	}

	// Write PID file
	pid, err := machine.PID()
	if err != nil {
		machine.Shutdown(ctx)
		return fmt.Errorf("failed to get PID: %w", err)
	}
	if err := os.WriteFile(vm.PIDFile, []byte(fmt.Sprintf("%d", pid)), 0644); err != nil {
		machine.Shutdown(ctx)
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	return nil
}

// Stop stops the Firecracker process
func (vm *VM) Stop() error {
	if vm.machine != nil {
		ctx := context.Background()
		if err := vm.machine.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown machine: %w", err)
		}
	}

	// Clean up files
	os.Remove(vm.SocketPath)
	os.Remove(vm.PIDFile)

	return nil
}

// generateVMID generates a VM ID based on user ID
func generateVMID(userID string) string {
	return fmt.Sprintf("vm-%s", userID)
}

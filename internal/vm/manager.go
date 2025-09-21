package vm

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

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
	Gateway    net.IP
	Netmask    net.IP
	SocketPath string
	PIDFile    string
	machine    *firecracker.Machine
	config     *internal.Config
	dataDir    string
	logger     *logrus.Entry
}

// Manager manages the lifecycle of Firecracker VMs
type Manager struct {
	config     *internal.Config
	vms        map[string]*VM
	ipPool     *IPPool
	bridgeName string
	logger     logrus.FieldLogger
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

	bridgeName := "sshvm-br0"

	manager := &Manager{
		config:     config,
		vms:        make(map[string]*VM),
		ipPool:     ipPool,
		bridgeName: bridgeName,
		logger:     logger,
	}

	// Set up network bridge
	if err := manager.setupNetworkBridge(); err != nil {
		return nil, fmt.Errorf("failed to setup network bridge: %w", err)
	}

	return manager, nil
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
		Gateway:    m.ipPool.Gateway(),
		Netmask:    m.ipPool.Netmask(),
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
		os.RemoveAll(vmDataDir)
		return nil, fmt.Errorf("failed to write firecracker binary: %w", err)
	}

	// Write vmlinux kernel to disk
	vmlinuxPath := filepath.Join(vmDataDir, "vmlinux")
	if err := os.WriteFile(vmlinuxPath, vmlinuxBinary, 0644); err != nil {
		m.ipPool.Release(ip)
		os.RemoveAll(vmDataDir)
		return nil, fmt.Errorf("failed to write vmlinux kernel: %w", err)
	}

	// Copy the rootfs image to the VM data directory (writable)
	buf, err := os.ReadFile(vm.config.Rootfs)
	if err == nil {
		err = os.WriteFile(filepath.Join(vmDataDir, "rootfs.img"), buf, 0644)
	}
	if err != nil {
		m.ipPool.Release(ip)
		os.RemoveAll(vmDataDir)
		return nil, fmt.Errorf("failed to copy rootfs image: %w", err)
	}

	// Start the VM
	if err := vm.Start(ctx, m); err != nil {
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
func (vm *VM) Start(ctx context.Context, manager *Manager) error {
	// Remove existing socket
	os.Remove(vm.SocketPath)

	vmlinuxPath := filepath.Join(vm.dataDir, "vmlinux")
	firecrackerPath := filepath.Join(vm.dataDir, "firecracker")

	bootArgs := "console=ttyS0 noapic reboot=k panic=1 pci=off nomodules random.trust_cpu=on"
	bootArgs += fmt.Sprintf(" ip=%s::%s:%s::eth0:off", vm.IP, vm.Gateway, vm.Netmask)

	// Generate unique ID from VM IP for MAC and TAP device (only works for <65535 VMs)
	vmNetID := int(vm.IP[len(vm.IP)-2])*256 + int(vm.IP[len(vm.IP)-1])
	tapName := fmt.Sprintf("sshvm-tap-%d", vmNetID)

	// Setup TAP device
	if err := manager.setupTAPDevice(tapName); err != nil {
		return fmt.Errorf("failed to setup TAP device: %w", err)
	}

	// Create machine configuration
	cfg := firecracker.Config{
		SocketPath:      vm.SocketPath,
		KernelImagePath: vmlinuxPath,
		KernelArgs:      bootArgs,
		ForwardSignals:  []os.Signal{}, // Don't forward any signals to firecracker
		Drives: []models.Drive{
			{
				DriveID:      firecracker.String("rootfs"),
				IsRootDevice: firecracker.Bool(true),
				IsReadOnly:   firecracker.Bool(false),
				PathOnHost:   firecracker.String(filepath.Join(vm.dataDir, "rootfs.img")),
			},
		},
		NetworkInterfaces: []firecracker.NetworkInterface{
			{
				StaticConfiguration: &firecracker.StaticNetworkConfiguration{
					// Network setup: https://gist.github.com/jvns/9b274f24cfa1db7abecd0d32483666a3
					MacAddress:  fmt.Sprintf("02:FC:00:00:%02x:%02x", vmNetID>>8, vmNetID&0xFF),
					HostDevName: tapName,
				},
				AllowMMDS: false,
			},
		},
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(int64(vm.config.VMCPUs)),
			MemSizeMib: firecracker.Int64(int64(vm.config.VMMemory)),
		},
	}

	// Create a custom command that uses our embedded firecracker binary
	cmd := exec.CommandContext(ctx, firecrackerPath, "--api-sock", vm.SocketPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// Create a process group so that signals (SIGINT) are not forwarded.
		Setpgid: true,
	}

	// Capture VM console output (boot logs, OpenRC, SSH, etc.)
	logPath := filepath.Join(vm.dataDir, "console.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer logFile.Close()

	cmd.Stdout = logFile
	cmd.Stderr = logFile

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
	if err := os.WriteFile(vm.PIDFile, fmt.Appendf(nil, "%d", pid), 0644); err != nil {
		machine.Shutdown(ctx)
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	return nil
}

// Stop stops the Firecracker process
func (vm *VM) Stop() error {
	if vm.machine != nil {
		ctx := context.Background()
		err := vm.machine.Shutdown(ctx)

		vm.machine.StopVMM()
		vm.machine.Wait(ctx)
		os.RemoveAll(vm.dataDir)

		if err != nil {
			return fmt.Errorf("failed to shutdown machine: %w", err)
		}

		vm.machine = nil
	}

	return nil
}

// generateVMID generates a VM ID based on user ID
func generateVMID(userID string) string {
	return fmt.Sprintf("vm-%s", userID)
}

// setupNetworkBridge creates and configures the network bridge
func (m *Manager) setupNetworkBridge() error {
	// Check if bridge already exists
	if err := exec.Command("ip", "link", "show", m.bridgeName).Run(); err == nil {
		m.logger.Infof("Bridge %s already exists", m.bridgeName)
		return nil
	}

	// Create bridge
	if err := exec.Command("ip", "link", "add", "name", m.bridgeName, "type", "bridge").Run(); err != nil {
		return fmt.Errorf("failed to create bridge %s: %w", m.bridgeName, err)
	}
	m.logger.Infof("Created bridge: %s", m.bridgeName)

	// Configure bridge IP (gateway)
	gateway := m.ipPool.Gateway()
	gatewayWithMask := fmt.Sprintf("%s/24", gateway) // TODO: make this dynamic based on network mask
	if err := exec.Command("ip", "addr", "add", gatewayWithMask, "dev", m.bridgeName).Run(); err != nil {
		// Ignore error if address already exists
		if !strings.Contains(err.Error(), "File exists") {
			return fmt.Errorf("failed to add IP to bridge: %w", err)
		}
	}

	// Bring bridge up
	if err := exec.Command("ip", "link", "set", "dev", m.bridgeName, "up").Run(); err != nil {
		return fmt.Errorf("failed to bring bridge up: %w", err)
	}

	// Enable IP forwarding
	if err := exec.Command("sh", "-c", "echo 1 > /proc/sys/net/ipv4/ip_forward").Run(); err != nil {
		return fmt.Errorf("failed to enable IP forwarding: %w", err)
	}

	m.logger.Infof("Bridge %s configured with gateway %s", m.bridgeName, gateway)
	return nil
}

// setupTAPDevice creates and configures a TAP device for a VM
func (m *Manager) setupTAPDevice(tapName string) error {
	// Check if TAP device already exists
	if err := exec.Command("ip", "link", "show", tapName).Run(); err == nil {
		m.logger.Debugf("TAP device %s already exists", tapName)
		return nil
	}

	// Create TAP device
	if err := exec.Command("ip", "tuntap", "add", tapName, "mode", "tap").Run(); err != nil {
		return fmt.Errorf("failed to create TAP device %s: %w", tapName, err)
	}

	// Attach TAP device to bridge
	if err := exec.Command("ip", "link", "set", "dev", tapName, "master", m.bridgeName).Run(); err != nil {
		return fmt.Errorf("failed to attach TAP device to bridge: %w", err)
	}

	// Bring TAP device up
	if err := exec.Command("ip", "link", "set", "dev", tapName, "up").Run(); err != nil {
		return fmt.Errorf("failed to bring TAP device up: %w", err)
	}

	m.logger.Debugf("Created and configured TAP device: %s", tapName)
	return nil
}

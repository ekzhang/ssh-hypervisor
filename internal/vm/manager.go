package vm

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ekzhang/ssh-hypervisor/internal"
	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"github.com/sirupsen/logrus"
)

// VM represents a single Firecracker microVM instance
type VM struct {
	ID         string
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
	config *internal.Config

	mutex  sync.RWMutex // Protects vms and vmRefs maps
	vms    map[string]*VM
	vmRefs map[string]int // Reference count for each VM

	ipPool     *IPPool
	bridgeName string
	logger     logrus.FieldLogger
}

// NewManager creates a new VM manager
func NewManager(config *internal.Config, logger logrus.FieldLogger, firecrackerBinary []byte, vmlinuxBinary []byte) (*Manager, error) {
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
		vmRefs:     make(map[string]int),
		ipPool:     ipPool,
		bridgeName: bridgeName,
		logger:     logger,
	}

	// Write Firecracker binary to main data directory (shared across VMs)
	firecrackerPath := filepath.Join(config.DataDir, "firecracker")
	if _, err := os.Stat(firecrackerPath); os.IsNotExist(err) {
		if err := os.WriteFile(firecrackerPath, firecrackerBinary, 0755); err != nil {
			return nil, fmt.Errorf("failed to write firecracker binary: %w", err)
		}
	}

	// Write vmlinux kernel to main data directory (shared across VMs)
	vmlinuxPath := filepath.Join(config.DataDir, "vmlinux")
	if _, err := os.Stat(vmlinuxPath); os.IsNotExist(err) {
		if err := os.WriteFile(vmlinuxPath, vmlinuxBinary, 0644); err != nil {
			return nil, fmt.Errorf("failed to write vmlinux kernel: %w", err)
		}
	}

	// Set up network bridge
	if err := manager.setupNetworkBridge(); err != nil {
		return nil, fmt.Errorf("failed to setup network bridge: %w", err)
	}

	return manager, nil
}

// GetOrCreateVM gets an existing VM or creates a new one if it doesn't exist
func (m *Manager) GetOrCreateVM(ctx context.Context, vmID string) (*VM, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Check if VM already exists and increment reference count
	if existingVM, exists := m.vms[vmID]; exists {
		m.vmRefs[vmID]++
		m.logger.Printf("Using existing VM %s (ref count: %d)", vmID, m.vmRefs[vmID])
		return existingVM, nil
	}

	// Check VM limit before creating new VM (0 = unlimited)
	if m.config.MaxConcurrentVMs > 0 && len(m.vms) >= m.config.MaxConcurrentVMs {
		return nil, fmt.Errorf("maximum number of concurrent VMs (%d) reached", m.config.MaxConcurrentVMs)
	}

	// Create new VM
	vm, err := m.createVMInternal(ctx, vmID)
	if err != nil {
		return nil, err
	}

	// Add to maps and set initial reference count
	m.vms[vmID] = vm
	m.vmRefs[vmID] = 1
	m.logger.Printf("Created new VM %s (ref count: 1)", vmID)

	return vm, nil
}

// createVMInternal creates and starts a new VM (internal method, assumes mutex is held)
func (m *Manager) createVMInternal(ctx context.Context, vmID string) (*VM, error) {
	// Validate VM ID, should be alphanumeric with - and _, not empty, and at most 48 chars
	if vmID == "" {
		return nil, fmt.Errorf("VM ID cannot be empty")
	}
	if strings.Trim(vmID, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
		return nil, fmt.Errorf("invalid VM ID: %s", vmID)
	}
	if len(vmID) > 48 {
		return nil, fmt.Errorf("VM ID too long: %s", vmID)
	}

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
		IP:         ip,
		Gateway:    m.ipPool.Gateway(),
		Netmask:    m.ipPool.Netmask(),
		SocketPath: filepath.Join(vmDataDir, "firecracker.sock"),
		PIDFile:    filepath.Join(vmDataDir, "firecracker.pid"),
		config:     m.config,
		dataDir:    vmDataDir,
		logger:     m.logger.WithField("vm_id", vmID),
	}

	// Copy the rootfs image to the VM data directory (writable)
	rootfsPath := filepath.Join(vmDataDir, "rootfs.img")
	if _, err := os.Stat(rootfsPath); os.IsNotExist(err) {
		buf, err := os.ReadFile(vm.config.Rootfs)
		if err == nil {
			err = os.WriteFile(rootfsPath, buf, 0644)
		}
		if err != nil {
			m.ipPool.Release(ip)
			os.RemoveAll(vmDataDir)
			return nil, fmt.Errorf("failed to copy rootfs image: %w", err)
		}
	}

	// Start the VM
	if err := vm.Start(ctx, m); err != nil {
		m.ipPool.Release(ip)
		os.RemoveAll(vmDataDir)
		return nil, fmt.Errorf("failed to start VM: %w", err)
	}

	return vm, nil
}

// GetVM returns the VM for a given user ID
func (m *Manager) GetVM(vmID string) (*VM, bool) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	vm, exists := m.vms[vmID]
	return vm, exists
}

// GetActiveVMCount returns the current number of active VMs
func (m *Manager) GetActiveVMCount() int {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return len(m.vms)
}

// ReleaseVM decrements the reference count for a VM and destroys it if no more references
func (m *Manager) ReleaseVM(vmID string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	vm, exists := m.vms[vmID]
	if !exists {
		return fmt.Errorf("VM %s not found", vmID)
	}

	// Decrement reference count
	m.vmRefs[vmID]--
	refCount := m.vmRefs[vmID]

	m.logger.Printf("Released VM %s (ref count: %d)", vmID, refCount)

	// Only destroy VM if no more references
	if refCount <= 0 {
		m.logger.Printf("Destroying VM %s (no more references)", vmID)

		if err := vm.Stop(); err != nil {
			return fmt.Errorf("failed to stop VM: %w", err)
		}

		m.ipPool.Release(vm.IP)
		delete(m.vms, vmID)
		delete(m.vmRefs, vmID)
	}

	return nil
}

// DestroyVM forcibly stops and removes a VM (for backward compatibility)
func (m *Manager) DestroyVM(vmID string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	vm, exists := m.vms[vmID]
	if !exists {
		return fmt.Errorf("VM %s not found", vmID)
	}

	m.logger.Printf("Forcibly destroying VM %s", vmID)

	if err := vm.Stop(); err != nil {
		return fmt.Errorf("failed to stop VM: %w", err)
	}

	m.ipPool.Release(vm.IP)
	delete(m.vms, vmID)
	delete(m.vmRefs, vmID)

	return nil
}

// Start starts the Firecracker process for this VM
func (vm *VM) Start(ctx context.Context, manager *Manager) error {
	// Remove existing socket, if any
	os.Remove(vm.SocketPath)

	vmlinuxPath := filepath.Join(vm.config.DataDir, "vmlinux")
	firecrackerPath := filepath.Join(vm.config.DataDir, "firecracker")

	bootArgs := "console=ttyS0 reboot=k panic=1 random.trust_cpu=on nomodules"

	// ip=IP::Gateway:Netmask:Hostname:Interface:off
	bootArgs += fmt.Sprintf(" ip=%s::%s:%s:%s:eth0:off", vm.IP, vm.Gateway, vm.Netmask, vm.ID)

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

	vm.logger.Infof("Starting VM with IP %s, TAP device %s, data dir %s", vm.IP, tapName, vm.dataDir)

	// Create a named pipe for VM serial input
	pipePath := filepath.Join(vm.dataDir, "console.in")
	// Remove existing pipe if it exists
	os.Remove(pipePath)
	if err := syscall.Mkfifo(pipePath, 0600); err != nil {
		return fmt.Errorf("mkfifo for console.in: %w", err)
	}
	pipeFile, err := os.OpenFile(pipePath, os.O_RDWR, os.ModeNamedPipe)
	if err != nil {
		return fmt.Errorf("open pipe for console.in: %v", err)
	}
	defer pipeFile.Close()

	// Capture VM console output (boot logs, OpenRC, SSH, etc.)
	logPath := filepath.Join(vm.dataDir, "console.out")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer logFile.Close()

	cmd.Stdin = pipeFile
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

	// Need to initialize virtio-rng (entropy) manually since not supported by SDK
	// https://github.com/firecracker-microvm/firecracker-go-sdk/issues/505
	machine.Handlers.FcInit = machine.Handlers.FcInit.Append(firecracker.Handler{
		Name: "virtio-rng",
		Fn: func(ctx context.Context, m *firecracker.Machine) error {
			tr := &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", m.Cfg.SocketPath)
				},
			}
			c := &http.Client{Transport: tr}
			defer c.CloseIdleConnections()

			body := strings.NewReader(`{"rate_limiter":{"bandwidth":{"size":4096,"one_time_burst":4096,"refill_time":100}}}`)
			req, _ := http.NewRequestWithContext(ctx, http.MethodPut, "http://unix/entropy", body)
			req.Header.Set("Content-Type", "application/json")
			resp, err := c.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				b, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("entropy PUT failed: %s: %s", resp.Status, string(b))
			}
			return nil
		},
	})

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
		vm.machine.Shutdown(ctx)

		// HACK: Give it a moment to shut down cleanly
		time.Sleep(250 * time.Millisecond)
		vm.machine.StopVMM()
		vm.machine.Wait(ctx)

		// Clean up only VM-specific files, preserve data and console output
		os.Remove(vm.SocketPath)                           // firecracker.sock
		os.Remove(vm.PIDFile)                              // firecracker.pid
		os.Remove(filepath.Join(vm.dataDir, "console.in")) // console.in

		vm.machine = nil
	}

	return nil
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
		// If TAP device exists, delete it
		m.logger.Debugf("TAP device %s already exists, deleting it", tapName)
		if err := exec.Command("ip", "link", "delete", tapName).Run(); err != nil {
			return fmt.Errorf("failed to delete existing TAP device %s: %w", tapName, err)
		}
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

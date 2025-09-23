package vm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ekzhang/ssh-hypervisor/internal"
	"github.com/sirupsen/logrus"
)

func TestNewManager(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "ssh-hypervisor-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	config := &internal.Config{
		VMCIDR:   "192.168.100.0/24",
		VMMemory: 128,
		VMCPUs:   1,
		DataDir:  tempDir,
	}

	manager, err := NewManager(config, logrus.NewEntry(logrus.StandardLogger()), []byte("fake firecracker"), []byte("fake vmlinux"))
	if err != nil {
		t.Fatalf("Failed to create VM manager: %v", err)
	}

	if manager == nil {
		t.Fatalf("VM manager is nil")
	}

	if manager.config != config {
		t.Errorf("VM manager config mismatch")
	}

	if manager.ipPool == nil {
		t.Errorf("VM manager IP pool is nil")
	}

	if len(manager.vms) != 0 {
		t.Errorf("Expected empty VM map, got %d VMs", len(manager.vms))
	}
}

func TestManagerInvalidCIDR(t *testing.T) {
	config := &internal.Config{
		VMCIDR:   "invalid-cidr",
		VMMemory: 128,
		VMCPUs:   1,
		DataDir:  "/tmp/ssh-hypervisor-test",
	}

	_, err := NewManager(config, logrus.NewEntry(logrus.StandardLogger()), []byte("fake firecracker"), []byte("fake vmlinux"))
	if err == nil {
		t.Errorf("Expected error with invalid CIDR")
	}
}

func TestVMCreationFlow(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "ssh-hypervisor-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a fake rootfs file
	rootfsPath := filepath.Join(tempDir, "rootfs.ext4")
	if err := os.WriteFile(rootfsPath, []byte("fake rootfs content"), 0644); err != nil {
		t.Fatalf("Failed to create fake rootfs: %v", err)
	}

	config := &internal.Config{
		VMCIDR:   "192.168.100.0/28",
		VMMemory: 128,
		VMCPUs:   1,
		DataDir:  tempDir,
		Rootfs:   rootfsPath,
	}

	manager, err := NewManager(config, logrus.NewEntry(logrus.StandardLogger()), []byte("fake firecracker"), []byte("fake vmlinux"))
	if err != nil {
		t.Fatalf("Failed to create VM manager: %v", err)
	}

	// Test VM creation (this will fail because we don't have a real Firecracker binary)
	vmID := "testuser"

	// This will fail at the firecracker execution step
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	vm, err := manager.GetOrCreateVM(ctx, vmID)
	// We expect this to fail since we're using a fake binary
	if err == nil {
		t.Errorf("Expected error with fake firecracker binary")
		if vm != nil {
			vm.Stop() // Clean up if somehow it worked
		}
	}

	// Since CreateVM cleans up on failure, the directory should not exist
	expectedVMDir := filepath.Join(tempDir, "vm-"+vmID)
	if _, err := os.Stat(expectedVMDir); !os.IsNotExist(err) {
		t.Errorf("Expected VM directory %s to be cleaned up after failure", expectedVMDir)
	}
}

func TestGetVM(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ssh-hypervisor-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	config := &internal.Config{
		VMCIDR:   "192.168.100.0/28",
		VMMemory: 128,
		VMCPUs:   1,
		DataDir:  tempDir,
	}

	manager, err := NewManager(config, logrus.NewEntry(logrus.StandardLogger()), []byte("fake firecracker"), []byte("fake vmlinux"))
	if err != nil {
		t.Fatalf("Failed to create VM manager: %v", err)
	}

	vmID := "testuser"

	// Test getting non-existent VM
	vm, exists := manager.GetVM(vmID)
	if exists {
		t.Errorf("Expected VM not to exist")
	}
	if vm != nil {
		t.Errorf("Expected nil VM for non-existent user")
	}

	// Add a VM manually to test retrieval
	testVM := &VM{
		ID: vmID,
	}
	manager.vms[vmID] = testVM

	// Test getting existing VM
	vm, exists = manager.GetVM(vmID)
	if !exists {
		t.Errorf("Expected VM to exist")
	}
	if vm != testVM {
		t.Errorf("Expected to get the same VM instance")
	}
}

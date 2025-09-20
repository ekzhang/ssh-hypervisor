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
	config := &internal.Config{
		VMCIDR:   "192.168.100.0/24",
		VMMemory: 128,
		VMCPUs:   1,
		DataDir:  "/tmp/ssh-hypervisor-test",
	}

	manager, err := NewManager(config, logrus.NewEntry(logrus.StandardLogger()))
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

	_, err := NewManager(config, logrus.NewEntry(logrus.StandardLogger()))
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

	config := &internal.Config{
		VMCIDR:   "192.168.100.0/28",
		VMMemory: 128,
		VMCPUs:   1,
		DataDir:  tempDir,
	}

	manager, err := NewManager(config, logrus.NewEntry(logrus.StandardLogger()))
	if err != nil {
		t.Fatalf("Failed to create VM manager: %v", err)
	}

	// Test VM creation (this will fail because we don't have a real Firecracker binary)
	// but we can test the setup logic
	userID := "testuser"
	fakeFirecrackerBinary := []byte("fake firecracker binary")
	fakeVmlinuxBinary := []byte("fake vmlinux kernel")

	// This will fail at the firecracker execution step, but we can verify setup
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	vm, err := manager.CreateVM(ctx, userID, fakeFirecrackerBinary, fakeVmlinuxBinary)
	// We expect this to fail since we're using a fake binary
	if err == nil {
		t.Errorf("Expected error with fake firecracker binary")
		if vm != nil {
			vm.Stop() // Clean up if somehow it worked
		}
	}

	// Verify that VM data directory was created
	expectedVMDir := filepath.Join(tempDir, "vm-"+userID)
	if _, err := os.Stat(expectedVMDir); os.IsNotExist(err) {
		t.Errorf("Expected VM directory %s to be created", expectedVMDir)
	}

	// Verify that firecracker binary was written
	firecrackerPath := filepath.Join(expectedVMDir, "firecracker")
	data, err := os.ReadFile(firecrackerPath)
	if err != nil {
		t.Errorf("Failed to read firecracker binary: %v", err)
	} else if string(data) != string(fakeFirecrackerBinary) {
		t.Errorf("Firecracker binary content mismatch")
	}

	// Check firecracker file permissions
	info, err := os.Stat(firecrackerPath)
	if err != nil {
		t.Errorf("Failed to stat firecracker binary: %v", err)
	} else if info.Mode().Perm() != 0755 {
		t.Errorf("Expected firecracker binary to have 755 permissions, got %o", info.Mode().Perm())
	}

	// Verify that vmlinux kernel was written
	vmlinuxPath := filepath.Join(expectedVMDir, "vmlinux")
	kernelData, err := os.ReadFile(vmlinuxPath)
	if err != nil {
		t.Errorf("Failed to read vmlinux kernel: %v", err)
	} else if string(kernelData) != string(fakeVmlinuxBinary) {
		t.Errorf("Vmlinux kernel content mismatch")
	}

	// Check vmlinux file permissions (should be 644, not executable)
	kernelInfo, err := os.Stat(vmlinuxPath)
	if err != nil {
		t.Errorf("Failed to stat vmlinux kernel: %v", err)
	} else if kernelInfo.Mode().Perm() != 0644 {
		t.Errorf("Expected vmlinux kernel to have 644 permissions, got %o", kernelInfo.Mode().Perm())
	}
}

func TestVMIDGeneration(t *testing.T) {
	testCases := []struct {
		userID   string
		expected string
	}{
		{"alice", "vm-alice"},
		{"bob", "vm-bob"},
		{"user-123", "vm-user-123"},
		{"", "vm-"},
	}

	for _, tc := range testCases {
		result := generateVMID(tc.userID)
		if result != tc.expected {
			t.Errorf("generateVMID(%s) = %s, expected %s", tc.userID, result, tc.expected)
		}
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

	manager, err := NewManager(config, logrus.NewEntry(logrus.StandardLogger()))
	if err != nil {
		t.Fatalf("Failed to create VM manager: %v", err)
	}

	userID := "testuser"

	// Test getting non-existent VM
	vm, exists := manager.GetVM(userID)
	if exists {
		t.Errorf("Expected VM not to exist")
	}
	if vm != nil {
		t.Errorf("Expected nil VM for non-existent user")
	}

	// Add a VM manually to test retrieval
	vmID := generateVMID(userID)
	testVM := &VM{
		ID:     vmID,
		UserID: userID,
	}
	manager.vms[vmID] = testVM

	// Test getting existing VM
	vm, exists = manager.GetVM(userID)
	if !exists {
		t.Errorf("Expected VM to exist")
	}
	if vm != testVM {
		t.Errorf("Expected to get the same VM instance")
	}
}

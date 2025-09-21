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

// TestVMIntegrationWithRealBinaries tests VM creation with real Firecracker and vmlinux binaries
// This test requires KVM support and will be skipped if /dev/kvm doesn't exist
func TestVMIntegrationWithRealBinaries(t *testing.T) {
	// Check if KVM is available
	if _, err := os.Stat("/dev/kvm"); os.IsNotExist(err) {
		t.Skip("Skipping integration test: /dev/kvm not available (KVM support required)")
	}

	// Load real binaries
	firecrackerBinary, err := os.ReadFile("binaries/firecracker")
	if err != nil {
		t.Skip("Skipping integration test: firecracker binary not found. Run 'go generate ./cmd/' first")
	}

	vmlinuxBinary, err := os.ReadFile("binaries/vmlinux")
	if err != nil {
		t.Skip("Skipping integration test: vmlinux binary not found. Run 'go generate ./cmd/' first")
	}

	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "ssh-hypervisor-integration-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a minimal rootfs file for testing (this won't actually boot)
	rootfsPath := filepath.Join(tempDir, "rootfs.ext4")
	if err := os.WriteFile(rootfsPath, []byte("minimal-rootfs"), 0644); err != nil {
		t.Fatalf("Failed to create test rootfs: %v", err)
	}

	config := &internal.Config{
		VMCIDR:   "192.168.100.0/28",
		VMMemory: 128,
		VMCPUs:   1,
		DataDir:  tempDir,
		Rootfs:   rootfsPath,
	}

	manager, err := NewManager(config, logrus.NewEntry(logrus.StandardLogger()))
	if err != nil {
		t.Fatalf("Failed to create VM manager: %v", err)
	}

	userID := "integration-test-user"

	// Test VM creation with real binaries
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	vm, err := manager.CreateVM(ctx, userID, firecrackerBinary, vmlinuxBinary)
	if err != nil {
		t.Fatalf("VM creation failed with minimal test setup: %v", err)
	}

	// If VM creation actually succeeded, clean it up
	if vm != nil {
		t.Logf("VM creation succeeded! VM ID: %s, IP: %s", vm.ID, vm.IP.String())

		// Verify VM properties
		if vm.ID != "vm-"+userID {
			t.Errorf("Unexpected VM ID: got %s, expected %s", vm.ID, "vm-"+userID)
		}

		if vm.IP == nil {
			t.Errorf("VM IP is nil")
		}

		// Stop the VM
		if err := vm.Stop(); err != nil {
			t.Errorf("Failed to stop VM: %v", err)
		}
	}
}

// TestFirecrackerBinaryVerification tests that the embedded binaries are valid
func TestFirecrackerBinaryVerification(t *testing.T) {
	// Load real firecracker binary
	firecrackerBinary, err := os.ReadFile("binaries/firecracker")
	if err != nil {
		t.Skip("Skipping binary verification test: firecracker binary not found. Run 'go generate ./cmd/' first")
	}

	if len(firecrackerBinary) == 0 {
		t.Error("Firecracker binary is empty")
	}

	// Check if it looks like an ELF binary (starts with ELF magic)
	if len(firecrackerBinary) < 4 || string(firecrackerBinary[:4]) != "\x7fELF" {
		t.Error("Firecracker binary doesn't appear to be a valid ELF file")
	}

	t.Logf("Firecracker binary size: %d bytes", len(firecrackerBinary))
}

func TestVmlinuxBinaryVerification(t *testing.T) {
	// Load real vmlinux binary
	vmlinuxBinary, err := os.ReadFile("binaries/vmlinux")
	if err != nil {
		t.Skip("Skipping binary verification test: vmlinux binary not found. Run 'go generate ./cmd/' first")
	}

	if len(vmlinuxBinary) == 0 {
		t.Error("vmlinux binary is empty")
	}

	// Check if it looks like an ELF binary (starts with ELF magic)
	if len(vmlinuxBinary) < 4 || string(vmlinuxBinary[:4]) != "\x7fELF" {
		t.Error("vmlinux binary doesn't appear to be a valid ELF file")
	}

	t.Logf("vmlinux binary size: %d bytes", len(vmlinuxBinary))
}

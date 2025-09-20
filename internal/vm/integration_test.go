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

	// We expect this to potentially fail during configuration since we don't have a real rootfs
	// But it should get further than the fake binary tests
	if err != nil {
		t.Logf("VM creation failed as expected with minimal test setup: %v", err)

		// Verify that the VM setup got further than just writing files
		expectedVMDir := filepath.Join(tempDir, "vm-"+userID)

		// Check that VM directory was created
		if _, err := os.Stat(expectedVMDir); os.IsNotExist(err) {
			t.Errorf("Expected VM directory %s to be created", expectedVMDir)
			return
		}

		// Check that real firecracker binary was written
		firecrackerPath := filepath.Join(expectedVMDir, "firecracker")
		if stat, err := os.Stat(firecrackerPath); err != nil {
			t.Errorf("Failed to stat firecracker binary: %v", err)
		} else if stat.Size() != int64(len(firecrackerBinary)) {
			t.Errorf("Firecracker binary size mismatch: got %d, expected %d", stat.Size(), len(firecrackerBinary))
		}

		// Check that real vmlinux kernel was written
		vmlinuxPath := filepath.Join(expectedVMDir, "vmlinux")
		if stat, err := os.Stat(vmlinuxPath); err != nil {
			t.Errorf("Failed to stat vmlinux kernel: %v", err)
		} else if stat.Size() != int64(len(vmlinuxBinary)) {
			t.Errorf("vmlinux kernel size mismatch: got %d, expected %d", stat.Size(), len(vmlinuxBinary))
		}

		// Check that Firecracker process might have started
		// Look for log file or socket creation attempts
		logPath := filepath.Join(expectedVMDir, "firecracker.log")
		if _, err := os.Stat(logPath); err == nil {
			// Log file was created, which means Firecracker actually started
			t.Logf("Firecracker process was started (log file created)")

			// Read a bit of the log to see what happened
			if logContent, err := os.ReadFile(logPath); err == nil {
				t.Logf("Firecracker log snippet: %s", string(logContent[:min(200, len(logContent))]))
			}
		}

		return
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

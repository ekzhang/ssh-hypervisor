//go:build linux && (amd64 || arm64)

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ekzhang/ssh-hypervisor/internal"
	// "github.com/ekzhang/ssh-hypervisor/internal/server"
	"github.com/ekzhang/ssh-hypervisor/internal/vm"
	"github.com/sirupsen/logrus"
)

var log *logrus.Logger = logrus.StandardLogger()

// Version string, can be overriden at build time with -ldflags.
var version = "dev"

func getVersion() string {
	return version
}

func main() {
	var (
		port     = flag.Int("port", 2222, "SSH server port")
		hostKey  = flag.String("host-key", "", "Path to SSH host key (generated if not provided)")
		vmCIDR   = flag.String("vm-cidr", "192.168.100.0/24", "CIDR block for VM IP addresses")
		vmMemory = flag.Int("vm-memory", 128, "VM memory in MB")
		vmCPUs   = flag.Int("vm-cpus", 1, "Number of VM CPUs")
		dataDir  = flag.String("data-dir", "./data", "Directory for VM snapshots and data")
		rootfs   = flag.String("rootfs", "", "Path to rootfs image (required)")
		version  = flag.Bool("version", false, "Show version information")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "ssh-hypervisor - SSH server that dynamically provisions Linux microVMs\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *version {
		fmt.Printf("ssh-hypervisor %s\n", getVersion())
		return
	}

	config := &internal.Config{
		Port:     *port,
		HostKey:  *hostKey,
		VMCIDR:   *vmCIDR,
		VMMemory: *vmMemory,
		VMCPUs:   *vmCPUs,
		DataDir:  *dataDir,
		Rootfs:   *rootfs,
	}

	if err := config.Validate(); err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	// Original server code (commented out for testing)
	// srv, err := server.NewServer(config, logrus.NewEntry(log))
	// if err != nil {
	// 	log.Fatalf("Failed to create server: %v", err)
	// }
	//
	// log.Printf("Starting ssh-hypervisor on port %d", config.Port)
	// log.Printf("VM network: %s", config.VMCIDR)
	// log.Printf("Data directory: %s", config.DataDir)
	//
	// if err := srv.Run(); err != nil {
	// 	log.Fatalf("Server error: %v", err)
	// }

	// Temporary: Create VM manager and single VM for testing
	manager, err := vm.NewManager(config, log)
	if err != nil {
		log.Fatalf("Failed to create VM manager: %v", err)
	}

	// Create context for VM lifecycle
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Printf("Creating Firecracker VM...")
	log.Printf("VM network: %s", config.VMCIDR)
	log.Printf("Data directory: %s", config.DataDir)

	// Create a single VM
	testVM, err := manager.CreateVM(ctx, "test-user", vm.GetFirecrackerBinary(), vm.GetVmlinuxBinary())
	if err != nil {
		log.Fatalf("Failed to create VM: %v", err)
	}

	log.Printf("VM created successfully!")
	log.Printf("VM ID: %s", testVM.ID)
	log.Printf("VM IP: %s", testVM.IP)
	log.Printf("VM Gateway: %s", testVM.Gateway)
	log.Printf("VM Netmask: %s", testVM.Netmask)

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("VM is running. Press Ctrl+C to shutdown gracefully...")

	// Wait for shutdown signal
	<-sigChan
	log.Printf("Received shutdown signal, stopping VM...")

	// Gracefully shutdown VM
	if err := manager.DestroyVM(testVM.ID); err != nil {
		log.Errorf("Error stopping VM: %v", err)
	} else {
		log.Printf("VM stopped successfully")
	}
}

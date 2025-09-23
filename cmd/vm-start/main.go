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
	"github.com/ekzhang/ssh-hypervisor/internal/vm"
	"github.com/sirupsen/logrus"
)

var log *logrus.Logger = logrus.StandardLogger()

var version = "dev"

func getVersion() string {
	return version
}

func main() {
	var (
		dataDir = flag.String("data-dir", "./data", "Directory for VM snapshots and data")
		rootfs  = flag.String("rootfs", "", "Path to rootfs image (required)")
		version = flag.Bool("version", false, "Show version information")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "vm-start - Start a single VM for testing\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *version {
		fmt.Printf("vm-start %s\n", getVersion())
		return
	}

	config := &internal.Config{
		Port:     2222,
		HostKey:  "",
		VMCIDR:   "192.168.100.0/24",
		VMMemory: 128,
		VMCPUs:   1,
		DataDir:  *dataDir,
		Rootfs:   *rootfs,
	}

	if err := config.Validate(); err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	manager, err := vm.NewManager(config, log, vm.GetFirecrackerBinary(), vm.GetVmlinuxBinary())
	if err != nil {
		log.Fatalf("Failed to create VM manager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Printf("Creating Firecracker VM...")
	log.Printf("VM network: %s", config.VMCIDR)
	log.Printf("Data directory: %s", config.DataDir)

	testVM, err := manager.GetOrCreateVM(ctx, "test-user")
	if err != nil {
		log.Fatalf("Failed to create VM: %v", err)
	}

	log.Printf("VM created successfully!")
	log.Printf("VM ID: %s", testVM.ID)
	log.Printf("VM IP: %s", testVM.IP)
	log.Printf("VM Gateway: %s", testVM.Gateway)
	log.Printf("VM Netmask: %s", testVM.Netmask)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("VM is running. Press Ctrl+C to shutdown gracefully...")

	<-sigChan
	log.Printf("Received shutdown signal, stopping VM...")

	if err := manager.DestroyVM(testVM.ID); err != nil {
		log.Errorf("Error stopping VM: %v", err)
	} else {
		log.Printf("VM stopped successfully")
	}
}

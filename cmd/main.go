//go:build linux && (amd64 || arm64)

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ekzhang/ssh-hypervisor/internal"
	"github.com/ekzhang/ssh-hypervisor/internal/server"
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

	srv, err := server.NewServer(config, logrus.NewEntry(log))
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	log.Printf("Starting ssh-hypervisor on port %d", config.Port)
	log.Printf("VM network: %s", config.VMCIDR)
	log.Printf("Data directory: %s", config.DataDir)

	if err := srv.Run(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

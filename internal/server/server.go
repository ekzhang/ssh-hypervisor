package server

import (
	"fmt"
	"log"

	"github.com/ekzhang/ssh-hypervisor/internal"
	"github.com/ekzhang/ssh-hypervisor/internal/vm"
	"github.com/sirupsen/logrus"
)

// Server represents the SSH hypervisor server
type Server struct {
	config    *internal.Config
	vmManager *vm.Manager
}

// NewServer creates a new SSH hypervisor server
func NewServer(config *internal.Config, logger logrus.FieldLogger) (*Server, error) {
	vmManager, err := vm.NewManager(config, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create VM manager: %w", err)
	}

	return &Server{
		config:    config,
		vmManager: vmManager,
	}, nil
}

// Run starts the SSH server
func (s *Server) Run() error {
	log.Printf("Server configuration:")
	log.Printf("  Port: %d", s.config.Port)
	log.Printf("  Host key: %s", s.config.HostKey)
	log.Printf("  VM CIDR: %s", s.config.VMCIDR)
	log.Printf("  VM Memory: %d MB", s.config.VMMemory)
	log.Printf("  VM CPUs: %d", s.config.VMCPUs)
	log.Printf("  Data directory: %s", s.config.DataDir)

	log.Printf("VM manager initialized successfully")

	// TODO: Initialize Wish SSH server
	// TODO: Set up networking (TAP devices)
	// TODO: Integrate VM provisioning with SSH connections

	return fmt.Errorf("server implementation not yet complete")
}

package server

import (
	"fmt"
	"log"

	"github.com/ekzhang/ssh-hypervisor/internal"
)

// Server represents the SSH hypervisor server
type Server struct {
	config *internal.Config
}

// NewServer creates a new SSH hypervisor server
func NewServer(config *internal.Config) (*Server, error) {
	return &Server{
		config: config,
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

	// TODO: Initialize Wish SSH server
	// TODO: Set up VM management
	// TODO: Set up networking (TAP devices)

	return fmt.Errorf("server implementation not yet complete")
}
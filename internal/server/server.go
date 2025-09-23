package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/ekzhang/ssh-hypervisor/internal"
	"github.com/ekzhang/ssh-hypervisor/internal/vm"
	"github.com/olekukonko/tablewriter"
	"github.com/sirupsen/logrus"
	cryptoSSH "golang.org/x/crypto/ssh"
)

const maxProgressBlocks = 40

// Server represents the SSH hypervisor server
type Server struct {
	config    *internal.Config
	vmManager *vm.Manager
	userStats *UserStats
	logger    logrus.FieldLogger
}

// NewServer creates a new SSH hypervisor server
func NewServer(config *internal.Config, logger logrus.FieldLogger) (*Server, error) {
	vmManager, err := vm.NewManager(config, logger, vm.GetFirecrackerBinary(), vm.GetVmlinuxBinary())
	if err != nil {
		return nil, fmt.Errorf("failed to create VM manager: %w", err)
	}

	userStats := NewUserStats(config.DataDir)
	if err := userStats.Load(); err != nil {
		logger.Errorf("Failed to load user stats: %v", err)
		// Continue anyway with empty stats
	}

	return &Server{
		config:    config,
		vmManager: vmManager,
		userStats: userStats,
		logger:    logger,
	}, nil
}

// Run starts the SSH server
func (s *Server) Run(ctx context.Context) error {
	s.logger.Printf("Server configuration:")
	s.logger.Printf("  Port: %d", s.config.Port)
	s.logger.Printf("  Host key: %s", s.config.HostKey)
	s.logger.Printf("  VM CIDR: %s", s.config.VMCIDR)
	s.logger.Printf("  VM Memory: %d MB", s.config.VMMemory)
	s.logger.Printf("  VM CPUs: %d", s.config.VMCPUs)
	s.logger.Printf("  Data directory: %s", s.config.DataDir)

	hostKey, err := s.loadOrGenerateHostKey()
	if err != nil {
		return fmt.Errorf("failed to load/generate host key: %w", err)
	}

	server := ssh.Server{
		Addr:        fmt.Sprintf(":%d", s.config.Port),
		Handler:     s.sshHandler,
		HostSigners: []ssh.Signer{hostKey},
		PublicKeyHandler: func(ctx ssh.Context, key ssh.PublicKey) bool {
			return true // Accept any public key
		},
		PasswordHandler: func(ctx ssh.Context, password string) bool {
			return true // Accept any password
		},
	}

	s.logger.Printf("Starting SSH server on port %d", s.config.Port)

	// Start server in goroutine
	done := make(chan error, 1)
	go func() {
		done <- server.ListenAndServe()
	}()

	// Wait for context cancellation or server error
	select {
	case <-ctx.Done():
		s.logger.Printf("Shutting down SSH server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("error during shutdown: %w", err)
		}

		// Save user stats before shutdown
		if err := s.userStats.Save(); err != nil {
			s.logger.Errorf("Failed to save user stats: %v", err)
		} else {
			s.logger.Printf("User stats saved successfully")
		}

		s.logger.Printf("SSH server shut down gracefully")
		return nil
	case err := <-done:
		// Save user stats on unexpected shutdown too
		if saveErr := s.userStats.Save(); saveErr != nil {
			s.logger.Errorf("Failed to save user stats: %v", saveErr)
		}

		if err != nil && err != ssh.ErrServerClosed {
			return fmt.Errorf("SSH server error: %w", err)
		}
		return nil
	}
}

// loadOrGenerateHostKey loads an existing host key or generates a new one
func (s *Server) loadOrGenerateHostKey() (ssh.Signer, error) {
	var keyPath string

	if s.config.HostKey != "" {
		keyPath = s.config.HostKey
	} else {
		// Generate default key path in data directory
		if err := os.MkdirAll(s.config.DataDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create data directory: %w", err)
		}
		keyPath = filepath.Join(s.config.DataDir, "ssh_host_ed25519_key")
	}

	// Try to load existing key
	if _, err := os.Stat(keyPath); err == nil {
		keyBytes, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read host key: %w", err)
		}

		signer, err := cryptoSSH.ParsePrivateKey(keyBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse host key: %w", err)
		}

		s.logger.Printf("Loaded existing host key from %s", keyPath)
		return signer, nil
	}

	// Generate new key
	s.logger.Printf("Generating new host key at %s", keyPath)

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate host key: %w", err)
	}

	// Convert to SSH format and save
	signer, err := cryptoSSH.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create signer: %w", err)
	}

	// Save private key
	privateKeyPEM, err := cryptoSSH.MarshalPrivateKey(privateKey, "")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal private key: %w", err)
	}

	privateKeyBytes := pem.EncodeToMemory(privateKeyPEM)
	if err := os.WriteFile(keyPath, privateKeyBytes, 0600); err != nil {
		return nil, fmt.Errorf("failed to write host key: %w", err)
	}

	s.logger.Printf("Generated new host key at %s", keyPath)
	return signer, nil
}

// sshHandler handles incoming SSH connections
func (s *Server) sshHandler(sess ssh.Session) {
	user := sess.User()
	remoteAddr := sess.RemoteAddr()

	s.logger.Printf("SSH connection from %s (user: %s)", remoteAddr, user)
	s.userStats.RecordConnection(user)

	// Show animated progress bar while creating VM
	ctx, cancel := context.WithCancel(sess.Context())
	defer cancel()

	// Check if VM already exists before getting/creating
	_, vmExists := s.vmManager.GetVM(user)

	// Show welcome message with appropriate VM status
	s.showWelcomeMessage(sess, user, !vmExists)

	// Start VM creation in background
	vmDone := make(chan *vm.VM, 1)
	vmErr := make(chan error, 1)
	go func() {
		testVM, err := s.vmManager.GetOrCreateVM(ctx, user)
		if err != nil {
			vmErr <- err
		} else {
			vmDone <- testVM
		}
	}()

	// Show animated progress bar with health check in a separate goroutine
	vmReady := make(chan string, 1)
	progressDone := make(chan struct{})
	vmCreateFailed := make(chan struct{})
	go func() {
		defer close(progressDone)
		s.showProgressBarWithHealthCheck(sess, ctx, vmReady, vmCreateFailed)
	}()

	// Wait for VM creation to complete or context cancellation
	var testVM *vm.VM
	select {
	case testVM = <-vmDone:
		// VM created successfully, start health check
		go func() {
			vmAddr := fmt.Sprintf("%s:22", testVM.IP.String())
			if s.waitForVMSSH(ctx, vmAddr) == nil {
				select {
				case vmReady <- testVM.IP.String():
				default:
				}
			}
		}()

		// Wait for progress bar to complete
		<-progressDone
	case err := <-vmErr:
		// Signal progress bar that VM creation failed
		close(vmCreateFailed)
		// Wait for progress bar to complete before showing error
		<-progressDone
		s.logger.Errorf("Failed to create VM for user %s: %v", user, err)

		// Show user-friendly error message
		errorMsg := err.Error()
		if strings.Contains(errorMsg, "maximum number of concurrent VMs") {
			wish.Println(sess, fmt.Sprintf("\n\033[31mServer is at capacity! Maximum of %d concurrent VMs are allowed.\033[0m", s.config.MaxConcurrentVMs))
			wish.Println(sess, "\033[31mPlease try again later when some VMs are freed up.\033[0m")
		} else {
			wish.Println(sess, fmt.Sprintf("\n\033[31mFailed to provision VM: %v\033[0m", err))
		}
		return
	case <-sess.Context().Done():
		// Session was cancelled (Ctrl+C), wait for progress bar to clean up
		<-progressDone
		s.logger.Printf("SSH session cancelled for user %s during VM creation", user)
		return
	}

	defer func() {
		if err := s.vmManager.ReleaseVM(testVM.ID); err != nil {
			s.logger.Errorf("Error releasing VM %s: %v", testVM.ID, err)
		}
	}()

	s.logger.Printf("Created VM %s for user %s (IP: %s)", testVM.ID, user, testVM.IP)

	// Clear progress line and show success
	wish.Print(sess, "\r\033[2K")
	completeBars := strings.Repeat("▮", maxProgressBlocks)
	wish.Println(sess, fmt.Sprintf("\033[32m%s\033[0m 100%%  🧨 \033[32mComplete!\033[0m", completeBars))
	wish.Println(sess, "")

	// Start SSH proxy to VM
	if err := s.proxySSHToVM(sess, testVM.IP.String()); err != nil {
		s.logger.Errorf("SSH proxy error for user %s: %v", user, err)
		wish.Println(sess, fmt.Sprintf("\033[31mConnection to VM failed: %v\033[0m", err))
	}

	s.logger.Printf("SSH session ended for user %s, destroying VM %s", user, testVM.ID)
}

// showWelcomeMessage displays the welcome message with user stats
func (s *Server) showWelcomeMessage(sess ssh.Session, user string, isNewVM bool) {
	now := time.Now()
	dayOfWeek := now.Weekday().String()

	wish.Println(sess, fmt.Sprintf("\n\033[1;35mHello, %s! 🌸\033[0m", user))
	wish.Println(sess, "")

	// Check if this is the user's first time
	isFirstTime := s.userStats.IsFirstTime(user)
	if isFirstTime {
		wish.Println(sess, fmt.Sprintf("Today is \033[3m%s\033[0m. It's your first time here.", dayOfWeek))
	} else {
		userStat, _ := s.userStats.GetUserStat(user)
		lastLogin := formatRelativeTime(userStat.LastConnected)
		wish.Println(sess, fmt.Sprintf("Today is \033[3m%s\033[0m. Your last login was \033[3m%s\033[0m.", dayOfWeek, lastLogin))
	}

	wish.Println(sess, "")

	// Show recent logins table
	recentUsers := s.userStats.GetRecentUsers(user, 10)
	if len(recentUsers) > 0 {
		wish.Println(sess, "\033[2;37mRecent logins:\033[0m")

		var buf bytes.Buffer
		table := tablewriter.NewTable(&buf,
			tablewriter.WithHeader([]string{"User", "Last login"}),
		)
		for _, userStat := range recentUsers {
			lastLogin := formatRelativeTime(userStat.LastConnected)
			table.Append([]string{userStat.Username, lastLogin})
		}

		table.Render()
		wish.Print(sess, buf.String())
	} else {
		wish.Println(sess, "You're the first user to connect! 🎉")
	}

	wish.Println(sess, "")
	if isNewVM {
		wish.Println(sess, "\033[2;37mBooting your fresh VM...\033[0m")
	} else {
		wish.Println(sess, "\033[2;37mConnecting to VM...\033[0m")
	}
}

// formatRelativeTime formats a time as a human-readable relative time
func formatRelativeTime(t time.Time) string {
	now := time.Now()
	diff := now.Sub(t)

	if diff < time.Minute {
		return "just now"
	} else if diff < time.Hour {
		minutes := int(diff.Minutes())
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	} else if diff < 24*time.Hour {
		hours := int(diff.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	} else {
		days := int(diff.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// showProgressBarWithHealthCheck displays an animated exponential progress bar
func (s *Server) showProgressBarWithHealthCheck(sess ssh.Session, ctx context.Context, vmReady <-chan string, vmCreateFailed <-chan struct{}) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	startTime := time.Now()
	completed := false

	// Ensure clean exit on context cancellation
	defer func() {
		if ctx.Err() != nil || sess.Context().Err() != nil {
			// Clear progress line if cancelled
			wish.Print(sess, "\r\033[2K")
			wish.Println(sess, "\n\033[33mCancelled during VM provisioning.\033[0m")
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sess.Context().Done():
			// Session context cancelled (Ctrl+C)
			return
		case <-vmCreateFailed:
			// VM creation failed, clear progress line and return
			wish.Print(sess, "\r\033[2K")
			return
		case <-vmReady:
			// VM is ready, jump to 100%
			if !completed {
				completed = true
				bar := strings.Repeat("▮", maxProgressBlocks)
				wish.Print(sess, fmt.Sprintf("\r\033[36m%s\033[0m 100%%", bar))
				return
			}
		case <-ticker.C:
			if completed {
				return
			}

			// Check for cancellation before updating display
			select {
			case <-ctx.Done():
				return
			case <-sess.Context().Done():
				return
			case <-vmCreateFailed:
				// VM creation failed, clear progress line and return
				wish.Print(sess, "\r\033[2K")
				return
			default:
			}

			// Exponential progress: fast at start, slower at end
			// Using exponential decay formula: 1 - e^(-k*t)
			elapsed := time.Since(startTime).Seconds()
			progress := int(100 * (1 - math.Exp(-1.2*elapsed)))

			// Cap at 99% until VM is actually ready
			if progress > 99 {
				progress = 99
			}

			// Calculate filled blocks
			filled := (progress * maxProgressBlocks) / 100
			if filled > maxProgressBlocks {
				filled = maxProgressBlocks
			}

			// Build progress bar
			bar := strings.Repeat("▮", filled) + strings.Repeat("▯", maxProgressBlocks-filled)

			// Update progress line
			wish.Print(sess, fmt.Sprintf("\r\033[36m%s\033[0m %d%%", bar, progress))
		}
	}
}

// proxySSHToVM establishes a transparent SSH proxy to the VM
func (s *Server) proxySSHToVM(sess ssh.Session, vmIP string) error {
	// Wait for VM SSH service to be ready (with timeout)
	vmAddr := fmt.Sprintf("%s:22", vmIP)
	if err := s.waitForVMSSH(sess.Context(), vmAddr); err != nil {
		return fmt.Errorf("VM SSH service not ready: %w", err)
	}

	// Create SSH client connection to VM
	config := &cryptoSSH.ClientConfig{
		User: "root", // VMs run as root by default
		Auth: []cryptoSSH.AuthMethod{
			cryptoSSH.Password(""), // Empty password for now
			cryptoSSH.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
				// Accept any keyboard interactive challenge
				answers := make([]string, len(questions))
				return answers, nil
			}),
		},
		HostKeyCallback: cryptoSSH.InsecureIgnoreHostKey(), // Skip host key verification for VMs
		Timeout:         10 * time.Second,
	}

	// Connect to VM SSH server
	vmClient, err := cryptoSSH.Dial("tcp", vmAddr, config)
	if err != nil {
		return fmt.Errorf("failed to connect to VM SSH: %w", err)
	}
	defer vmClient.Close()

	// Create a session on the VM
	vmSession, err := vmClient.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create VM session: %w", err)
	}
	defer vmSession.Close()

	// Set up pipes between the client session and VM session
	vmSession.Stdin = sess
	vmSession.Stdout = sess
	vmSession.Stderr = sess.Stderr()

	// Forward environment variables
	for _, env := range sess.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			vmSession.Setenv(parts[0], parts[1])
		}
	}

	// Handle terminal requests
	pty, winCh, isPty := sess.Pty()
	if isPty {
		if err := vmSession.RequestPty(pty.Term, pty.Window.Height, pty.Window.Width, cryptoSSH.TerminalModes{}); err != nil {
			return fmt.Errorf("failed to request pty: %w", err)
		}

		// Handle window size changes
		go func() {
			for win := range winCh {
				vmSession.WindowChange(win.Height, win.Width)
			}
		}()
	}

	// Start shell on VM
	if err := vmSession.Shell(); err != nil {
		return fmt.Errorf("failed to start shell: %w", err)
	}

	// Wait for either session to end or context cancellation
	done := make(chan error, 1)
	go func() {
		done <- vmSession.Wait()
	}()

	select {
	case err := <-done:
		// VM session ended normally
		return err
	case <-sess.Context().Done():
		// Client session was cancelled (Ctrl+C)
		vmSession.Close()
		return sess.Context().Err()
	}
}

// waitForVMSSH waits for the VM's SSH service to become available
func (s *Server) waitForVMSSH(ctx context.Context, vmAddr string) error {
	timeout := time.After(15 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timeout waiting for VM SSH service")
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", vmAddr, 1*time.Second)
			if err == nil {
				conn.Close()
				s.logger.Printf("VM SSH service is ready at %s", vmAddr)
				return nil
			}
		}
	}
}

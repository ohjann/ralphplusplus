package memory

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Sidecar manages a ChromaDB subprocess lifecycle.
type Sidecar struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	port     int
	logFile  *os.File
	reused   bool // true if we attached to an existing instance
}

// Start launches ChromaDB as a subprocess (or reuses an existing instance on the target port).
// It waits up to 15 seconds for the server to become healthy.
func (s *Sidecar) Start(ctx context.Context, pythonPath string, dataDir string, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if port == 0 {
		port = 9876
	}
	s.port = port

	// Check if ChromaDB is already running on the target port.
	if isHealthyFunc(port) {
		s.reused = true
		return nil
	}

	chromaDir := filepath.Join(dataDir, "chroma")
	if err := os.MkdirAll(chromaDir, 0o755); err != nil {
		return fmt.Errorf("creating chroma data dir: %w", err)
	}

	// Open log file for stdout/stderr capture.
	logPath := filepath.Join(dataDir, "chroma.log")
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening chroma log file: %w", err)
	}
	s.logFile = lf

	s.cmd = exec.CommandContext(ctx,
		pythonPath, "-m", "chromadb.cli.cli", "run",
		"--path", chromaDir,
		"--port", fmt.Sprintf("%d", port),
	)
	s.cmd.Stdout = lf
	s.cmd.Stderr = lf
	// Start in its own process group so we can signal it cleanly.
	s.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := s.cmd.Start(); err != nil {
		lf.Close()
		s.logFile = nil
		return fmt.Errorf("starting chromadb process: %w", err)
	}

	// Wait for ChromaDB to become healthy (up to 15 seconds, poll every 500ms).
	deadline := time.Now().Add(15 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.stopLocked()
			return ctx.Err()
		case <-ticker.C:
			if isHealthyFunc(port) {
				return nil
			}
			if time.Now().After(deadline) {
				s.stopLocked()
				return fmt.Errorf("chromadb did not become healthy within 15 seconds (port %d)", port)
			}
			// Check if process exited early.
			if s.cmd.ProcessState != nil {
				s.stopLocked()
				return fmt.Errorf("chromadb process exited prematurely (port %d)", port)
			}
		}
	}
}

// Stop sends SIGTERM, waits up to 5 seconds, then SIGKILL if needed.
func (s *Sidecar) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopLocked()
}

func (s *Sidecar) stopLocked() error {
	if s.reused {
		// We didn't start it; don't stop it.
		s.reused = false
		return nil
	}

	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}

	// Send SIGTERM.
	_ = s.cmd.Process.Signal(syscall.SIGTERM)

	// Wait up to 5 seconds for clean exit.
	done := make(chan error, 1)
	go func() {
		done <- s.cmd.Wait()
	}()

	select {
	case <-done:
		// Process exited cleanly.
	case <-time.After(5 * time.Second):
		// Force kill.
		_ = s.cmd.Process.Kill()
		<-done
	}

	s.cmd = nil

	if s.logFile != nil {
		s.logFile.Close()
		s.logFile = nil
	}

	return nil
}

// IsRunning checks if the managed subprocess is still alive.
func (s *Sidecar) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.reused {
		return isHealthyFunc(s.port)
	}

	if s.cmd == nil || s.cmd.Process == nil {
		return false
	}
	// ProcessState is set after Wait returns; if nil, process is still running.
	return s.cmd.ProcessState == nil
}

// Port returns the port the sidecar is running on.
func (s *Sidecar) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// isHealthyFunc is a package-level variable for testability.
var isHealthyFunc = isHealthy

// isHealthy checks if ChromaDB is responding on the given port.
func isHealthy(port int) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/api/v1/heartbeat", port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

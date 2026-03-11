package memory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// saveSidecarFuncs saves the current function variables and returns a restore function.
func saveSidecarFuncs() func() {
	orig := isHealthyFunc
	return func() {
		isHealthyFunc = orig
	}
}

func TestStart_DetectsRunningInstance(t *testing.T) {
	defer saveSidecarFuncs()()

	// Create a mock heartbeat server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/heartbeat" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	// Extract port from test server URL.
	parts := strings.Split(srv.URL, ":")
	port, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		t.Fatalf("failed to parse port from %s: %v", srv.URL, err)
	}

	// Point isHealthyFunc at our test server.
	isHealthyFunc = func(p int) bool {
		if p != port {
			return false
		}
		resp, err := http.Get(srv.URL + "/api/v1/heartbeat")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}

	s := &Sidecar{}
	err = s.Start(context.Background(), "/fake/python", t.TempDir(), port)
	if err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}

	if !s.reused {
		t.Error("expected sidecar to detect existing instance (reused=true)")
	}

	// Cleanup should be a no-op since we reused.
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop() returned error: %v", err)
	}
}

func TestStop_AlreadyStopped(t *testing.T) {
	defer saveSidecarFuncs()()

	s := &Sidecar{}

	// Stop on a sidecar that was never started should not error.
	err := s.Stop()
	if err != nil {
		t.Fatalf("Stop() on never-started sidecar returned error: %v", err)
	}

	// Double-stop should also be fine.
	err = s.Stop()
	if err != nil {
		t.Fatalf("second Stop() returned error: %v", err)
	}
}

func TestIsRunning_FalseAfterStop(t *testing.T) {
	defer saveSidecarFuncs()()

	s := &Sidecar{}

	// Never started — should not be running.
	if s.IsRunning() {
		t.Error("IsRunning() should return false for never-started sidecar")
	}

	// Simulate a reused instance that is now stopped.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	parts := strings.Split(srv.URL, ":")
	port, _ := strconv.Atoi(parts[len(parts)-1])

	isHealthyFunc = func(p int) bool {
		if p != port {
			return false
		}
		resp, err := http.Get(srv.URL + "/api/v1/heartbeat")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}

	s2 := &Sidecar{}
	err := s2.Start(context.Background(), "/fake/python", t.TempDir(), port)
	if err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}

	if !s2.IsRunning() {
		t.Error("IsRunning() should return true for reused running instance")
	}

	// Stop the sidecar (clears reused flag).
	if err := s2.Stop(); err != nil {
		t.Fatalf("Stop() returned error: %v", err)
	}

	if s2.IsRunning() {
		t.Error("IsRunning() should return false after Stop()")
	}
}

func TestPort_ReturnsConfiguredPort(t *testing.T) {
	defer saveSidecarFuncs()()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	parts := strings.Split(srv.URL, ":")
	port, _ := strconv.Atoi(parts[len(parts)-1])

	isHealthyFunc = func(p int) bool {
		resp, err := http.Get(srv.URL + "/api/v1/heartbeat")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}

	s := &Sidecar{}
	if err := s.Start(context.Background(), "/fake/python", t.TempDir(), port); err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}
	defer s.Stop()

	if s.Port() != port {
		t.Errorf("Port() = %d, want %d", s.Port(), port)
	}
}

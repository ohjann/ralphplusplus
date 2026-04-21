package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/ohjann/ralphplusplus/internal/viewer"
)

// runViewer implements `ralph viewer [--port N] [--no-open]`. Follows the
// install-skill dispatch pattern: it runs before config.Parse so it needs no
// prd.json and works in any cwd. Defaults: --port 0 (OS-chosen), --no-open
// false (auto-open the URL in the user's default browser).
func runViewer(args []string) error {
	fs := flag.NewFlagSet("viewer", flag.ContinueOnError)
	port := fs.Int("port", 0, "TCP port to bind on 127.0.0.1 (0 = OS-chosen)")
	noOpen := fs.Bool("no-open", false, "Don't auto-open the URL in a browser")
	if err := fs.Parse(args); err != nil {
		return err
	}

	lockF, existing, err := viewer.Acquire()
	if err != nil {
		return fmt.Errorf("viewer lock: %w", err)
	}
	if existing != nil {
		token, tokErr := viewer.LoadOrCreateToken()
		if tokErr != nil {
			return fmt.Errorf("read token: %w", tokErr)
		}
		url := fmt.Sprintf("http://127.0.0.1:%d/?token=%s", existing.Port, token)
		printAlreadyRunning(url, !*noOpen)
		if !*noOpen {
			_ = openBrowser(url)
		}
		return nil
	}
	defer viewer.Release(lockF)

	token, err := viewer.LoadOrCreateToken()
	if err != nil {
		return fmt.Errorf("viewer token: %w", err)
	}

	// Server lifetime controls the projects.Index fsnotify watcher.
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()

	vs, err := viewer.NewServer(serverCtx, token, Version)
	if err != nil {
		return fmt.Errorf("viewer server: %w", err)
	}
	static, err := viewer.StaticHandler(vs.Handler())
	if err != nil {
		return fmt.Errorf("static handler: %w", err)
	}
	handler := viewer.LoopbackOnly(static)

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return fmt.Errorf("listener returned non-TCP addr %T", ln.Addr())
	}
	actualPort := tcpAddr.Port

	if err := viewer.Write(lockF, viewer.LockInfo{
		PID:       os.Getpid(),
		Port:      actualPort,
		StartedAt: time.Now(),
	}); err != nil {
		_ = ln.Close()
		return fmt.Errorf("write lockfile: %w", err)
	}

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/?token=%s", actualPort, token)
	printStartedBanner(url, !*noOpen)
	if !*noOpen {
		_ = openBrowser(url)
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		close(errCh)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigCh:
	case err := <-errCh:
		if err != nil {
			return err
		}
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	return nil
}

// printStartedBanner writes the post-start info. The URL is on its own line
// of stdout so existing tooling that scrapes the first line (the historical
// contract) keeps working. The friendly banner goes to stderr.
func printStartedBanner(url string, willOpen bool) {
	fmt.Println(url)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  Ralph Viewer is up.")
	if willOpen {
		fmt.Fprintln(os.Stderr, "  Opening in your default browser…  (use --no-open to skip)")
	} else {
		fmt.Fprintln(os.Stderr, "  Open the URL above in your browser to log in.")
	}
	fmt.Fprintln(os.Stderr, "  Press Ctrl+C to stop.")
	fmt.Fprintln(os.Stderr)
}

func printAlreadyRunning(url string, willOpen bool) {
	fmt.Println(url)
	fmt.Fprintln(os.Stderr)
	if willOpen {
		fmt.Fprintln(os.Stderr, "  Ralph Viewer is already running. Reopening in your default browser.")
		fmt.Fprintln(os.Stderr, "  (Use --no-open to suppress.)")
	} else {
		fmt.Fprintln(os.Stderr, "  Ralph Viewer is already running. Open the URL above in your browser.")
	}
	fmt.Fprintln(os.Stderr)
}

// openBrowser launches the OS's default URL handler. Errors are surfaced to
// the caller (usually swallowed — failure to auto-open should never block
// the viewer from starting).
func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd = "xdg-open"
		args = []string{url}
	}
	return exec.Command(cmd, args...).Start()
}

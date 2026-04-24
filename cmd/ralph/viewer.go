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

	"github.com/ohjann/ralphplusplus/internal/history"
	"github.com/ohjann/ralphplusplus/internal/userdata"
	"github.com/ohjann/ralphplusplus/internal/viewer"
)

// runViewer implements `ralph viewer [--port N] [--no-open] [--tailscale]
// [--tailscale-hostname H] [--tailscale-port N]`. Follows the install-skill
// dispatch pattern: it runs before config.Parse so it needs no prd.json and
// works in any cwd.
//
// Listeners:
//   - Loopback (always): 127.0.0.1:<--port> with token gate. The terminal
//     URL keeps existing behaviour for local browsers.
//   - Tailnet (when --tailscale): joins the user's tailnet via tsnet and
//     binds the same handler at http://<--tailscale-hostname>/. Tailscale's
//     handshake is the auth boundary — no token, no Host header gymnastics
//     on phones. First launch prompts an interactive Tailscale login.
//
// The two listeners share one viewer.Server (so SSE clients on either path
// see the same projects index, the same daemon proxies, etc.). On startup,
// the best reachable URL is written to <userdata>/viewer-url so the daemon's
// notifier can deep-link push notifications back to the UI.
func runViewer(args []string) error {
	fs := flag.NewFlagSet("viewer", flag.ContinueOnError)
	port := fs.Int("port", 0, "TCP port to bind on 127.0.0.1 (0 = OS-chosen)")
	noOpen := fs.Bool("no-open", false, "Don't auto-open the URL in a browser")
	useTailscale := fs.Bool("tailscale", false, "Also expose the viewer to your tailnet via tsnet (interactive login on first run)")
	tsHostname := fs.String("tailscale-hostname", "ralph", "Tailnet hostname when --tailscale is set")
	tsPort := fs.Int("tailscale-port", 80, "Tailnet port when --tailscale is set")
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

	// Flip any orphaned "running" manifests from dead daemons on this host
	// to "interrupted" so the UI doesn't lie about their status. Safe to run
	// on every viewer start — it only touches manifests whose stamped PID is
	// no longer alive; live daemons are untouched.
	if sweepErr := history.SweepInterrupted(); sweepErr != nil {
		fmt.Fprintf(os.Stderr, "warn: sweep interrupted manifests: %v\n", sweepErr)
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

	// Loopback front-door: token + loopback Host check (current behaviour).
	loopbackHandler := viewer.LoopbackOnly(static)
	loopbackAddr := fmt.Sprintf("127.0.0.1:%d", *port)
	loopbackLn, err := net.Listen("tcp", loopbackAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", loopbackAddr, err)
	}
	tcpAddr, ok := loopbackLn.Addr().(*net.TCPAddr)
	if !ok {
		_ = loopbackLn.Close()
		return fmt.Errorf("listener returned non-TCP addr %T", loopbackLn.Addr())
	}
	actualPort := tcpAddr.Port
	loopbackURL := fmt.Sprintf("http://127.0.0.1:%d/?token=%s", actualPort, token)

	if err := viewer.Write(lockF, viewer.LockInfo{
		PID:       os.Getpid(),
		Port:      actualPort,
		StartedAt: time.Now(),
	}); err != nil {
		_ = loopbackLn.Close()
		return fmt.Errorf("write lockfile: %w", err)
	}

	loopbackSrv := &http.Server{
		Handler:           loopbackHandler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Tailnet front-door (optional). Tsnet may take a moment on first launch
	// (interactive login); we run its bring-up synchronously so the printed
	// banner reflects the real reachable URL — printing a tailnet URL that
	// wasn't ready yet would mislead users.
	var (
		tsnetSrv  *http.Server
		tsnetLn   net.Listener
		tnet      *viewer.Tailnet
		tailURL   string
		tailErr   error
	)
	if *useTailscale {
		fmt.Fprintf(os.Stderr, "  Starting tailnet node %q…\n", *tsHostname)
		tnet, tailErr = viewer.NewTailnet(serverCtx, *tsHostname, os.Stderr)
		if tailErr != nil {
			fmt.Fprintf(os.Stderr, "  tailnet: %v (continuing without remote access)\n", tailErr)
		} else {
			tailAddr := fmt.Sprintf(":%d", *tsPort)
			tsnetLn, tailErr = tnet.Listen(tailAddr)
			if tailErr != nil {
				fmt.Fprintf(os.Stderr, "  tailnet listen %s: %v\n", tailAddr, tailErr)
				_ = tnet.Close()
				tnet = nil
			} else {
				tsnetSrv = &http.Server{
					Handler:           tnet.TrustHandler(static),
					ReadHeaderTimeout: 10 * time.Second,
				}
				if *tsPort == 80 {
					tailURL = fmt.Sprintf("http://%s/", *tsHostname)
				} else {
					tailURL = fmt.Sprintf("http://%s:%d/", *tsHostname, *tsPort)
				}
			}
		}
	}

	// Now that tailnet bring-up is settled, expose the tailnet URL to the
	// viewer's /api/integrations probe so the UI reflects reality.
	vs.TailscaleURL = tailURL

	// The hint file feeds the daemon's notifier so push notifications open
	// the right URL on tap. Tailnet URL preferred — it works on phones and
	// has no embedded token to leak through ntfy.sh transit.
	bestURL := loopbackURL
	if tailURL != "" {
		bestURL = tailURL
	}
	if writeErr := userdata.WriteViewerURL(bestURL); writeErr != nil {
		fmt.Fprintf(os.Stderr, "warn: write viewer-url hint: %v\n", writeErr)
	}
	defer func() { _ = userdata.RemoveViewerURL() }()

	printStartedBanner(loopbackURL, tailURL, !*noOpen)
	if !*noOpen {
		_ = openBrowser(loopbackURL)
	}

	errCh := make(chan error, 2)
	go func() {
		if err := loopbackSrv.Serve(loopbackLn); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	if tsnetSrv != nil {
		go func() {
			if err := tsnetSrv.Serve(tsnetLn); err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("tailnet serve: %w", err)
			}
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigCh:
	case err := <-errCh:
		if err != nil {
			if tnet != nil {
				_ = tnet.Close()
			}
			return err
		}
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = loopbackSrv.Shutdown(shutCtx)
	if tsnetSrv != nil {
		_ = tsnetSrv.Shutdown(shutCtx)
	}
	if tnet != nil {
		_ = tnet.Close()
	}
	return nil
}

// printStartedBanner writes the post-start info. The loopback URL stays on
// stdout's first line (the historical contract scraped by tooling). The
// friendly banner — including the tailnet URL when --tailscale is on —
// goes to stderr.
func printStartedBanner(loopbackURL, tailURL string, willOpen bool) {
	fmt.Println(loopbackURL)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  Ralph Viewer is up.")
	if tailURL != "" {
		fmt.Fprintf(os.Stderr, "  Tailnet:  %s   (no token, peers only)\n", tailURL)
		fmt.Fprintf(os.Stderr, "  Local:    %s\n", loopbackURL)
	}
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

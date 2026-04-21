package viewer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ohjann/ralphplusplus/internal/history"
	"github.com/ohjann/ralphplusplus/internal/userdata"
)

// SpawnResult is the outcome of a successful spawn. FP routes the new run in
// the viewer index; PID is the detached process id. RunID is populated only
// by paths that poll for a freshly-written manifest (retro) and is omitted
// for the daemon path, which returns before any run has been opened.
type SpawnResult struct {
	FP    string `json:"fp"`
	PID   int    `json:"pid"`
	RunID string `json:"runId,omitempty"`
}

// Spawner errors. Each represents a distinct HTTP status: ErrInvalidPath →
// 400 (body is malformed or the path does not resolve to an existing
// directory), ErrPathConfirmation → 409 with a {warn, resolved} body on the
// first POST when the resolved path is outside $HOME and the caller has not
// yet sent {confirm:true}, ErrDaemonAlreadyRunning → 409 when the target
// repo already has a live daemon, ErrRetroAlreadyRunning → 409 when a retro
// is already running for the same repo fingerprint.
var (
	ErrInvalidPath          = errors.New("invalid_path")
	ErrPathConfirmation     = errors.New("path_confirmation_required")
	ErrDaemonAlreadyRunning = errors.New("daemon_already_running")
	ErrRetroAlreadyRunning  = errors.New("retro_already_running")
)

// AllowedFlags is the whitelist of spawn-form flags the viewer accepts. Any
// other key in the request body's `flags` object returns 400. Enforced as a
// hard whitelist because CLI flag misspellings are the most common
// footgun; the path check is deliberately soft ($HOME warning, not a hard
// wall) because the viewer is already loopback + token-gated.
var AllowedFlags = map[string]struct{}{
	"Workers":        {},
	"WorkersAuto":    {},
	"AutoMaxWorkers": {},
	"JudgeEnabled":   {},
	"QualityReview":  {},
	"ModelOverride":  {},
}

// SpawnRequest is the body of POST /api/spawn-daemon. Flags are a shape-free
// map so the SPA can add new whitelisted entries without a Go-side schema
// change; any unknown key is rejected before the spawn actually happens.
type SpawnRequest struct {
	RepoPath string                 `json:"repoPath"`
	Flags    map[string]interface{} `json:"flags,omitempty"`
	Confirm  bool                   `json:"confirm,omitempty"`
}

// ResolvePath runs filepath.Abs then EvalSymlinks on in, returning the
// resolved path and a bool for whether it lives under $HOME. Any error in
// either step collapses to ErrInvalidPath so the caller does not have to
// branch on stat vs. symlink-loop vs. nonexistent. The resolved path must
// also be an existing directory.
func ResolvePath(in string) (resolved string, insideHome bool, err error) {
	if in == "" {
		return "", false, ErrInvalidPath
	}
	abs, aerr := filepath.Abs(in)
	if aerr != nil {
		return "", false, ErrInvalidPath
	}
	r, serr := filepath.EvalSymlinks(abs)
	if serr != nil {
		return "", false, ErrInvalidPath
	}
	st, stErr := os.Stat(r)
	if stErr != nil || !st.IsDir() {
		return "", false, ErrInvalidPath
	}

	home, herr := os.UserHomeDir()
	if herr != nil || home == "" {
		return r, false, nil
	}
	rel, relErr := filepath.Rel(home, r)
	if relErr != nil {
		return r, false, nil
	}
	inside := rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
	return r, inside, nil
}

// BuildDaemonArgs renders the whitelisted flags map into CLI args. The
// --daemon and --dir args are always prepended so the spawner does not
// depend on the caller remembering them.
func BuildDaemonArgs(repoPath string, flags map[string]interface{}) ([]string, error) {
	for k := range flags {
		if _, ok := AllowedFlags[k]; !ok {
			return nil, fmt.Errorf("flag_not_allowed: %s", k)
		}
	}
	args := []string{"--daemon", "--dir", repoPath}
	// Workers: "auto" (string) or positive int
	if v, ok := flags["Workers"]; ok {
		switch t := v.(type) {
		case string:
			if t == "auto" {
				args = append(args, "--workers", "auto")
			} else {
				n, err := strconv.Atoi(t)
				if err != nil || n < 1 {
					return nil, fmt.Errorf("Workers: must be a positive integer or \"auto\"")
				}
				args = append(args, "--workers", strconv.Itoa(n))
			}
		case float64:
			n := int(t)
			if n < 1 {
				return nil, fmt.Errorf("Workers: must be >= 1")
			}
			args = append(args, "--workers", strconv.Itoa(n))
		default:
			return nil, fmt.Errorf("Workers: must be a positive integer or \"auto\"")
		}
	}
	if v, ok := flags["WorkersAuto"]; ok {
		if b, okb := v.(bool); okb && b {
			args = append(args, "--workers", "auto")
		}
	}
	if v, ok := flags["AutoMaxWorkers"]; ok {
		// --auto-max-workers is not a real CLI flag; auto scaling caps default
		// at 5 inside the daemon. We accept the field to stay forward-compatible
		// with the planned settings API but currently no-op when the config
		// does not expose a CLI surface for it. Validate the type only.
		switch t := v.(type) {
		case float64:
			if int(t) < 1 {
				return nil, fmt.Errorf("AutoMaxWorkers: must be >= 1")
			}
		case string:
			if _, err := strconv.Atoi(t); err != nil {
				return nil, fmt.Errorf("AutoMaxWorkers: must be an integer")
			}
		default:
			return nil, fmt.Errorf("AutoMaxWorkers: must be an integer")
		}
	}
	if v, ok := flags["JudgeEnabled"]; ok {
		b, okb := v.(bool)
		if !okb {
			return nil, fmt.Errorf("JudgeEnabled: must be a bool")
		}
		if !b {
			args = append(args, "--no-judge")
		}
	}
	if v, ok := flags["QualityReview"]; ok {
		b, okb := v.(bool)
		if !okb {
			return nil, fmt.Errorf("QualityReview: must be a bool")
		}
		if !b {
			args = append(args, "--no-quality-review")
		}
	}
	if v, ok := flags["ModelOverride"]; ok {
		s, oks := v.(string)
		if !oks {
			return nil, fmt.Errorf("ModelOverride: must be a string")
		}
		if s != "" {
			args = append(args, "--model", s)
		}
	}
	return args, nil
}

// daemonReachable probes the unix socket at <repoPath>/.ralph/daemon.sock.
// Returns true when the daemon responds 200 on /api/state — the same check
// cmd/ralph uses. Missing-socket, dial-refused, or stat errors all collapse
// to false so the spawn path can decide to proceed.
func daemonReachable(repoPath string) bool {
	sock := filepath.Join(repoPath, ".ralph", "daemon.sock")
	if _, err := os.Stat(sock); err != nil {
		return false
	}
	client := &http.Client{
		Timeout: 500 * time.Millisecond,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: 500 * time.Millisecond}
				return d.DialContext(ctx, "unix", sock)
			},
			DisableKeepAlives: true,
		},
	}
	resp, err := client.Get("http://daemon/api/state")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// SpawnDaemon starts `ralph --daemon --dir <repoPath> <flags...>` detached
// from the viewer's process group, with stdout/stderr going to
// <repoPath>/.ralph/daemon.log. Returns {fp, pid} on success.
func SpawnDaemon(ctx context.Context, repoPath string, flags map[string]interface{}) (SpawnResult, error) {
	if daemonReachable(repoPath) {
		return SpawnResult{}, ErrDaemonAlreadyRunning
	}

	args, err := BuildDaemonArgs(repoPath, flags)
	if err != nil {
		return SpawnResult{}, err
	}

	pid, err := spawnDetached(repoPath, args, "daemon.log")
	if err != nil {
		return SpawnResult{}, err
	}

	fp, ferr := userdata.Fingerprint(repoPath)
	if ferr != nil {
		return SpawnResult{}, fmt.Errorf("fingerprint: %w", ferr)
	}

	return SpawnResult{FP: fp, PID: pid}, nil
}

// SpawnRetro starts `ralph retro --dir <repoPath>` detached, writing
// stdout/stderr to <repoPath>/.ralph/retro.log. Unlike SpawnDaemon it does
// not probe the daemon socket (a retro can run alongside a live daemon) but
// it does refuse to spawn if another retro is already running for the same
// repo (checked via the run manifest). Returns {fp, pid}; the caller is
// responsible for polling for the new manifest to fill in RunID.
func SpawnRetro(ctx context.Context, repoPath string) (SpawnResult, error) {
	fp, ferr := userdata.Fingerprint(repoPath)
	if ferr != nil {
		return SpawnResult{}, fmt.Errorf("fingerprint: %w", ferr)
	}

	if _, running, err := isRetroRunning(fp); err != nil {
		return SpawnResult{}, fmt.Errorf("check running retro: %w", err)
	} else if running {
		return SpawnResult{}, ErrRetroAlreadyRunning
	}

	// The retro CLI takes no user flags from the viewer — the fixed positional
	// + --dir argument list is the whitelist.
	args := []string{"retro", "--dir", repoPath}
	pid, err := spawnDetached(repoPath, args, "retro.log")
	if err != nil {
		return SpawnResult{}, err
	}
	return SpawnResult{FP: fp, PID: pid}, nil
}

// spawnDetached execs the current ralph binary with args, detached via
// Setsid from the viewer's process group, with stdout/stderr redirected to
// <repoPath>/.ralph/<logName>. The spawned process is reaped in a
// background goroutine so it does not become a zombie if it exits before
// its side-effects (socket, manifest) appear.
func spawnDetached(repoPath string, args []string, logName string) (int, error) {
	exePath, err := ralphExecutable()
	if err != nil {
		return 0, err
	}

	dotRalph := filepath.Join(repoPath, ".ralph")
	if err := os.MkdirAll(dotRalph, 0o755); err != nil {
		return 0, fmt.Errorf("ensure .ralph dir: %w", err)
	}

	logPath := filepath.Join(dotRalph, logName)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open log: %w", err)
	}

	cmd := exec.Command(exePath, args...)
	cmd.Dir = repoPath
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return 0, fmt.Errorf("start process: %w", err)
	}
	pid := cmd.Process.Pid

	go func() {
		_ = cmd.Wait()
		logFile.Close()
	}()

	return pid, nil
}

// isRetroRunning returns (runID, true, nil) when a retro manifest with
// Status=="running" exists for fp. A missing runs dir or no retro manifests
// returns ("", false, nil) — this is the expected state before the first
// retro. Only filesystem errors propagate as err.
func isRetroRunning(fp string) (string, bool, error) {
	manifests, err := history.LoadManifestsForRepo(fp)
	if err != nil {
		return "", false, err
	}
	for _, m := range manifests {
		if m.Kind == history.KindRetro && m.Status == history.StatusRunning {
			return m.RunID, true, nil
		}
	}
	return "", false, nil
}

// retroRunIDSet reads the current retro run ids for fp so the caller can
// spot a newly-written manifest afterwards. Errors are silently turned into
// an empty set — the caller only uses this to filter the post-spawn poll,
// not to gate the spawn.
func retroRunIDSet(fp string) map[string]struct{} {
	out := map[string]struct{}{}
	manifests, err := history.LoadManifestsForRepo(fp)
	if err != nil {
		return out
	}
	for _, m := range manifests {
		if m.Kind == history.KindRetro {
			out[m.RunID] = struct{}{}
		}
	}
	return out
}

// waitForNewRetroRun polls LoadManifestsForRepo every ~100ms until it finds
// a retro manifest whose RunID is absent from known (the pre-spawn
// snapshot). Returns "" on timeout or ctx.Done — the caller treats this as
// "manifest didn't appear yet" and returns the pid-only response.
func waitForNewRetroRun(ctx context.Context, fp string, known map[string]struct{}, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for {
		manifests, err := history.LoadManifestsForRepo(fp)
		if err == nil {
			for _, m := range manifests {
				if m.Kind != history.KindRetro {
					continue
				}
				if _, seen := known[m.RunID]; !seen {
					return m.RunID
				}
			}
		}
		if time.Now().After(deadline) {
			return ""
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// ralphExecutable resolves the path to the currently running binary so the
// spawned daemon is the same version as the viewer. os.Executable returns
// the symlink target on Linux, which is what exec.Command expects.
func ralphExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	return exe, nil
}

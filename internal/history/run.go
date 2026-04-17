package history

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/userdata"
)

// ManifestSchemaVersion is the on-disk format version for manifest.json.
const ManifestSchemaVersion = 1

// Run kinds.
const (
	KindDaemon            = "daemon"
	KindRetro             = "retro"
	KindMemoryConsolidate = "memory-consolidate"
	KindAdHoc             = "ad-hoc"
)

// Run statuses.
const (
	StatusRunning     = "running"
	StatusComplete    = "complete"
	StatusInterrupted = "interrupted"
	StatusFailed      = "failed"
)

// Totals accumulates per-run cost metrics.
type Totals struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CacheRead    int `json:"cache_read"`
	CacheWrite   int `json:"cache_write"`
	Iterations   int `json:"iterations"`
}

// IterationRecord describes a single role×iteration Claude invocation.
type IterationRecord struct {
	Index          int               `json:"index"`
	Role           string            `json:"role"`
	Model          string            `json:"model,omitempty"`
	SessionID      string            `json:"session_id,omitempty"`
	StartTime      time.Time         `json:"start_time"`
	EndTime        *time.Time        `json:"end_time,omitempty"`
	PromptFile     string            `json:"prompt_file"`
	TranscriptFile string            `json:"transcript_file"`
	MetaFile       string            `json:"meta_file"`
	TokenUsage     *costs.TokenUsage `json:"token_usage,omitempty"`
	Error          string            `json:"error,omitempty"`
}

// StoryRecord aggregates iterations for one story in a run.
type StoryRecord struct {
	StoryID     string            `json:"story_id"`
	Title       string            `json:"title,omitempty"`
	Iterations  []IterationRecord `json:"iterations,omitempty"`
	FinalStatus string            `json:"final_status,omitempty"`
}

// Manifest is the root document persisted at <runDir>/manifest.json.
type Manifest struct {
	SchemaVersion int               `json:"schema_version"`
	RunID         string            `json:"run_id"`
	Kind          string            `json:"kind"`
	RepoFP        string            `json:"repo_fp"`
	RepoPath      string            `json:"repo_path"`
	RepoName      string            `json:"repo_name"`
	GitBranch     string            `json:"git_branch,omitempty"`
	GitHeadSHA    string            `json:"git_head_sha,omitempty"`
	PRDPath       string            `json:"prd_path,omitempty"`
	PRDSnapshot   string            `json:"prd_snapshot,omitempty"`
	RalphVersion  string            `json:"ralph_version"`
	ClaudeModels  map[string]string `json:"claude_models,omitempty"`
	Flags         []string          `json:"flags,omitempty"`
	PID           int               `json:"pid"`
	Hostname      string            `json:"hostname"`
	ProcessStart  time.Time         `json:"process_start"`
	StartTime     time.Time         `json:"start_time"`
	EndTime       *time.Time        `json:"end_time,omitempty"`
	Status        string            `json:"status"`
	Stories       []StoryRecord     `json:"stories,omitempty"`
	Totals        Totals            `json:"totals"`
}

// RunOpts carries invocation-specific context for OpenRun.
type RunOpts struct {
	// Kind is one of the Kind* constants. Empty defaults to "daemon".
	Kind         string
	ClaudeModels map[string]string
	Flags        []string
}

// Run owns a live run directory and its manifest. Methods are
// concurrency-safe: StartIteration may be called from multiple goroutines.
type Run struct {
	id       string
	dir      string
	manifest Manifest

	mu sync.Mutex
}

// OpenRun touches the repo, stamps the process identity, creates the run dir,
// and writes the initial manifest with Status="running".
func OpenRun(projectDir, prdFile, version string, opts RunOpts) (*Run, error) {
	if projectDir == "" {
		return nil, errors.New("OpenRun: projectDir is empty")
	}
	kind := opts.Kind
	if kind == "" {
		kind = KindDaemon
	}

	fp, meta, err := TouchRepo(projectDir)
	if err != nil {
		return nil, fmt.Errorf("touch repo: %w", err)
	}

	id, err := newRunID()
	if err != nil {
		return nil, fmt.Errorf("new run id: %w", err)
	}

	dir, err := runDirFor(fp, id)
	if err != nil {
		return nil, err
	}
	if err := userdata.EnsureDirs(dir); err != nil {
		return nil, fmt.Errorf("ensure run dir: %w", err)
	}
	if err := userdata.EnsureDirs(filepath.Join(dir, "turns")); err != nil {
		return nil, fmt.Errorf("ensure turns dir: %w", err)
	}

	hostname, _ := os.Hostname()
	now := time.Now().UTC()

	branch, head := gitState(projectDir)

	m := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		RunID:         id,
		Kind:          kind,
		RepoFP:        fp,
		RepoPath:      meta.Path,
		RepoName:      meta.Name,
		GitBranch:     branch,
		GitHeadSHA:    head,
		PRDPath:       prdFile,
		PRDSnapshot:   prdSnapshot(prdFile),
		RalphVersion:  version,
		ClaudeModels:  opts.ClaudeModels,
		Flags:         opts.Flags,
		PID:           os.Getpid(),
		Hostname:      hostname,
		ProcessStart:  now,
		StartTime:     now,
		Status:        StatusRunning,
	}

	r := &Run{id: id, dir: dir, manifest: m}
	if err := r.writeManifest(); err != nil {
		return nil, err
	}
	return r, nil
}

// ID returns the run identifier.
func (r *Run) ID() string { return r.id }

// Dir returns the absolute path to the run directory.
func (r *Run) Dir() string { return r.dir }

// UpdateStoryList seeds the manifest.Stories slice with the provided entries.
// Existing iterations for matching StoryIDs are preserved.
func (r *Run) UpdateStoryList(stories []StoryRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing := make(map[string]StoryRecord, len(r.manifest.Stories))
	for _, s := range r.manifest.Stories {
		existing[s.StoryID] = s
	}
	merged := make([]StoryRecord, 0, len(stories))
	for _, s := range stories {
		if prev, ok := existing[s.StoryID]; ok {
			s.Iterations = prev.Iterations
			if s.FinalStatus == "" {
				s.FinalStatus = prev.FinalStatus
			}
		}
		merged = append(merged, s)
	}
	r.manifest.Stories = merged
	return r.writeManifestLocked()
}

// SetStoryFinal records a terminal status for the given story.
func (r *Run) SetStoryFinal(storyID, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := r.findStory(storyID)
	if idx < 0 {
		r.manifest.Stories = append(r.manifest.Stories, StoryRecord{
			StoryID:     storyID,
			FinalStatus: status,
		})
	} else {
		r.manifest.Stories[idx].FinalStatus = status
	}
	return r.writeManifestLocked()
}

// StartIteration allocates an IterationWriter for a (story, role, iter) tuple
// and stamps the iteration start in the manifest.
func (r *Run) StartIteration(storyID, role string, iter int, model string) (*IterationWriter, error) {
	if storyID == "" || role == "" {
		return nil, fmt.Errorf("StartIteration: storyID and role required")
	}

	turnDir := filepath.Join(r.dir, "turns", storyID)
	if err := userdata.EnsureDirs(turnDir); err != nil {
		return nil, fmt.Errorf("ensure turn dir: %w", err)
	}
	stem := fmt.Sprintf("%s-iter-%d", role, iter)
	rec := IterationRecord{
		Index:          iter,
		Role:           role,
		Model:          model,
		StartTime:      time.Now().UTC(),
		PromptFile:     filepath.Join(turnDir, stem+".prompt"),
		TranscriptFile: filepath.Join(turnDir, stem+".jsonl"),
		MetaFile:       filepath.Join(turnDir, stem+".meta.json"),
	}

	r.mu.Lock()
	sIdx := r.findStory(storyID)
	if sIdx < 0 {
		r.manifest.Stories = append(r.manifest.Stories, StoryRecord{StoryID: storyID})
		sIdx = len(r.manifest.Stories) - 1
	}
	r.manifest.Stories[sIdx].Iterations = append(r.manifest.Stories[sIdx].Iterations, rec)
	iIdx := len(r.manifest.Stories[sIdx].Iterations) - 1
	writeErr := r.writeManifestLocked()
	r.mu.Unlock()
	if writeErr != nil {
		return nil, writeErr
	}

	if err := writeIterMeta(rec); err != nil {
		return nil, err
	}

	return &IterationWriter{
		run:     r,
		storyID: storyID,
		sIdx:    sIdx,
		iIdx:    iIdx,
		rec:     rec,
	}, nil
}

// Finalize stamps EndTime/totals and flips the manifest status. It is safe to
// call multiple times — only the first call with a non-running status sticks.
//
// When summary is non-nil, the run-history.json entry is appended in the same
// critical section as the manifest write, with RunID and Kind sourced from the
// manifest so the two records cannot diverge.
func (r *Run) Finalize(status string, totals Totals, summary *costs.RunSummary) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.manifest.Status != StatusRunning {
		return nil
	}
	now := time.Now().UTC()
	r.manifest.EndTime = &now
	r.manifest.Status = status
	r.manifest.Totals = totals
	if err := r.writeManifestLocked(); err != nil {
		return err
	}
	if summary != nil {
		summary.RunID = r.manifest.RunID
		summary.Kind = r.manifest.Kind
		if err := costs.AppendRun(r.manifest.RepoFP, *summary); err != nil {
			return fmt.Errorf("append run history: %w", err)
		}
	}
	return nil
}

// ComputeTotals derives per-run cost totals from the recorded iterations. It
// is safe to call repeatedly; Finalize uses the returned value to stamp the
// manifest when no external accounting is available.
func (r *Run) ComputeTotals() Totals {
	r.mu.Lock()
	defer r.mu.Unlock()
	var t Totals
	for _, s := range r.manifest.Stories {
		for _, it := range s.Iterations {
			t.Iterations++
			if it.TokenUsage == nil {
				continue
			}
			t.InputTokens += it.TokenUsage.InputTokens
			t.OutputTokens += it.TokenUsage.OutputTokens
			t.CacheRead += it.TokenUsage.CacheRead
			t.CacheWrite += it.TokenUsage.CacheWrite
		}
	}
	return t
}

func (r *Run) findStory(storyID string) int {
	for i, s := range r.manifest.Stories {
		if s.StoryID == storyID {
			return i
		}
	}
	return -1
}

func (r *Run) writeManifest() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writeManifestLocked()
}

func (r *Run) writeManifestLocked() error {
	data, err := json.MarshalIndent(r.manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	return writeAtomicFn(filepath.Join(r.dir, "manifest.json"), data)
}

// updateIteration mutates the matching iteration record under lock and
// rewrites the manifest. It returns the mutated record so callers can mirror
// it to the per-iteration meta file.
func (r *Run) updateIteration(sIdx, iIdx int, fn func(*IterationRecord)) (IterationRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sIdx < 0 || sIdx >= len(r.manifest.Stories) {
		return IterationRecord{}, fmt.Errorf("story index out of range")
	}
	if iIdx < 0 || iIdx >= len(r.manifest.Stories[sIdx].Iterations) {
		return IterationRecord{}, fmt.Errorf("iteration index out of range")
	}
	fn(&r.manifest.Stories[sIdx].Iterations[iIdx])
	rec := r.manifest.Stories[sIdx].Iterations[iIdx]
	if err := r.writeManifestLocked(); err != nil {
		return rec, err
	}
	return rec, nil
}

// IterationWriter is the handoff to the runner: it persists the prompt,
// provides a tee target for raw stream JSON, and seals the iteration with
// Finish.
type IterationWriter struct {
	run     *Run
	storyID string
	sIdx    int
	iIdx    int
	rec     IterationRecord

	streamMu   sync.Mutex
	streamFile *os.File
}

// WritePrompt persists the outgoing prompt bytes to <stem>.prompt.
func (w *IterationWriter) WritePrompt(prompt string) error {
	return os.WriteFile(w.rec.PromptFile, []byte(prompt), 0o644)
}

// RawStreamWriter returns an io.Writer that appends to <stem>.jsonl. The file
// is opened lazily on first call. Callers typically tee Claude's raw stdout
// into this writer. Safe for concurrent callers.
func (w *IterationWriter) RawStreamWriter() io.Writer {
	return &lazyStreamWriter{w: w}
}

func (w *IterationWriter) openStream() (*os.File, error) {
	w.streamMu.Lock()
	defer w.streamMu.Unlock()
	if w.streamFile != nil {
		return w.streamFile, nil
	}
	f, err := os.OpenFile(w.rec.TranscriptFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	w.streamFile = f
	return f, nil
}

// Finish stamps EndTime, token usage, session id, and optional error on the
// iteration record and closes the transcript file. The .meta.json sidecar is
// rewritten so the manifest can be rebuilt if truncated.
func (w *IterationWriter) Finish(sessionID string, usage *costs.TokenUsage, runErr error) error {
	rec, updateErr := w.run.updateIteration(w.sIdx, w.iIdx, func(ir *IterationRecord) {
		end := time.Now().UTC()
		ir.EndTime = &end
		if usage != nil {
			ir.TokenUsage = usage
		}
		if sessionID != "" {
			ir.SessionID = sessionID
		}
		if runErr != nil {
			ir.Error = runErr.Error()
		}
	})

	w.streamMu.Lock()
	if w.streamFile != nil {
		_ = w.streamFile.Close()
		w.streamFile = nil
	}
	w.streamMu.Unlock()

	if updateErr != nil {
		return updateErr
	}
	return writeIterMeta(rec)
}

type lazyStreamWriter struct {
	w *IterationWriter
}

func (l *lazyStreamWriter) Write(p []byte) (int, error) {
	f, err := l.w.openStream()
	if err != nil {
		return 0, err
	}
	l.w.streamMu.Lock()
	defer l.w.streamMu.Unlock()
	return f.Write(p)
}

// writeIterMeta persists the iteration record as a sibling .meta.json.
func writeIterMeta(rec IterationRecord) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal iter meta: %w", err)
	}
	return writeAtomicFn(rec.MetaFile, data)
}

// SweepInterrupted scans every repo's runs/*/manifest.json and flips running
// manifests to interrupted *only* when the manifest's Hostname matches this
// host AND the stamped PID is no longer alive. Entries from other hosts or
// live PIDs are left untouched, making this sweep safe to run on every Ralph
// start even with concurrent daemons in other repos.
func SweepInterrupted() error {
	reposDir, err := userdata.ReposDir()
	if err != nil {
		return err
	}
	repoEntries, err := os.ReadDir(reposDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read repos dir: %w", err)
	}
	host, _ := os.Hostname()
	for _, repo := range repoEntries {
		if !repo.IsDir() {
			continue
		}
		runsDir := filepath.Join(reposDir, repo.Name(), "runs")
		runEntries, err := os.ReadDir(runsDir)
		if err != nil {
			continue
		}
		for _, run := range runEntries {
			if !run.IsDir() {
				continue
			}
			if err := sweepOne(filepath.Join(runsDir, run.Name(), "manifest.json"), host); err != nil {
				// Keep sweeping — one bad manifest shouldn't block others.
				continue
			}
		}
	}
	return nil
}

func sweepOne(manifestPath, host string) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if m.Status != StatusRunning {
		return nil
	}
	if m.Hostname != host {
		return nil
	}
	if pidLiveFn(m.PID) {
		return nil
	}
	now := time.Now().UTC()
	m.Status = StatusInterrupted
	m.EndTime = &now
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicFn(manifestPath, out)
}

// pidLiveFn is swapped in tests. In production it delegates to pidAlive,
// which returns true iff the process exists and accepts signal 0.
var pidLiveFn = pidAlive

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds; Signal(0) probes liveness.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// runDirFor returns <userdata>/repos/<fp>/runs/<runID>.
func runDirFor(fp, runID string) (string, error) {
	repo, err := userdata.RepoDir(fp)
	if err != nil {
		return "", err
	}
	return filepath.Join(repo, "runs", runID), nil
}

// newRunID returns "run-<unix-ms>-<6char-base32-lowercase>". The millisecond
// prefix keeps IDs sortable; 30 bits of randomness make collisions negligible
// across parallel daemons.
func newRunID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	s := strings.ToLower(enc.EncodeToString(b[:]))
	if len(s) < 6 {
		return "", fmt.Errorf("base32 encoder returned %d chars", len(s))
	}
	return fmt.Sprintf("run-%d-%s", time.Now().UnixMilli(), s[:6]), nil
}

// gitState returns (branch, head SHA) for dir, best-effort. Empty strings on
// any failure — history tracking must not block a run.
func gitState(dir string) (branch, head string) {
	if out, err := gitCmd(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		branch = strings.TrimSpace(out)
	}
	if out, err := gitCmd(dir, "rev-parse", "HEAD"); err == nil {
		head = strings.TrimSpace(out)
	}
	return branch, head
}

func gitCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

// prdSnapshot returns sha256-hex of the PRD file content (or "" if missing).
func prdSnapshot(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

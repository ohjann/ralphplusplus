package postcompletion

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ohjann/ralphplusplus/internal/checkpoint"
)

func writePRD(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func prdHash(t *testing.T, prdFile string) string {
	t.Helper()
	h, err := checkpoint.ComputePRDHash(prdFile)
	if err != nil {
		t.Fatalf("ComputePRDHash: %v", err)
	}
	return h
}

func TestSentinel_MissingIsInvalid(t *testing.T) {
	tmp := t.TempDir()
	prd := filepath.Join(tmp, "prd.json")
	writePRD(t, prd, `{"project":"x"}`)

	if SentinelValid(tmp, prd) {
		t.Fatal("expected SentinelValid false with no sentinel file")
	}
}

func TestSentinel_RoundTripAndMatch(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".ralph"), 0o755); err != nil {
		t.Fatal(err)
	}
	prd := filepath.Join(tmp, "prd.json")
	writePRD(t, prd, `{"project":"x","user_stories":[{"id":"S1"}]}`)

	if err := WriteSentinel(tmp, prdHash(t, prd)); err != nil {
		t.Fatalf("WriteSentinel: %v", err)
	}
	if !SentinelValid(tmp, prd) {
		t.Fatal("expected SentinelValid true after WriteSentinel with matching hash")
	}
}

func TestSentinel_PRDChangeInvalidates(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".ralph"), 0o755); err != nil {
		t.Fatal(err)
	}
	prd := filepath.Join(tmp, "prd.json")
	writePRD(t, prd, `{"project":"x","user_stories":[]}`)

	if err := WriteSentinel(tmp, prdHash(t, prd)); err != nil {
		t.Fatalf("WriteSentinel: %v", err)
	}
	if !SentinelValid(tmp, prd) {
		t.Fatal("expected valid immediately after write")
	}

	writePRD(t, prd, `{"project":"x","user_stories":[{"id":"S1"}]}`)
	if SentinelValid(tmp, prd) {
		t.Fatal("expected SentinelValid false after PRD content changed")
	}
}

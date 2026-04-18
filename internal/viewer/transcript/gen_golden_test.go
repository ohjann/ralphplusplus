package transcript

// This file is intentionally part of the test package but is not a real test.
// It exists only to expose a helper used by `go test -run TestRegenerateGolden
// -update` when regenerating testdata/turns-golden.json. Kept in a _test.go
// file so it is never compiled into the production binary.

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "regenerate testdata/turns-golden.json")

func TestRegenerateGolden(t *testing.T) {
	if !*updateGolden {
		t.Skip("pass -update to regenerate the golden file")
	}
	dir := "testdata"
	seq, err := ParseFile(filepath.Join(dir, "prompt.md"), filepath.Join(dir, "stream.jsonl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	var turns []Turn
	for turn, err := range seq {
		if err != nil {
			t.Fatalf("iterate: %v", err)
		}
		turns = append(turns, turn)
	}
	out, err := json.MarshalIndent(turns, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out = append(out, '\n')
	if err := os.WriteFile(filepath.Join(dir, "turns-golden.json"), out, 0o644); err != nil {
		t.Fatalf("write golden: %v", err)
	}
}

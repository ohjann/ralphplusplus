package transcript

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseFile_Golden(t *testing.T) {
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

	got, err := json.MarshalIndent(turns, "", "  ")
	if err != nil {
		t.Fatalf("marshal got: %v", err)
	}
	got = append(got, '\n')

	want, err := os.ReadFile(filepath.Join(dir, "turns-golden.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("golden mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

func TestParseFile_ToolUseInputIsValidJSON(t *testing.T) {
	seq, err := ParseFile(
		filepath.Join("testdata", "prompt.md"),
		filepath.Join("testdata", "stream.jsonl"),
	)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	for turn, err := range seq {
		if err != nil {
			t.Fatalf("iterate: %v", err)
		}
		for i, b := range turn.Blocks {
			if b.Kind != "tool_use" {
				continue
			}
			if len(b.Input) == 0 {
				t.Errorf("turn %d block %d: tool_use input is empty", turn.Index, i)
				continue
			}
			var probe any
			if err := json.Unmarshal(b.Input, &probe); err != nil {
				t.Errorf("turn %d block %d: tool_use input is not valid JSON: %v (%s)", turn.Index, i, err, b.Input)
			}
		}
	}
}

func TestParseFile_EarlyBreak(t *testing.T) {
	seq, err := ParseFile(
		filepath.Join("testdata", "prompt.md"),
		filepath.Join("testdata", "stream.jsonl"),
	)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	count := 0
	for range seq {
		count++
		if count == 2 {
			break
		}
	}
	if count != 2 {
		t.Errorf("expected to break after 2 turns, got %d", count)
	}
}

func TestParseFile_MissingPrompt(t *testing.T) {
	_, err := ParseFile("testdata/does-not-exist.md", filepath.Join("testdata", "stream.jsonl"))
	if err == nil {
		t.Fatal("expected error for missing prompt, got nil")
	}
}

func TestParseFile_MissingJSONL(t *testing.T) {
	_, err := ParseFile(filepath.Join("testdata", "prompt.md"), "testdata/does-not-exist.jsonl")
	if err == nil {
		t.Fatal("expected error for missing jsonl, got nil")
	}
}

func TestParseFile_AbsorbsUnknownTopLevelTypes(t *testing.T) {
	tmp := t.TempDir()
	prompt := filepath.Join(tmp, "p.md")
	jsonl := filepath.Join(tmp, "s.jsonl")
	if err := os.WriteFile(prompt, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"system","subtype":"status"}`,
		`{"type":"telemetry","foo":"bar"}`,
		`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}`,
		`{"type":"assistant","message":{"id":"x","role":"assistant","model":"m"}}`,
		`{"type":"result","num_turns":0}`,
	}
	data := []byte("")
	for _, l := range lines {
		data = append(data, l...)
		data = append(data, '\n')
	}
	if err := os.WriteFile(jsonl, data, 0o644); err != nil {
		t.Fatal(err)
	}

	seq, err := ParseFile(prompt, jsonl)
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
	// Only Turn 0 (synthesised from prompt) should be emitted.
	if len(turns) != 1 {
		t.Fatalf("expected 1 turn (prompt only), got %d", len(turns))
	}
	if turns[0].Role != "user" || len(turns[0].Blocks) != 1 || turns[0].Blocks[0].Text != "hi" {
		t.Errorf("turn 0 not synthesised correctly: %+v", turns[0])
	}
}

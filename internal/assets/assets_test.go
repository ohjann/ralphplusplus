package assets

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestReadPromptEmbedded(t *testing.T) {
	for _, name := range []string{
		"ralph-prompt.md",
		"judge-prompt.md",
		"prompts/architect.md",
		"prompts/implementer.md",
		"prompts/debugger.md",
		"prompts/simplify.md",
		"prompts/dream.md",
		"prompts/synthesis.md",
	} {
		data, err := ReadPrompt(name)
		if err != nil {
			t.Errorf("ReadPrompt(%q) returned error: %v", name, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("ReadPrompt(%q) returned empty content", name)
		}
	}
}

func TestReadPromptMissing(t *testing.T) {
	t.Setenv("RALPH_HOME", "")
	if _, err := ReadPrompt("does-not-exist.md"); err == nil {
		t.Fatal("expected error for missing asset, got nil")
	}
}

func TestReadPromptRalphHomeOverride(t *testing.T) {
	dir := t.TempDir()
	override := []byte("OVERRIDE CONTENT")
	if err := os.WriteFile(filepath.Join(dir, "ralph-prompt.md"), override, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RALPH_HOME", dir)

	got, err := ReadPrompt("ralph-prompt.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(override) {
		t.Fatalf("expected override content, got %q", got)
	}
}

func TestReadPromptRalphHomeFallsBackToEmbed(t *testing.T) {
	// RALPH_HOME is set but the requested file isn't there — embedded copy
	// should still be used.
	dir := t.TempDir()
	t.Setenv("RALPH_HOME", dir)

	got, err := ReadPrompt("ralph-prompt.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("expected embedded fallback content, got empty")
	}
}

func TestReadPromptNestedRalphHomeOverride(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "prompts")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "architect.md"), []byte("ARCH"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RALPH_HOME", dir)

	got, err := ReadPrompt("prompts/architect.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ARCH" {
		t.Fatalf("expected override content, got %q", got)
	}
}

func TestSkillFS(t *testing.T) {
	sub := SkillFS()
	data, err := fs.ReadFile(sub, "SKILL.md")
	if err != nil {
		t.Fatalf("reading SKILL.md from SkillFS: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("SKILL.md is empty")
	}
}

func TestAvailable(t *testing.T) {
	names := Available()
	want := map[string]bool{
		"ralph-prompt.md":        false,
		"judge-prompt.md":        false,
		"prompts/architect.md":   false,
		"prompts/implementer.md": false,
		"prompts/debugger.md":    false,
		"prompts/simplify.md":    false,
		"prompts/dream.md":       false,
		"prompts/synthesis.md":   false,
	}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
		// Ensure skill files are not reported as prompts.
		if n == "skills/ralph/SKILL.md" {
			t.Errorf("Available() should not include skill files: %s", n)
		}
	}
	for n, found := range want {
		if !found {
			t.Errorf("Available() missing %q", n)
		}
	}
}

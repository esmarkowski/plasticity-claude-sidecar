package memory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/esmarkowski/plasticity-claude-sidecar/internal/harness"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The store is keyed to the project, not to the working directory: a session in a
// worktree reads the memories of the repo the worktree came from. Deriving the
// path from the transcript finds nothing at all there, and the harness knows the
// real answer.
func TestDirPrefersWhatTheHarnessReports(t *testing.T) {
	mem := []harness.Item{
		{Name: "/home/u/.claude/CLAUDE.md", Source: "User"},
		{Name: "/repo/CLAUDE.md", Source: "Project"},
		{Name: "/home/u/.claude/projects/-repo/memory/MEMORY.md", Source: "AutoMem"},
	}
	if got := Dir(mem, "/home/u/.claude/projects/-repo--worktree/s.jsonl"); got !=
		"/home/u/.claude/projects/-repo/memory" {
		t.Errorf("Dir = %q, want the directory the harness named", got)
	}

	// With no probe yet, beside the transcript is the right guess for a session
	// started in the project root.
	if got := Dir(nil, "/home/u/.claude/projects/-repo/s.jsonl"); got !=
		"/home/u/.claude/projects/-repo/memory" {
		t.Errorf("fallback Dir = %q", got)
	}
	// Instruction files are not memories, whatever their path looks like.
	if got := Dir([]harness.Item{{Name: "/x/memory/CLAUDE.md", Source: "Project"}}, ""); got != "" {
		t.Errorf("a Project file was taken for the memory store: %q", got)
	}
	if got := Dir(nil, ""); got != "" {
		t.Errorf("Dir invented %q from nothing", got)
	}
}

func TestLoadReadsTheStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "memory")
	write(t, dir, IndexFile, "- [A thing](thing.md) — a hook\n- [Gone](missing.md) — dangling\n")
	write(t, dir, "thing.md", `---
name: a-thing
description: what the thing is for
metadata:
  type: feedback
---

The body of the memory, which is most of its cost.
`)
	write(t, dir, "orphan.md", "---\nname: orphan\ndescription: nobody links here\nmetadata:\n  type: project\n---\n\nbody\n")
	// Not memories.
	write(t, dir, "notes.txt", "ignored")
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	s := Load(dir)
	if s.Index.Tokens == 0 {
		t.Error("the index was not read")
	}
	if len(s.Notes) != 2 {
		t.Fatalf("read %d memories, want 2: %+v", len(s.Notes), s.Notes)
	}

	byName := map[string]Note{}
	for _, n := range s.Notes {
		byName[n.Name] = n
	}
	thing := byName["thing.md"]
	if thing.Title != "a-thing" || thing.Description != "what the thing is for" || thing.Kind != "feedback" {
		t.Errorf("frontmatter not read: %+v", thing)
	}
	if !thing.Indexed {
		t.Error("a memory the index links to was reported as an orphan")
	}
	// On disk, unreachable, never going to be recalled — the thing worth seeing.
	if byName["orphan.md"].Indexed {
		t.Error("a memory nothing links to was reported as indexed")
	}
	if orphans := s.Orphans(); len(orphans) != 1 || orphans[0].Name != "orphan.md" {
		t.Errorf("orphans = %+v", orphans)
	}
	if s.Total() != s.Index.Tokens+s.Notes[0].Tokens+s.Notes[1].Tokens {
		t.Error("Total does not account for the index and every memory")
	}
}

// A description in the body of a memory about YAML is not that memory's
// description.
func TestParseOnlyTrustsFrontmatter(t *testing.T) {
	n := parse("/x/y.md", []byte("no frontmatter here\ndescription: not mine\nname: also not\n"))
	if n.Description != "" || n.Title != "y" {
		t.Errorf("body was read as frontmatter: %+v", n)
	}
}

// A project nothing has been remembered about is the ordinary case, and is not an
// error.
func TestLoadTolerantOfAnAbsentStore(t *testing.T) {
	s := Load(filepath.Join(t.TempDir(), "nope"))
	if !s.Empty() || len(s.Notes) != 0 {
		t.Errorf("a missing directory produced %+v", s)
	}
	if !Load("").Empty() {
		t.Error("an empty path produced a store")
	}
}

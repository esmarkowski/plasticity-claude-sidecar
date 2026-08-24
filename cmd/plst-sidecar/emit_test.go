package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const payloadPath = "../../testdata/hooks/post-tool-use.json"

// BenchmarkEmit measures the whole cost a hook actually pays: process start,
// JSON parse, and the append. The in-process work alone would flatter it, and
// the number that matters is what gets added to every tool call in a session.
func BenchmarkEmit(b *testing.B) {
	bin := binary(b)
	payload, err := os.ReadFile(payloadPath)
	if err != nil {
		b.Fatal(err)
	}
	b.Setenv("CLAUDE_SIDECAR_DIR", b.TempDir())
	b.ResetTimer()
	for b.Loop() {
		cmd := exec.Command(bin, "emit")
		cmd.Stdin = bytes.NewReader(payload)
		if err := cmd.Run(); err != nil {
			b.Fatal(err)
		}
	}
}

// TestEmitIsSilentAndForgiving pins the two invariants that keep a broken
// sidecar from harming the session that invoked it: nothing on stdout, and
// always exit 0. Both are checked against inputs designed to break it.
func TestEmitIsSilentAndForgiving(t *testing.T) {
	bin := binary(t)
	good, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		stdin []byte
		dir   string
	}{
		{"valid payload", good, t.TempDir()},
		{"malformed json", []byte("{ not json"), t.TempDir()},
		{"empty stdin", nil, t.TempDir()},
		{"binary garbage", []byte{0x00, 0xff, 0xfe}, t.TempDir()},
		{"unwritable log dir", good, readOnlyDir(t)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, "emit")
			cmd.Stdin = bytes.NewReader(tc.stdin)
			cmd.Env = append(os.Environ(), "CLAUDE_SIDECAR_DIR="+tc.dir)
			var out bytes.Buffer
			cmd.Stdout = &out
			if err := cmd.Run(); err != nil {
				t.Errorf("exited non-zero (%v); a hook failure costs the user context", err)
			}
			if out.Len() != 0 {
				t.Errorf("wrote %q to stdout; hook stdout is injected into the model's context", out.String())
			}
		})
	}
}

func readOnlyDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "nested")
}

func binary(tb testing.TB) string {
	tb.Helper()
	bin, err := filepath.Abs("../../bin/sidecar")
	if err != nil {
		tb.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		tb.Skip("run `mise run build` first")
	}
	return bin
}

package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"claude-sidecar/internal/event"
)

// A probe starts a Claude Code session, so it is cached hard. The things it
// measures — system prompt, tool schemas, memory files, skills — only change
// when configuration changes, which is what the fingerprint tracks.
type cached struct {
	Fingerprint string   `json:"fingerprint"`
	Snapshot    Snapshot `json:"snapshot"`
}

func cachePath() string { return filepath.Join(event.Dir(), "harness-cache.json") }

// Load returns a cached snapshot for dir, and whether it is still valid for the
// current configuration.
func Load(dir string) (Snapshot, bool) {
	all := loadAll()
	c, ok := all[dir]
	if !ok {
		return Snapshot{}, false
	}
	return c.Snapshot, c.Fingerprint == fingerprint(dir)
}

// Save records a snapshot against the configuration that produced it.
func Save(dir string, s Snapshot) error {
	all := loadAll()
	all[dir] = cached{Fingerprint: fingerprint(dir), Snapshot: s}
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(event.Dir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(cachePath(), append(b, '\n'), 0o600)
}

func loadAll() map[string]cached {
	all := map[string]cached{}
	b, err := os.ReadFile(cachePath())
	if err != nil {
		return all
	}
	_ = json.Unmarshal(b, &all)
	return all
}

// fingerprint hashes the size and mtime of every input that changes what the
// harness loads. Cheap to compute and it changes exactly when a re-probe is
// warranted — editing CLAUDE.md, installing a plugin, adding a skill.
func fingerprint(dir string) string {
	home, _ := os.UserHomeDir()
	inputs := []string{
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".claude", "CLAUDE.md"),
		filepath.Join(home, ".claude", "agents"),
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".claude", "plugins"),
		filepath.Join(dir, "CLAUDE.md"),
		filepath.Join(dir, ".claude"),
		filepath.Join(dir, ".mcp.json"),
	}
	h := sha256.New()
	for _, p := range inputs {
		fi, err := os.Stat(p)
		if err != nil {
			_, _ = fmt.Fprintf(h, "%s:absent\n", p)
			continue
		}
		_, _ = fmt.Fprintf(h, "%s:%d:%d\n", p, fi.Size(), fi.ModTime().UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Get returns a usable snapshot for dir, probing only when the cache is missing
// or stale. probe is separated from the cache read so callers in a UI can
// decide whether they are willing to wait.
func Get(dir string, allowProbe bool, timeout time.Duration) (Snapshot, error) {
	if s, fresh := Load(dir); s.OK() && fresh {
		return s, nil
	}
	if !allowProbe {
		if s, _ := Load(dir); s.OK() {
			// Stale beats nothing: the system prompt rarely moves, and a stale
			// figure is far closer than inferring one by subtraction.
			return s, nil
		}
		return Snapshot{}, fmt.Errorf("no harness snapshot for %s — run `sidecar probe`", dir)
	}
	s, err := Probe(dir, timeout)
	if err != nil {
		return Snapshot{}, err
	}
	return s, Save(dir, s)
}

// Package memory reads the file-per-fact memory store an agent keeps beside a
// project's transcripts.
//
// What this can and cannot know is worth stating plainly, because the difference
// is the whole reason the package is small. The index — MEMORY.md — is loaded at
// session start, and the harness reports it by name and exact size, so its cost
// is a measurement. The individual memories are recalled on demand, and nothing
// records which: across every transcript on the machine this was written on, no
// recalled memory's text is ever injected and no system reminder names one. So
// this reports the pool and what each memory would cost if recalled, and says so
// rather than implying the list is what is in the window.
package memory

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/esmarkowski/plasticity-claude-sidecar/internal/attrib"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/harness"
)

// IndexFile is the memory index, and the one memory artifact loaded at startup.
const IndexFile = "MEMORY.md"

// Note is one memory file.
type Note struct {
	Name string
	Path string
	// Title and Description come from the frontmatter, which is what makes a
	// listing readable: a slug says what a file is called, a description says
	// what it is for.
	Title       string
	Description string
	Kind        string
	// Tokens is what the memory costs if it is recalled, estimated from its own
	// text rather than from its size on disk — the two differ by a factor that
	// depends on how dense the writing is.
	Tokens int
	// Indexed is whether the index links to it. A memory the index has lost is
	// still on disk, is no longer reachable, and will never be recalled — which
	// is worth seeing.
	Indexed bool
}

// Store is a project's memory directory.
type Store struct {
	Dir   string
	Index Note
	Notes []Note
}

// Dir is where a session's memories live.
//
// The harness is asked first, and answers exactly: /context lists the memory
// index it loaded, with AutoMem as its source, and the directory that file sits
// in is the store. This matters because the store is keyed to the project rather
// than to the working directory — a session running in a worktree reads the
// memories of the repo the worktree came from, and deriving the path from the
// transcript's own directory finds nothing at all.
//
// Guessing beside the transcript is the fallback, for a session with no probe
// yet. It is right for a session started in the project root, which is most of
// them.
func Dir(mem []harness.Item, transcript string) string {
	for _, it := range mem {
		if strings.EqualFold(it.Source, autoMemSource) && it.Name != "" {
			return filepath.Dir(it.Name)
		}
	}
	if transcript == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(transcript), "memory")
}

// autoMemSource is what the harness calls a memory file in its own accounting,
// as distinct from the User and Project instruction files beside it.
const autoMemSource = "AutoMem"

// Load reads a memory store. A missing directory is the ordinary state of a
// project nothing has been remembered about, and gives an empty store rather
// than an error.
func Load(dir string) Store {
	s := Store{Dir: dir}
	if dir == "" {
		return s
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return s
	}

	linked := map[string]bool{}
	if b, err := os.ReadFile(filepath.Join(dir, IndexFile)); err == nil {
		s.Index = parse(filepath.Join(dir, IndexFile), b)
		s.Index.Indexed = true
		for _, m := range indexLink.FindAllStringSubmatch(string(b), -1) {
			linked[filepath.Base(m[1])] = true
		}
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == IndexFile {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		n := parse(path, b)
		n.Indexed = linked[e.Name()]
		s.Notes = append(s.Notes, n)
	}
	// Largest first: this is a list read to find what is costing something.
	sort.Slice(s.Notes, func(i, j int) bool {
		if s.Notes[i].Tokens != s.Notes[j].Tokens {
			return s.Notes[i].Tokens > s.Notes[j].Tokens
		}
		return s.Notes[i].Name < s.Notes[j].Name
	})
	return s
}

// indexLink matches the markdown links the index is made of, which is how a
// memory is known to be reachable.
var indexLink = regexp.MustCompile(`\(([^)]+\.md)\)`)

var (
	frontName = regexp.MustCompile(`(?m)^name:\s*(.+)$`)
	frontDesc = regexp.MustCompile(`(?m)^description:\s*(.+)$`)
	frontType = regexp.MustCompile(`(?m)^\s*type:\s*(.+)$`)
)

// parse reads what a memory says about itself.
//
// Only the frontmatter, and only where there is one: a `description:` line in the
// body of a memory about YAML is not that memory's description.
func parse(path string, b []byte) Note {
	n := Note{Name: filepath.Base(path), Path: path, Tokens: attrib.Estimate(string(b))}
	head := ""
	if s := string(b); strings.HasPrefix(s, "---") {
		if end := strings.Index(s[3:], "\n---"); end >= 0 {
			head = s[:end+3]
		}
	}
	if m := frontName.FindStringSubmatch(head); m != nil {
		n.Title = strings.TrimSpace(m[1])
	}
	if m := frontDesc.FindStringSubmatch(head); m != nil {
		n.Description = strings.TrimSpace(m[1])
	}
	if m := frontType.FindStringSubmatch(head); m != nil {
		n.Kind = strings.TrimSpace(m[1])
	}
	if n.Title == "" {
		n.Title = strings.TrimSuffix(n.Name, ".md")
	}
	return n
}

// Total is what the whole pool would cost if every memory were recalled at once.
func (s Store) Total() int {
	n := s.Index.Tokens
	for _, m := range s.Notes {
		n += m.Tokens
	}
	return n
}

// Empty is a project nothing has been remembered about.
func (s Store) Empty() bool { return s.Index.Tokens == 0 && len(s.Notes) == 0 }

// Orphans are memories the index does not link to: on disk, unreachable, and
// never going to be recalled.
func (s Store) Orphans() []Note {
	var out []Note
	for _, m := range s.Notes {
		if !m.Indexed {
			out = append(out, m)
		}
	}
	return out
}

package harness

import "testing"

// Sample of what /context actually printed on this machine, kept verbatim so a
// change in the harness's output format fails a test instead of silently
// zeroing the largest category in the dashboard.
const sample = `## Context Usage

**Model:** claude-opus-5[1m]  
**Tokens:** 21.3k / 1m (2%)

### Estimated usage by category

| Category | Tokens | Percentage |
|----------|--------|------------|
| System prompt | 3.3k | 0.3% |
| System tools | 12.8k | 1.3% |
| System tools (deferred) | 12.5k | 1.2% |
| Custom agents | 782 | 0.1% |
| Memory files | 1.2k | 0.1% |
| Skills | 3.2k | 0.3% |
| Messages | 8 | 0.0% |
| Free space | 978.7k | 97.9% |

### Custom Agents

| Agent Type | Source | Tokens |
|------------|--------|--------|
| caveman:cavecrew-builder | Plugin | 134 |
| implement-agent | User | 140 |

### Memory Files

| Type | Path | Tokens |
|------|------|--------|
| User | /Users/spencer/.claude/CLAUDE.md | 265 |
| User | /Users/spencer/.claude/github-issues.md | 871 |

### Skills

| Skill | Source | Tokens |
|-------|--------|--------|
| dataviz | Built-in | ~380 |
| loop | Built-in | ~120 |
`

func TestParse(t *testing.T) {
	s := Parse(sample)

	if s.Model != "claude-opus-5[1m]" {
		t.Errorf("model = %q", s.Model)
	}
	if s.Window != 1_000_000 {
		t.Errorf("window = %d, want 1000000", s.Window)
	}
	if s.Used != 21_300 {
		t.Errorf("used = %d, want 21300", s.Used)
	}
	if got := s.Categories[CatSystemTools]; got != 12_800 {
		t.Errorf("system tools = %d, want 12800", got)
	}
	// Static must exclude Messages and Free space, or it would report the whole
	// window as preloaded.
	if got := s.Static(); got != 33_782 {
		t.Errorf("static = %d, want 33782", got)
	}
	if len(s.Agents) != 2 || s.Agents[0].Name != "caveman:cavecrew-builder" {
		t.Errorf("agents = %+v", s.Agents)
	}
	// Memory rows are type|path|tokens — the path is the identity, not the type.
	if len(s.Memory) != 2 || s.Memory[0].Name != "/Users/spencer/.claude/CLAUDE.md" {
		t.Errorf("memory = %+v", s.Memory)
	}
	if s.Memory[0].Tokens != 265 {
		t.Errorf("memory tokens = %d, want 265", s.Memory[0].Tokens)
	}
	if len(s.Skills) != 2 || s.Skills[0].Tokens != 380 {
		t.Errorf("skills = %+v", s.Skills)
	}
	if !s.OK() {
		t.Error("snapshot should be usable")
	}
}

func TestParseTokens(t *testing.T) {
	cases := map[string]int{
		"3.3k": 3300, "12.5k": 12500, "1m": 1_000_000, "978.7k": 978_700,
		"~380": 380, "782": 782, "12,500": 12_500, "": 0, "n/a": 0,
	}
	for in, want := range cases {
		if got := parseTokens(in); got != want {
			t.Errorf("parseTokens(%q) = %d, want %d", in, got, want)
		}
	}
}

// An unrecognizable report must yield an unusable snapshot rather than a
// confidently wrong one, so callers fall back to inference instead of trusting
// zeros.
func TestParseRejectsGarbage(t *testing.T) {
	if s := Parse("not the report you were looking for"); s.OK() {
		t.Error("garbage parsed into a usable snapshot")
	}
}

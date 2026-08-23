// Package harness reads Claude Code's own accounting of what it loaded.
//
// Claude Code has a /context command that reports per-category token counts,
// including the three things a transcript can never reveal: the size of the
// system prompt, the size of the tool schemas, and the per-file cost of the
// memory files it injected. Those numbers come from the harness's own
// tokenizer, so they beat anything this program could estimate.
//
// The catch is that reading them means starting a session — /context only
// exists inside one — so a probe is a real (if tiny) Claude Code run. That is
// why results are cached and refreshed only when the inputs change.
package harness

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Item is one named contributor: a memory file, a skill, an agent.
type Item struct {
	Name   string `json:"name"`
	Source string `json:"source,omitempty"`
	Tokens int    `json:"tokens"`
}

// Snapshot is one reading of what the harness loads for a given directory.
type Snapshot struct {
	CWD        string         `json:"cwd"`
	Model      string         `json:"model"`
	Window     int            `json:"window"`
	Used       int            `json:"used"`
	Categories map[string]int `json:"categories"`
	Agents     []Item         `json:"agents"`
	Memory     []Item         `json:"memory"`
	Skills     []Item         `json:"skills"`
	ProbedAt   time.Time      `json:"probed_at"`
}

// Category names as /context reports them.
const (
	CatSystemPrompt  = "System prompt"
	CatSystemTools   = "System tools"
	CatDeferredTools = "System tools (deferred)"
	CatCustomAgents  = "Custom agents"
	CatMemoryFiles   = "Memory files"
	CatSkills        = "Skills"
	CatMessages      = "Messages"
	CatFreeSpace     = "Free space"
)

// Static is everything the harness loads before the conversation starts. This
// is the constant that a transcript-only analysis has to infer by subtraction.
func (s Snapshot) Static() int {
	n := 0
	for _, k := range []string{CatSystemPrompt, CatSystemTools, CatDeferredTools, CatCustomAgents, CatMemoryFiles, CatSkills} {
		n += s.Categories[k]
	}
	return n
}

// OK reports whether this snapshot parsed into something usable.
func (s Snapshot) OK() bool { return s.Window > 0 && len(s.Categories) > 0 }

// Probe starts a throwaway Claude Code session in dir and reads /context.
//
// The session is real: it appears in the transcript directory and costs a
// negligible number of tokens. Callers should cache the result rather than
// probing on a timer.
func Probe(dir string, timeout time.Duration) (Snapshot, error) {
	cmd := exec.Command("claude", "-p", "/context", "--output-format", "json")
	cmd.Dir = dir
	out, err := runWithTimeout(cmd, timeout)
	if err != nil {
		return Snapshot{}, err
	}
	var envelope struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		return Snapshot{}, fmt.Errorf("parsing claude output: %w", err)
	}
	if envelope.IsError {
		return Snapshot{}, fmt.Errorf("claude reported an error running /context")
	}
	s := Parse(envelope.Result)
	s.CWD = dir
	s.ProbedAt = time.Now().UTC()
	if !s.OK() {
		return s, fmt.Errorf("could not parse /context output")
	}
	return s, nil
}

func runWithTimeout(cmd *exec.Cmd, timeout time.Duration) ([]byte, error) {
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := cmd.Output()
		done <- result{out, err}
	}()
	select {
	case r := <-done:
		return r.out, r.err
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil, fmt.Errorf("claude -p /context timed out after %s", timeout)
	}
}

// Parse reads the markdown /context prints. Tolerant by design: an unfamiliar
// section is skipped rather than fatal, because this output is a human-facing
// report and its layout is free to change.
func Parse(md string) Snapshot {
	s := Snapshot{Categories: map[string]int{}}
	section := ""
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "#") {
			section = strings.ToLower(strings.TrimLeft(line, "# "))
			continue
		}
		if strings.HasPrefix(line, "**Model:**") {
			s.Model = strings.TrimSpace(strings.TrimPrefix(line, "**Model:**"))
			continue
		}
		if strings.HasPrefix(line, "**Tokens:**") {
			s.Used, s.Window = parseUsage(strings.TrimPrefix(line, "**Tokens:**"))
			continue
		}
		cells, ok := row(line)
		if !ok {
			continue
		}
		switch {
		case strings.Contains(section, "usage by category") && len(cells) >= 2:
			s.Categories[cells[0]] = parseTokens(cells[1])
		case strings.Contains(section, "custom agents") && len(cells) >= 3:
			s.Agents = append(s.Agents, Item{Name: cells[0], Source: cells[1], Tokens: parseTokens(cells[2])})
		case strings.Contains(section, "memory files") && len(cells) >= 3:
			// Columns are type, path, tokens; the path is the identity.
			s.Memory = append(s.Memory, Item{Name: cells[1], Source: cells[0], Tokens: parseTokens(cells[2])})
		case strings.Contains(section, "skills") && len(cells) >= 3:
			s.Skills = append(s.Skills, Item{Name: cells[0], Source: cells[1], Tokens: parseTokens(cells[2])})
		}
	}
	return s
}

// row splits a markdown table row, rejecting header and separator rows.
func row(line string) ([]string, bool) {
	if !strings.HasPrefix(line, "|") {
		return nil, false
	}
	parts := strings.Split(strings.Trim(line, "|"), "|")
	cells := make([]string, 0, len(parts))
	for _, p := range parts {
		cells = append(cells, strings.TrimSpace(p))
	}
	if len(cells) == 0 || cells[0] == "" {
		return nil, false
	}
	switch cells[0] {
	case "Category", "Agent Type", "Type", "Skill":
		return nil, false
	}
	if strings.HasPrefix(cells[0], "---") {
		return nil, false
	}
	return cells, true
}

// parseUsage reads "21.3k / 1m (2%)".
func parseUsage(s string) (used, window int) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "("); i >= 0 {
		s = s[:i]
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	return parseTokens(parts[0]), parseTokens(parts[1])
}

// parseTokens reads the abbreviated counts /context prints: "3.3k", "~380",
// "1m", "12,500". The abbreviation costs a little precision, which is well
// inside the error of anything else in this program.
func parseTokens(s string) int {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "~"))
	s = strings.ReplaceAll(s, ",", "")
	if s == "" {
		return 0
	}
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "k"), strings.HasSuffix(s, "K"):
		mult, s = 1_000, s[:len(s)-1]
	case strings.HasSuffix(s, "m"), strings.HasSuffix(s, "M"):
		mult, s = 1_000_000, s[:len(s)-1]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int(v * mult)
}

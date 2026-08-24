package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/esmarkowski/plasticity-claude-sidecar/internal/attrib"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/event"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/harness"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/session"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/transcript"
)

// report prints the context breakdown as plain text. Deliberately built before
// any TUI: it makes the attribution engine inspectable and diffable, and it is
// the thing to reach for when the question is "what did that tool call cost".
func report(args []string) int {
	evs, err := event.Load(event.LogPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "sidecar:", err)
		return 1
	}

	sess, ok := pickSession(args, evs)
	if !ok {
		fmt.Fprintln(os.Stderr, "sidecar: no session found — start a Claude Code session, or pass --session")
		return 1
	}

	lines, err := transcript.Load(sess.Transcript)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sidecar:", err)
		return 1
	}
	// A stale snapshot still beats none: the system prompt does not move, and
	// inferring it by subtraction is strictly worse than a slightly old
	// measurement.
	snap, _ := harness.Get(sess.CWD, false, 0)

	rep := attrib.AnalyzeWith(lines, evs, snap)
	rep.Session = sess.ID
	if rep.CWD == "" {
		rep.CWD = sess.CWD
	}

	if hasFlag(args, "--audit") {
		printAudit(attrib.Audit(lines, evs, snap))
		return 0
	}
	if hasFlag(args, "--json") {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintln(os.Stderr, "sidecar:", err)
			return 1
		}
		return 0
	}
	printReport(rep, hasFlag(args, "--detail"))
	return 0
}

func pickSession(args []string, evs []event.Event) (session.Session, bool) {
	for i, a := range args {
		if a == "--session" && i+1 < len(args) {
			return session.Find(evs, args[i+1])
		}
	}
	return session.Active(evs)
}

const barWidth = 34

func printReport(r attrib.Report, detail bool) {
	fmt.Printf("session  %s\n", short(r.Session))
	fmt.Printf("cwd      %s", r.CWD)
	if r.Branch != "" {
		fmt.Printf("  (%s)", r.Branch)
	}
	fmt.Println()
	fmt.Printf("model    %s\n", r.Model)
	fmt.Printf("context  %s tokens over %d requests\n", comma(r.Total), r.Turns)
	fmt.Printf("cache    %s read · %s written · %s fresh\n",
		comma(r.Usage.CacheReadInputTokens),
		comma(r.Usage.CacheCreationInputTokens),
		comma(r.Usage.InputTokens))
	if r.Compacted {
		fmt.Printf("compact  yes — window reached %s before collapsing\n", comma(r.PreTokens))
	}
	fmt.Println()

	for _, s := range r.Slices {
		pct := 0.0
		if r.Total > 0 {
			pct = float64(s.Tokens) / float64(r.Total) * 100
		}
		fmt.Printf("  %-24s %9s  %5.1f%%  %s\n",
			s.Bucket, comma(s.Tokens), pct, bar(pct))
		if !detail {
			continue
		}
		for i, it := range s.Detail {
			if i >= 8 {
				fmt.Printf("      %-20s %s more\n", "…", comma(len(s.Detail)-8))
				break
			}
			note := it.Note
			if it.Count > 1 {
				note = fmt.Sprintf("×%d %s", it.Count, note)
			}
			fmt.Printf("      %-40s %9s  %s\n", trim(it.Name, 40), comma(it.Tokens), strings.TrimSpace(note))
		}
	}
}

// printAudit shows the residual request by request. A flat residual column is
// the evidence that attribution is complete; a climbing one names the bug.
func printAudit(points []attrib.AuditPoint) {
	fmt.Printf("%5s %12s %12s %12s %12s %10s\n", "req", "context", "measured", "residual", "thinking", "resid-Δ")
	prev := 0
	for i, p := range points {
		delta := ""
		if i > 0 {
			delta = fmt.Sprintf("%+d", p.Residual-prev)
		}
		fmt.Printf("%5d %12s %12s %12s %12s %10s\n",
			p.Request, comma(p.Context), comma(p.Measured), comma(p.Residual), comma(p.Thinking), delta)
		prev = p.Residual
	}
}

func bar(pct float64) string {
	n := int(pct / 100 * barWidth)
	if n > barWidth {
		n = barWidth
	}
	return strings.Repeat("█", n) + strings.Repeat("░", barWidth-n)
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func trim(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	// Keep the tail: for a file path the basename identifies it, the leading
	// directories rarely do.
	return "…" + string(r[len(r)-n+1:])
}

// comma groups thousands. Context sizes run to seven figures and are unreadable
// without it.
func comma(n int) string {
	s := fmt.Sprint(n)
	if n < 0 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

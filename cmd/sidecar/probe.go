package main

import (
	"fmt"
	"os"
	"time"

	"claude-sidecar/internal/event"
	"claude-sidecar/internal/harness"
	"claude-sidecar/internal/session"
)

// probeTimeout is generous: a probe starts a real Claude Code session, and
// startup on a large project with many plugins is not fast.
const probeTimeout = 3 * time.Minute

// probe reads Claude Code's own /context accounting and caches it.
//
// This is the only way to get exact numbers for the parts of the window that
// never reach the transcript: the system prompt, the tool schemas, and the
// per-file cost of memory files. Without it the dashboard has to infer that
// whole block by subtraction, which lumps it into one opaque row.
//
// It costs a throwaway session, so it is explicit rather than automatic, and
// the result is cached until the configuration that produced it changes.
func probe(args []string) int {
	dir := flagValue(args, "--dir")
	if dir == "" {
		// Probe where the session being watched actually runs: memory files and
		// skills are per-project, so probing the wrong directory measures the
		// wrong thing.
		evs, _ := event.Load(event.LogPath())
		if s, ok := session.Active(evs); ok && s.CWD != "" {
			dir = s.CWD
		} else {
			dir, _ = os.Getwd()
		}
	}

	if s, fresh := harness.Load(dir); s.OK() && fresh && !hasFlag(args, "--force") {
		fmt.Printf("cached snapshot for %s is still current (probed %s)\n",
			dir, s.ProbedAt.Local().Format("Jan 2 15:04"))
		printSnapshot(s)
		return 0
	}

	fmt.Printf("probing %s — this starts a short Claude Code session…\n", dir)
	s, err := harness.Probe(dir, probeTimeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sidecar:", err)
		return 1
	}
	if err := harness.Save(dir, s); err != nil {
		fmt.Fprintln(os.Stderr, "sidecar: caching snapshot:", err)
		return 1
	}
	printSnapshot(s)
	return 0
}

func printSnapshot(s harness.Snapshot) {
	fmt.Printf("\nmodel   %s\nwindow  %s tokens\nstatic  %s tokens loaded before the conversation starts\n\n",
		s.Model, comma(s.Window), comma(s.Static()))

	for _, k := range []string{
		harness.CatSystemPrompt, harness.CatSystemTools, harness.CatDeferredTools,
		harness.CatCustomAgents, harness.CatMemoryFiles, harness.CatSkills,
	} {
		if v := s.Categories[k]; v > 0 {
			fmt.Printf("  %-26s %9s\n", k, comma(v))
		}
	}

	if len(s.Memory) > 0 {
		fmt.Println("\nmemory files")
		for _, it := range s.Memory {
			fmt.Printf("  %-58s %8s  %s\n", trim(it.Name, 58), comma(it.Tokens), it.Source)
		}
	}
	if len(s.Skills) > 0 {
		fmt.Printf("\nskills — %d loaded, %s tokens\n", len(s.Skills), comma(s.Categories[harness.CatSkills]))
		for i, it := range topItems(s.Skills, 8) {
			fmt.Printf("  %-40s %8s  %s\n", trim(it.Name, 40), comma(it.Tokens), it.Source)
			if i == 7 {
				fmt.Printf("  %-40s %8s\n", "…", "")
			}
		}
	}
}

// topItems returns the n largest, since these lists run to dozens of entries
// and only the expensive ones are actionable.
func topItems(items []harness.Item, n int) []harness.Item {
	sorted := make([]harness.Item, len(items))
	copy(sorted, items)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j-1].Tokens < sorted[j].Tokens; j-- {
			sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
		}
	}
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}

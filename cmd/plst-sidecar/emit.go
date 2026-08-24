package main

import (
	"bytes"
	"io"
	"os"
	"time"

	"github.com/esmarkowski/plasticity-claude-sidecar/internal/event"
)

// maxStdin bounds what we will read from a hook. A PostToolUse payload carries
// the full tool output, which for a wide grep can be enormous; we only need
// enough to size and preview it.
const maxStdin = 8 << 20

// emit is the hook entry point. Two invariants, both about never harming the
// session that invoked us:
//
//  1. Nothing is ever written to stdout. Hook stdout is injected into the
//     model's context on several events, so a stray byte here would corrupt
//     the very thing this tool exists to measure.
//  2. The exit code is always 0. A non-zero exit surfaces in the transcript as
//     a hook_non_blocking_error and costs the user context to read.
func emit(args []string) int {
	raw, _ := io.ReadAll(io.LimitReader(os.Stdin, maxStdin))
	if len(bytes.TrimSpace(raw)) == 0 {
		// No payload means we were not really invoked as a hook. Recording an
		// event for it would only add noise to the log.
		return 0
	}
	ev := event.FromHook(raw, time.Now().UTC())
	if hasFlag(args, "--no-preview") {
		ev.Previews = nil
	}
	// Deliberately discarded: there is no useful recovery, and complaining
	// would violate both invariants above.
	_ = event.Append(ev)
	return 0
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

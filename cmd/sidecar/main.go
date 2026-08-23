// Command sidecar is a debugging companion for Claude Code: it records what the
// harness injects into a session and renders where the context window went.
//
// One binary, several roles. `emit` runs as a hook hundreds of times a session
// and must stay fast and silent; everything else is interactive.
package main

import (
	"fmt"
	"os"
)

const usage = `sidecar — Claude Code context debugger

  sidecar emit                 hook target; reads hook JSON on stdin, appends one event
  sidecar report [--json]      context attribution for the active session
  sidecar watch [--follow]     live dashboard
  sidecar open [--dev]         open the dashboard in a new Ghostty window
  sidecar probe [--force]      read Claude Code's own /context accounting and cache it
  sidecar install              register the hooks in ~/.claude/settings.json
  sidecar events [-n N]        tail the raw event log
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "emit":
		os.Exit(emit(args))
	case "events":
		os.Exit(events(args))
	case "report":
		os.Exit(report(args))
	case "watch":
		os.Exit(watch(args))
	case "open":
		os.Exit(openWindow(args))
	case "probe":
		os.Exit(probe(args))
	case "install":
		os.Exit(install(args))
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

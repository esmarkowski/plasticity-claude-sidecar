// Command plst-sidecar is a plst module: a debugging companion for agent
// sessions that records what the harness injects and renders where the context
// window went.
//
// One binary, several roles. `emit` runs as a hook hundreds of times a session
// and must stay fast and silent; everything else is interactive.
package main

import (
	"fmt"
	"os"
)

// version is stamped at build time.
var version = "dev"

const usage = `plst sidecar — agent context debugger

  plst sidecar start            the dashboard, following the active session
                                unless --no-follow or --session
       --window [--dev]         open it in a new Ghostty window instead
  plst sidecar report [--json]  context attribution for the active session
  plst sidecar probe [--force]  read the harness's own /context accounting and cache it
  plst sidecar install          register the hooks in the harness settings
  plst sidecar events [-n N]    tail the raw event log
  plst sidecar emit             hook target; reads hook JSON on stdin, appends one event
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
	// watch is what start was called before, and is still what the dev loop and
	// a good deal of shell history type. Kept working, out of the usage text.
	case "start", "watch":
		// A window is where the dashboard is put, not a different thing to run,
		// so it is a flag on the one command rather than a command of its own.
		if hasFlag(args, "--window") {
			os.Exit(openWindow(args))
		}
		os.Exit(watch(args))
	// open is what --window was, from before it was a flag.
	case "window", "open":
		os.Exit(openWindow(args))
	case "probe":
		os.Exit(probe(args))
	case "install":
		os.Exit(install(args))
	case manifestFlag:
		os.Exit(manifest())
	case "--version", "version":
		fmt.Println("plst-sidecar " + version)
		os.Exit(0)
	case "--help", "-h", "help":
		fmt.Print(usage)
		os.Exit(0)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

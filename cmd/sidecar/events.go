package main

import (
	"fmt"
	"os"
	"strconv"

	"claude-sidecar/internal/event"
)

// events dumps the raw log. The crudest possible view, and the first one that
// works — useful for confirming a newly registered hook actually fires.
func events(args []string) int {
	n := 40
	for i, a := range args {
		if (a == "-n" || a == "--lines") && i+1 < len(args) {
			if v, err := strconv.Atoi(args[i+1]); err == nil {
				n = v
			}
		}
	}
	evs, err := event.Load(event.LogPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "sidecar:", err)
		return 1
	}
	if len(evs) == 0 {
		fmt.Fprintf(os.Stderr, "no events yet at %s — run `sidecar install`\n", event.LogPath())
		return 0
	}
	if n < len(evs) {
		evs = evs[len(evs)-n:]
	}
	for _, ev := range evs {
		fmt.Println(ev)
	}
	return 0
}

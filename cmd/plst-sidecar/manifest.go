package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// manifestFlag is how plst asks a module what it offers. A flag rather than a
// subcommand, so it can never collide with a command this module wants to own.
const manifestFlag = "--plst-manifest"

// manifest tells plst what this module is, so `plst` can list it without having
// to know anything about it.
//
// stdout and nothing else: plst reads this as JSON, so a stray line of
// diagnostics here would be a parse failure rather than a warning.
func manifest() int {
	out := struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Version     string `json:"version"`
		Commands    []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"commands"`
	}{
		Name:        "sidecar",
		Description: "agent context debugger",
		Version:     version,
	}
	for _, c := range [][2]string{
		{"start", "open the dashboard in a new window"},
		{"watch", "dashboard in this terminal"},
		{"report", "context attribution for the active session"},
		{"probe", "read the harness's own /context accounting"},
		{"install", "register the hooks in the harness settings"},
		{"events", "tail the raw event log"},
		{"emit", "hook target; appends one event"},
	} {
		out.Commands = append(out.Commands, struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}{c[0], c[1]})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return 1
	}
	fmt.Fprintln(os.Stdout, string(b))
	return 0
}

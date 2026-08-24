package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/esmarkowski/plasticity-claude-sidecar/internal/event"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/settings"
)

// install registers the sidecar's hooks in ~/.claude/settings.json.
func install(args []string) int {
	binary, err := installedPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sidecar:", err)
		return 1
	}
	command := binary + " emit"
	path := settings.Path()

	fmt.Printf("hook command   %s\n", command)
	fmt.Printf("settings file  %s\n", path)
	fmt.Printf("event log      %s\n", event.LogPath())
	fmt.Printf("events         %d (%s …)\n\n", len(settings.Events), strings.Join(settings.Events[:4], ", "))

	if !hasFlag(args, "--yes") && !confirm("Register these hooks?") {
		fmt.Println("nothing changed")
		return 0
	}

	added, err := settings.Register(path, command, settings.Events)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sidecar:", err)
		return 1
	}
	if len(added) == 0 {
		fmt.Println("already registered — nothing to do")
		return 0
	}
	fmt.Printf("registered %d events: %s\n", len(added), strings.Join(added, " "))
	fmt.Println("a backup of the previous settings is in ~/.claude/backups/")
	fmt.Println("hooks take effect in sessions started from here on")
	return 0
}

// installedPath resolves where the hook should point: at this binary, wherever
// plst put it.
//
// It has to be an absolute path in the settings file. A hook runs under /bin/sh
// with none of the user's shell activation, so a bare name on PATH is the thing
// that fails with exit 127 — the exact failure this program exists to surface.
// The path is discovered rather than assumed, so installing from a module
// directory, a dev build, or a release all register the binary that actually ran.
func installedPath() (string, error) {
	self, err := selfPath()
	if err != nil {
		return "", err
	}
	// Registering the dev build would leave the hooks pointing into a working
	// tree, which is fine while developing and wrong on a real install. Worth
	// saying rather than silently doing.
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, self); err == nil && !strings.HasPrefix(rel, "..") {
			fmt.Fprintf(os.Stderr, "note: registering the build in this working tree (%s)\n", rel)
		}
	}
	return self, nil
}

// confirm asks with gum when it is available and falls back to a plain prompt.
// gum is a good fit here and nowhere else in this program: a one-shot question
// is exactly what it is built for.
func confirm(question string) bool {
	if gum, err := exec.LookPath("gum"); err == nil {
		cmd := exec.Command(gum, "confirm", question)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run() == nil
	}
	fmt.Printf("%s [y/N] ", question)
	var answer string
	if _, err := fmt.Scanln(&answer); err != nil {
		return false
	}
	return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
}

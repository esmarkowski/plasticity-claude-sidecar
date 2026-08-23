package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"claude-sidecar/internal/event"
	"claude-sidecar/internal/settings"
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

// installedPath resolves where the hook should point. It must be a stable
// absolute path that outlives a rebuild, not the temporary binary in ./bin —
// otherwise `mise run dev` would leave the hooks pointing at a file that moves.
func installedPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(home, ".local", "bin", "sidecar")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("%s does not exist yet — run `mise run install` first", p)
	}
	return p, nil
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

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// openWindow launches the dashboard in its own Ghostty window.
//
// Ghostty on macOS cannot start the emulator from its own CLI — `ghostty
// --help` says so and points at `open -na`. `-e` then runs a command in the new
// window.
func openWindow(args []string) int {
	command, err := windowCommand(hasFlag(args, "--dev"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "sidecar:", err)
		return 1
	}
	cmd := exec.Command("open", "-na", "Ghostty.app", "--args",
		"--title=claude sidecar",
		"--window-width=104",
		"--window-height=46",
		"-e", command,
	)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "sidecar: opening Ghostty:", err)
		return 1
	}
	return 0
}

// windowCommand picks what runs inside the window. In dev mode that is the
// watchexec supervisor rather than the binary, so the window survives every
// rebuild instead of closing when the process it was given exits.
func windowCommand(dev bool) (string, error) {
	if dev {
		root, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("cd %q && mise run dev", root), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin", "sidecar") + " watch --follow", nil
}

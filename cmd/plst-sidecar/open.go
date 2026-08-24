package main

import (
	"fmt"
	"os"
	"os/exec"
)

// openWindow launches the dashboard in its own Ghostty window.
//
// Ghostty on macOS cannot start the emulator from its own CLI — `ghostty --help`
// says so and points at `open -na`. `-e` then runs a command in the new window.
func openWindow(args []string) int {
	command, err := windowCommand(hasFlag(args, "--dev"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "sidecar:", err)
		return 1
	}
	// The command follows -e as separate arguments, not as one string. Ghostty
	// takes what comes after -e as an argv, so a single string containing spaces
	// is a request to run a program whose name has spaces in it.
	argv := append([]string{"-na", "Ghostty.app", "--args",
		"--title=plst sidecar",
		"--window-width=104",
		"--window-height=46",
		"-e"}, command...)

	cmd := exec.Command("open", argv...)
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
func windowCommand(dev bool) ([]string, error) {
	if dev {
		root, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		// A shell, because this is two commands. The path is passed through %q
		// here and only here: it is going into a shell, which is the one place
		// quoting is the caller's job.
		return []string{"/bin/sh", "-c", fmt.Sprintf("cd %q && mise run dev", root)}, nil
	}
	self, err := selfPath()
	if err != nil {
		return nil, err
	}
	return []string{self, "watch", "--follow"}, nil
}

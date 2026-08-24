package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// selfPath is where this binary is, resolved rather than assumed.
//
// Nothing here hardcodes an install location. A module is put wherever plst was
// configured to put it, so the only thing that reliably knows where this binary
// lives is this binary — and the two places that need a path to write down, the
// hook command and the window command, both need one that will still be right
// after plst moves or updates the module.
//
// Symlinks are resolved because ~/.local/bin is commonly a link farm, and a hook
// pointing at the link keeps working only as long as the link does.
func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot determine where this binary is: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Abs(exe)
}

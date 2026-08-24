package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/esmarkowski/plasticity-claude-sidecar/internal/event"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/ui"
)

// watch runs the live dashboard.
func watch(args []string) int {
	follow, sess, state := hasFlag(args, "--follow"), flagValue(args, "--session"), loadUIState()

	if hasFlag(args, "--once") {
		// One frame to stdout. Renders the same view the live dashboard does,
		// which makes the layout checkable without a terminal attached.
		w, h := termSize()
		if v := flagValue(args, "--width"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				w = n
			}
		}
		if v := flagValue(args, "--tab"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 1 {
				state.Tab = n - 1
			}
		}
		fmt.Println(ui.Render(follow, sess, state, w, h, hasFlag(args, "--picker")))
		return 0
	}

	model := ui.New(follow, sess, state)

	w, err := ui.Watch()
	if err != nil {
		// A missing watcher is survivable: the two-second poll still refreshes,
		// just less promptly. Not worth refusing to start over.
		fmt.Fprintln(os.Stderr, "sidecar: file watching unavailable:", err)
	} else {
		defer w.Close()
		model.Attach(w)
	}

	// tea.WithoutSignalHandler makes this the single owner of shutdown. Bubble
	// Tea handles SIGINT itself but not SIGTERM, and SIGTERM is exactly what the
	// hot-reload loop sends on every rebuild — left untrapped, the alt-screen
	// and raw-mode escapes never get undone and the terminal is wrecked.
	p := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithoutSignalHandler(),
		// Cell motion rather than all motion: clicks and the wheel are all the
		// dashboard reads, and all-motion floods the program with a message per
		// pointer move for nothing.
		tea.WithMouseCellMotion(),
	)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigs
		p.Quit()
	}()

	final, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sidecar:", err)
		return 1
	}
	if m, ok := final.(ui.Model); ok {
		saveUIState(m.SaveState())
	}
	return 0
}

// uiStatePath persists which tab and session you were looking at. This is what
// makes a rebuild feel like a reload rather than an interruption.
func uiStatePath() string { return filepath.Join(event.Dir(), "uistate.json") }

func loadUIState() ui.State {
	var s ui.State
	b, err := os.ReadFile(uiStatePath())
	if err != nil {
		return s
	}
	_ = json.Unmarshal(b, &s)
	return s
}

func saveUIState(s ui.State) {
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	if err := os.MkdirAll(event.Dir(), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(uiStatePath(), b, 0o600)
}

// termSize is a best effort for the one-shot render: a wide-enough default when
// there is no terminal to ask.
func termSize() (int, int) {
	w, h := 104, 46
	if v := os.Getenv("COLUMNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 40 {
			w = n
		}
	}
	if v := os.Getenv("LINES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 10 {
			h = n
		}
	}
	return w, h
}

func flagValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

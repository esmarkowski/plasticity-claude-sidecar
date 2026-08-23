package ui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"claude-sidecar/internal/transcript"
)

func at(min int) time.Time {
	return time.Date(2026, 8, 23, 14, min, 0, 0, time.UTC)
}

func run(name string, exit int, min int) transcript.HookRun {
	return transcript.HookRun{Name: name, ExitCode: exit, TS: at(min), Failed: exit != 0}
}

// A transcript keeps a whole session's history, so the fix has to be able to
// retire the failures that came before it.
func TestGroupFailuresResolvesAfterACleanRun(t *testing.T) {
	hooks := []transcript.HookRun{
		run("PreToolUse:Bash", 127, 10),
		run("PreToolUse:Bash", 127, 11),
		run("SessionStart:startup", 127, 12),
		run("PreToolUse:Bash", 0, 22),
	}

	got := groupFailures(hooks)
	if len(got) != 2 {
		t.Fatalf("got %d groups, want 2: %+v", len(got), got)
	}

	// Still broken sorts first, whatever the counts say.
	if got[0].Name != "SessionStart:startup" || got[0].Resolved {
		t.Errorf("unresolved failure did not lead: %+v", got[0])
	}
	fixed := got[1]
	if !fixed.Resolved || fixed.Count != 2 {
		t.Errorf("fixed hook not resolved with its history intact: %+v", fixed)
	}
	if !fixed.Since.Equal(at(22)) {
		t.Errorf("Since = %v, want the first clean run", fixed.Since)
	}
	if !fixed.Last.Equal(at(11)) {
		t.Errorf("Last = %v, want the last failing run", fixed.Last)
	}
}

// A clean run only counts if it came after the failure. Otherwise a hook that
// worked once and then broke would report itself fixed.
func TestGroupFailuresIgnoresEarlierCleanRuns(t *testing.T) {
	got := groupFailures([]transcript.HookRun{
		run("Stop", 0, 10),
		run("Stop", 1, 11),
	})
	if len(got) != 1 || got[0].Resolved {
		t.Fatalf("a clean run before the failure resolved it: %+v", got)
	}
}

// Failing a second way is a second problem: the exit codes are separate rows,
// and a clean run answers both.
func TestGroupFailuresSeparatesExitCodes(t *testing.T) {
	got := groupFailures([]transcript.HookRun{
		run("PreToolUse:Bash", 127, 10),
		run("PreToolUse:Bash", 2, 11),
		run("PreToolUse:Bash", 0, 12),
	})
	if len(got) != 2 {
		t.Fatalf("got %d groups, want one per exit code: %+v", len(got), got)
	}
	for _, f := range got {
		if !f.Resolved {
			t.Errorf("exit %d not resolved by the clean run: %+v", f.ExitCode, f)
		}
	}
}

// The stop summary records a command and a duration but no hook name. Reading
// those as clean runs would clear a failure because some other hook succeeded.
func TestGroupFailuresIgnoresAnonymousCleanRuns(t *testing.T) {
	got := groupFailures([]transcript.HookRun{
		run("Stop", 1, 10),
		{Event: "Stop", Command: "/usr/local/bin/sidecar emit", DurationMS: 17, TS: at(11)},
	})
	if len(got) != 1 || got[0].Resolved {
		t.Fatalf("an unnamed run resolved a named failure: %+v", got)
	}
}

// Dismissing is for the hook that was removed from settings.json: it will never
// run again, so nothing will ever prove it fixed.
func TestDismissHidesTheFailureAndTheBadge(t *testing.T) {
	m := Model{tab: TabHooks, hooks: []transcript.HookRun{
		run("SessionStart:startup", 127, 10),
		run("SessionStart:startup", 127, 11),
	}}
	if m.failingHooks() != 1 {
		t.Fatalf("badge = %d before dismissing, want 1", m.failingHooks())
	}

	m.dismissed = m.dismissAll()
	current, _, dismissed := m.failureGroups()
	if len(current) != 0 || dismissed != 1 {
		t.Fatalf("after dismissing: %d current, %d dismissed", len(current), dismissed)
	}
	if m.failingHooks() != 0 {
		t.Errorf("badge = %d after dismissing, want 0", m.failingHooks())
	}

	// A dismissal is a watermark, not a delete. The same hook failing again is a
	// new failure and has to come back on its own.
	m.hooks = append(m.hooks, run("SessionStart:startup", 127, 30))
	if m.failingHooks() != 1 {
		t.Errorf("a failure after the dismissal stayed hidden")
	}
}

// Restoring is the way back from dismissing something that was not actually
// fixed, so it has to bring the badge back too.
func TestRestoreBringsDismissedFailuresBack(t *testing.T) {
	m := Model{tab: TabHooks, offset: map[Tab]int{}, cursor: map[Tab]int{}, collapsed: map[rowRef]bool{}, vp: newViewport(),
		hooks: []transcript.HookRun{run("Stop", 1, 10)}}

	dismissed, _ := m.key(keyOf("x"))
	m = dismissed.(Model)
	if m.failingHooks() != 0 {
		t.Fatalf("x did not dismiss")
	}

	restored, _ := m.key(keyOf("X"))
	m = restored.(Model)
	if m.failingHooks() != 1 {
		t.Errorf("X did not restore: badge = %d", m.failingHooks())
	}
}

// Dismissing is scoped to the tab that shows the failures; x elsewhere is a
// keystroke that would silently clear a badge the user was not looking at.
func TestDismissOnlyAppliesOnTheHooksTab(t *testing.T) {
	m := Model{tab: TabContext, offset: map[Tab]int{}, cursor: map[Tab]int{}, collapsed: map[rowRef]bool{}, vp: newViewport(),
		hooks: []transcript.HookRun{run("Stop", 1, 10)}}
	updated, _ := m.key(keyOf("x"))
	if updated.(Model).failingHooks() != 1 {
		t.Error("x cleared failures from another tab")
	}
}

// A dismissal has to survive the rebuild that follows fixing the hook.
func TestDismissalsRoundTripThroughState(t *testing.T) {
	m := Model{tab: TabHooks, offset: map[Tab]int{}, cursor: map[Tab]int{}, collapsed: map[rowRef]bool{}, vp: newViewport(),
		hooks: []transcript.HookRun{run("Stop", 1, 10)}}
	m.dismissed = m.dismissAll()

	revived := New(false, "", m.SaveState())
	revived.hooks = m.hooks
	if revived.failingHooks() != 0 {
		t.Error("dismissal did not survive a restart")
	}
}

// keyOf builds the keypress the dashboard would see for a plain character.
func keyOf(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

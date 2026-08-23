package ui

import "errors"

// errNoSessions is shown as guidance rather than a failure: a fresh install has
// no events and no transcripts, and that is a normal state to start in.
var errNoSessions = errors.New("no Claude Code sessions found yet")

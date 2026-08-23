# claude-sidecar

A debugging companion for Claude Code. It answers one question the harness does
not: **what is actually filling the context window right now, and why.**

```
sidecar open --dev     # live dashboard in its own Ghostty window, hot-reloading
sidecar open           # same, without the rebuild loop
sidecar watch --follow # in the current terminal
sidecar report --detail
sidecar report --audit # per-request residual — the engine's own regression check
sidecar probe          # read Claude Code's /context accounting and cache it
sidecar install        # register the hooks in ~/.claude/settings.json
sidecar events -n 20   # raw hook log
```

## How it gets its numbers

Three sources, because no one of them is sufficient.

**The transcript** (`~/.claude/projects/<slug>/<session>.jsonl`) has every
message and, on each assistant line, the `usage` the API returned. That usage is
the only exact number in the system: `input_tokens + cache_read +
cache_creation` is the current size of the window. Everything else is
reconciled to it.

Reading that file top to bottom overcounts, though. It is append-only, so it
accumulates rewound branches and pre-compaction history the model can no longer
see. Following `parentUuid` backwards from the newest message counts exactly
what is live — and gets compaction right for free, because a `compact_boundary`
line has a nil parent, so the walk simply stops there.

**The `InstructionsLoaded` hook**, because CLAUDE.md and `.claude/rules/*.md`
content never appears in the transcript at all. `grep -c claudeMd` on a real
session returns 0. The hook is the only signal that a rule was injected, and it
carries the load reason — so a rule pulled in by a glob match shows up with the
file that triggered it, and with a count, since a broad glob is charged again on
every match.

**Claude Code's own `/context`**, via `sidecar probe`. It reports the system
prompt, the tool schemas, and per-file memory and per-skill costs from the
harness's own tokenizer. Those are the largest and least visible part of the
window. Without a probe they have to be inferred by subtraction and collapse
into one opaque row; with one, they are measured.

## Why the estimates are trustworthy

Claude's tokenizer is not public, so message text is estimated from character
density — and that estimate was wrong by 35% in the first cut, in the flattering
direction. `sidecar report --audit` is what found it: it plots, per request,
what we could measure against what the API billed. The system prompt does not
change during a session, so if attribution were complete that residual would be
flat. It climbed.

So the correction is fitted rather than tuned. Each request is one observation
of `context = base + scale × measured + thinking`; least squares over a session
recovers `scale`, with `base` taken from the probe when there is one. Buckets
with an exact source — the probe's constants, reasoning tokens from `usage` —
are never rescaled. The header always shows the real total.

Fitting the session two ways also settled a question worth not guessing about:
reasoning tokens **accumulate** in the window rather than being dropped per
turn. Treating them as cumulative fits better (R² 0.998 vs 0.997) and recovers a
constant matching what `/context` independently reports.

## Architecture

```
hook fires ──► sidecar emit ──► ~/.claude/sidecar/events.jsonl ──fsnotify──► sidecar watch
               (~2ms, one O_APPEND write, then exit 0)
```

No daemon, no socket, no gRPC. The emitter's only job is a single `O_APPEND`
write, which is atomic at this size, so the ~20 hook types firing in parallel
cannot interleave. Nothing has to be listening, so a crashed dashboard can never
stall a turn.

Two invariants in `emit`, both about not harming the session that invoked it:
it never writes to stdout (hook stdout is injected into the model's context on
several events) and it always exits 0 (a non-zero exit costs the user context to
read as a `hook_non_blocking_error`). Both are pinned by tests, against
malformed input, empty input, and an unwritable log directory.

It is a single static Go binary invoked by absolute path for a reason: hooks run
under `/bin/sh` with none of the user's shell activation. Two node-based hooks on
this machine were failing silently with `exit 127: node: command not found`,
which is exactly the kind of thing the Hooks tab surfaces.

## Hot reload

The emitter needs no reload — hooks exec it fresh every time, so a rebuild is
picked up by the next hook. The dashboard is a pure reader, so restarting it
replays the log and lands in the same place. `mise run dev` rebuilds and
restarts on save; SIGTERM is trapped so the alt-screen and raw-mode escapes are
undone (Bubble Tea traps SIGINT but not SIGTERM, and SIGTERM is what the loop
sends), and the active tab and scroll position are persisted across the bounce.

A failed build leaves the previous binary running with the error printed above
it, so a typo never costs you the window.

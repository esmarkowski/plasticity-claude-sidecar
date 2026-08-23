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

A transcript is a whole session's history, so fixing the hook does not erase the
failures it already logged. Two things retire them. A clean run of the same hook
resolves its earlier failures automatically — the hook proves the fix by running
— and they move to a Resolved list, which is worth keeping visible: forty
failures now passing is a different story from never having failed. For the hook
that was deleted from `settings.json` and will never run again to prove anything,
`x` dismisses the list. A dismissal is a watermark, not a delete, so the same
hook failing again afterwards comes back on its own; `X` restores.

## Moving around

Every tab that draws rows has a cursor. `j`/`k` move it, `←`/`→` shut and open the
row's breakdown, and `enter` acts on the row — what that means depends on the row,
and the footer names it: a category on the context tab jumps to the tab that
breaks it down, a hook failure is dismissed. `tab` and `shift+tab` move between
tabs, which is why the arrows do not: a tree needs them more than the chip row
does. On a tab with no rows to select, `j`/`k` fall back to scrolling.

The body is a `bubbles/viewport`, so the mouse wheel scrolls it and `pgup`/`pgdn`
page it. The viewport follows the cursor rather than the other way round, but only
when the cursor has just moved — following it on every refresh dragged the view
back to wherever the cursor was parked, so scrolling a long tab lasted one
two-second tick. The chip row is hand-rolled — bubbles ships
list, table, and viewport, none of which is a row of tabs — and records where each
chip landed so a click can be tested against it. Each tab keeps its own scroll
offset, since one viewport is shared by all of them.

Four kinds of row expand, and all of them start shut: a breakdown is detail asked
for, and every row volunteering its own made the tools tab forty lines of things
nobody had asked about. A `▸` marks a row that has something in it. Nested bars
are faded toward the background, because the bar on the parent row is the one
making the point and the ones under it are its parts.

A tool shows the programs behind it. A category on the context tab has its own
tab. An agent shows its own context composition — the bar beside it was already
that composition, drawn but unlabelled — and the full task where the column cut it
off. A request on the timeline shows what landed in the window since the previous
one, named after the call that caused it rather than the tool that made it:
`Read user.rb`, not `Read`. The largest of those is a column of its own, so a row
reads as "request 107 grew 421 tokens, and it was an Edit to routes.rb" without
expanding anything. Where the jump is larger than everything that can be named,
the difference is a row called `unexplained`, because that tab exists to show the
residual and a breakdown quietly explaining a third of a jump would hide it.

Column widths are sized from the top-level names only, never from the nested ones.
Sizing them to everything meant the column grew the first time a long command
appeared under Bash, and every right-aligned number in the table stepped sideways
on the next two-second refresh.

Bash is the one tool name that covers everything, so its calls are broken down by
the program that ran. Wrappers are followed through to the real command —
`mise exec -- bundle exec rspec` is filed under rspec, and `cd app && bin/rails
test` under bin/rails — because a project that runs everything through mise would
otherwise report a single row for most of a session. The parent's bar is drawn in
segments, one shade per part, and each part's own bar is drawn at the same scale,
so the segment and the row are the same width.

## Hot reload

The emitter needs no reload — hooks exec it fresh every time, so a rebuild is
picked up by the next hook. The dashboard is a pure reader, so restarting it
replays the log and lands in the same place. `mise run dev` rebuilds and
restarts on save; SIGTERM is trapped so the alt-screen and raw-mode escapes are
undone (Bubble Tea traps SIGINT but not SIGTERM, and SIGTERM is what the loop
sends), and the active tab, scroll position, cursor, and folded rows are all
persisted across the bounce.

A failed build leaves the previous binary running with the error printed above
it, so a typo never costs you the window.

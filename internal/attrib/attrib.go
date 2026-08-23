package attrib

import (
	"encoding/json"
	"os"
	"sort"
	"strings"

	"claude-sidecar/internal/event"
	"claude-sidecar/internal/harness"
	"claude-sidecar/internal/transcript"
)

// Bucket names a category of text in the context window. Order here is the
// order the dashboard lists them in when sizes tie.
type Bucket string

const (
	BucketSystem       Bucket = "system + tools"
	BucketRules        Bucket = "rules & memory"
	BucketSkills       Bucket = "skill listing"
	BucketAgents       Bucket = "agent listing"
	BucketToolDeltas   Bucket = "tool schema deltas"
	BucketUser         Bucket = "user messages"
	BucketAssistant    Bucket = "assistant text"
	BucketThinking     Bucket = "thinking"
	BucketToolCalls    Bucket = "tool calls"
	BucketToolResults  Bucket = "tool results"
	BucketHookOutput   Bucket = "hook injections"
	BucketReminders    Bucket = "reminders & attachments"
	BucketUnattributed Bucket = "unattributed"
)

// exactBucket marks the buckets whose token counts come from a source that
// already counted tokens — the harness's own /context accounting, or the API's
// usage field — and so must not be rescaled by a character-density correction.
var exactBucket = map[Bucket]bool{
	BucketSystem:       true,
	BucketThinking:     true,
	BucketSkills:       true,
	BucketAgents:       true,
	BucketRules:        true,
	BucketUnattributed: true,
}

// attachmentBucket maps a transcript attachment type onto a bucket. Anything
// not listed falls into reminders, which is the right default: unknown
// attachments are injected notices.
var attachmentBucket = map[string]Bucket{
	"hook_success":            BucketHookOutput,
	"hook_non_blocking_error": BucketHookOutput,
	"skill_listing":           BucketSkills,
	"invoked_skills":          BucketSkills,
	"agent_listing_delta":     BucketAgents,
	"deferred_tools_delta":    BucketToolDeltas,
	"mcp_instructions_delta":  BucketToolDeltas,
}

// Slice is one row of the breakdown.
type Slice struct {
	Bucket Bucket
	Tokens int
	// Detail holds the per-item breakdown behind a bucket: tool names for tool
	// results, file paths for rules.
	Detail []Item
}

// Item is one contributor inside a bucket.
type Item struct {
	Name   string
	Tokens int
	Count  int
	Note   string
	// Children break the item into the parts that make it up, largest first,
	// and always sum back to Tokens. Populated where one name covers work of
	// several kinds — Bash, whose calls are every program on the machine.
	Children []Item
}

// Report is a full attribution of one session's context window.
type Report struct {
	Session   string
	CWD       string
	Branch    string
	Model     string
	PermMode  string
	Effort    string
	Title     string
	Usage     transcript.Usage
	Total     int // real context size; the one exact number
	Slices    []Slice
	Turns     int
	Compacted bool
	PreTokens int
	// Estimated is the sum of what we could measure from the transcript, before
	// the residual is folded in. Kept so the UI can be honest about coverage.
	Estimated int
	// ThinkingBlocks counts reasoning blocks in context whose text the
	// transcript does not retain.
	ThinkingBlocks int
	// Window is the model's context limit, when known.
	Window int
	// Scale is the fitted correction applied to estimated buckets, and R2 how
	// well that fit describes the session. Surfaced rather than hidden so a bad
	// fit is visible.
	Scale float64
	R2    float64
	// Probed records whether the static half came from the harness's own
	// numbers rather than being inferred by subtraction.
	Probed   bool
	ProbedAt string
}

// thinkingTotal sums reasoning tokens across every request still in the chain,
// deduplicated by request id — usage is repeated on each content block of one
// response, so summing lines would multiply by the reply's block count.
func thinkingTotal(chain []transcript.Line) int {
	seen := map[string]bool{}
	total := 0
	for _, l := range chain {
		if l.Message == nil || l.Message.Usage == nil {
			continue
		}
		key := l.RequestID
		if key == "" {
			key = l.MessageID
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		total += l.Message.Usage.Details.ThinkingTokens
	}
	return total
}

// Analyze attributes a session's context window across buckets.
//
// events supplies what the transcript cannot: which rule files the harness
// injected. CLAUDE.md content never appears in the transcript, so without the
// InstructionsLoaded hook that cost is invisible.
func Analyze(lines []transcript.Line, events []event.Event) Report {
	return AnalyzeWith(lines, events, harness.Snapshot{})
}

// AnalyzeWith is Analyze given the harness's own accounting of what it loaded.
//
// When a snapshot is available the static half of the window — system prompt,
// tool schemas, memory files, skills, custom agents — comes from Claude Code's
// own tokenizer instead of being inferred by subtraction. That turns the
// largest and least certain part of the breakdown into a measurement, and it
// itemizes memory files and skills individually, which no amount of transcript
// reading could do.
func AnalyzeWith(lines []transcript.Line, events []event.Event, snap harness.Snapshot) Report {
	chain := transcript.Chain(lines)
	usage, model, _ := transcript.LatestUsage(chain)

	r := Report{
		Model: model,
		Usage: usage,
		Total: usage.ContextTokens(),
		Turns: len(transcript.Requests(chain)),
		// Titles are recorded on their own lines, outside the message chain, so
		// they come from the whole file rather than from what is still in
		// context.
		Title: transcript.Title(lines),
	}

	tally := map[Bucket]int{}
	detail := map[Bucket]map[string]*Item{}
	add := func(b Bucket, name string, tokens int) {
		tally[b] += tokens
		if name == "" {
			return
		}
		if detail[b] == nil {
			detail[b] = map[string]*Item{}
		}
		it, ok := detail[b][name]
		if !ok {
			it = &Item{Name: name}
			detail[b][name] = it
		}
		it.Tokens += tokens
		it.Count++
	}

	// Sub-items are kept beside the tally rather than on the Item, because an
	// Item is handed out by value and these have to keep accumulating.
	kids := map[string]map[string]*Item{}
	addWithin := func(b Bucket, name, child string, tokens int) {
		add(b, name, tokens)
		if name == "" || child == "" {
			return
		}
		key := childKey(b, name)
		if kids[key] == nil {
			kids[key] = map[string]*Item{}
		}
		c, ok := kids[key][child]
		if !ok {
			c = &Item{Name: child}
			kids[key][child] = c
		}
		c.Tokens += tokens
		c.Count++
	}

	toolByID := map[string]string{}
	familyByID := map[string]string{}
	thinkingBlocks := 0

	for _, l := range chain {
		switch {
		case l.Type == "system" && l.Subtype == "compact_boundary":
			r.Compacted = true
			if l.Compact != nil {
				r.PreTokens = l.Compact.PreTokens
			}

		case l.Attachment != nil:
			kind, _ := l.Attachment["type"].(string)
			b, ok := attachmentBucket[kind]
			if !ok {
				b = BucketReminders
			}
			// With a harness snapshot these listings are already counted, and
			// more accurately. Counting the attachment too would double them.
			if snap.OK() && (b == BucketSkills || b == BucketAgents) {
				continue
			}
			add(b, attachmentLabel(l.Attachment, kind), Estimate(attachmentText(l.Attachment, kind)))

		case l.Message != nil:
			if l.CWD != "" {
				r.CWD = l.CWD
			}
			if l.GitBranch != "" {
				r.Branch = l.GitBranch
			}
			if l.SessionID != "" {
				r.Session = l.SessionID
			}
			for _, blk := range l.Message.Blocks() {
				switch blk.Type {
				case "thinking":
					// Claude Code strips the reasoning text before writing the
					// transcript, so there is nothing here to measure. Thinking
					// is accounted for from usage instead, in thinkingTokens.
					thinkingBlocks++
				case "text":
					if l.Message.Role == "user" {
						add(BucketUser, "", Estimate(blk.Text))
					} else {
						add(BucketAssistant, "", Estimate(blk.Text))
					}
				case "tool_use":
					toolByID[blk.ID] = blk.Name
					familyByID[blk.ID] = toolFamily(blk.Name, blk.Input)
					addWithin(BucketToolCalls, blk.Name, familyByID[blk.ID],
						Estimate(string(blk.Input)))
				case "tool_result":
					name := toolByID[blk.ToolUseID]
					if name == "" {
						name = "(unknown tool)"
					}
					// Credited to the same family as the call it answers: what a
					// command cost is mostly what came back from it.
					addWithin(BucketToolResults, name, familyByID[blk.ToolUseID],
						Estimate(resultText(blk)))
				}
			}
		}
	}

	// Thinking is the one bucket with an exact source and no text: the API bills
	// it per request in output_tokens_details, and the transcript keeps only an
	// opaque signature where the reasoning was.
	//
	// It accumulates. Reasoning from earlier turns stays in the window, which is
	// not obvious and was worth checking rather than assuming: fitting the
	// session's requests both ways, treating thinking as cumulative fits better
	// (R² 0.998 vs 0.997) and recovers a constant matching what /context
	// independently reports for the system prompt and tool schemas.
	if t := thinkingTotal(chain); t > 0 {
		add(BucketThinking, "", t)
	}
	r.ThinkingBlocks = thinkingBlocks

	for _, it := range memory(events, r.Session, snap) {
		add(BucketRules, it.Name, it.Tokens)
		if d := detail[BucketRules][it.Name]; d != nil {
			d.Count = maxInt(it.Count, 1)
			d.Note = it.Note
		}
	}

	if snap.OK() {
		r.Probed = true
		r.ProbedAt = snap.ProbedAt.Local().Format("Jan 2 15:04")
		r.Window = snap.Window
		// The system prompt and tool schemas are constants the transcript never
		// records; the snapshot is the only direct measurement of them.
		add(BucketSystem, "system prompt", snap.Categories[harness.CatSystemPrompt])
		add(BucketSystem, "tool schemas", snap.Categories[harness.CatSystemTools])
		add(BucketSystem, "deferred tool names", snap.Categories[harness.CatDeferredTools])
		for _, it := range snap.Agents {
			add(BucketAgents, it.Name, it.Tokens)
			if d := detail[BucketAgents][it.Name]; d != nil {
				d.Note = it.Source
			}
		}
		for _, it := range snap.Skills {
			add(BucketSkills, it.Name, it.Tokens)
			if d := detail[BucketSkills][it.Name]; d != nil {
				d.Note = it.Source
			}
		}
	}

	// Scale the estimated buckets onto real tokens.
	//
	// Character-density estimates are systematically off, and by a consistent
	// factor within a session, so the factor can be recovered from the session's
	// own request history instead of being tuned by hand. Buckets with an exact
	// source — the probe's constants, reasoning tokens from usage — are left
	// alone; scaling a measurement would only add error.
	if scale, ok := fitScale(chain, events, snap); ok {
		r.Scale = scale
		for b := range tally {
			if exactBucket[b] {
				continue
			}
			tally[b] = int(float64(tally[b]) * scale)
		}
	}

	for _, n := range tally {
		r.Estimated += n
	}

	// Whatever is left over after everything measurable is accounted for.
	// Without a snapshot this is dominated by the system prompt and tool
	// schemas, so it is labelled as such; with one, those are already counted
	// and the remainder is genuinely unexplained and should stay small.
	residual := r.Total - r.Estimated
	switch {
	case residual > 0 && r.Probed:
		tally[BucketUnattributed] = residual
	case residual > 0:
		tally[BucketSystem] = residual
	case residual < 0:
		// Over-estimated. Absorb the overshoot in the estimated buckets only:
		// the exact ones are measurements, and shrinking them to make the
		// arithmetic work would be fabricating a number.
		estimated := 0
		for b, n := range tally {
			if !exactBucket[b] {
				estimated += n
			}
		}
		if estimated > 0 {
			shrink := float64(estimated+residual) / float64(estimated)
			if shrink < 0 {
				shrink = 0
			}
			for b, n := range tally {
				if !exactBucket[b] {
					tally[b] = int(float64(n) * shrink)
				}
			}
		}
	}

	for b, n := range tally {
		if n <= 0 {
			continue
		}
		s := Slice{Bucket: b, Tokens: n}
		for _, it := range detail[b] {
			item := *it
			item.Children = rankChildren(kids[childKey(b, it.Name)], item.Tokens)
			s.Detail = append(s.Detail, item)
		}
		sort.Slice(s.Detail, func(i, j int) bool { return s.Detail[i].Tokens > s.Detail[j].Tokens })
		r.Slices = append(r.Slices, s)
	}
	sort.Slice(r.Slices, func(i, j int) bool { return r.Slices[i].Tokens > r.Slices[j].Tokens })
	return r
}

// resultText renders a tool result as the text the model saw. Results arrive
// either as a plain string or as an array of content blocks.
func resultText(b transcript.Block) string {
	if len(b.Content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(b.Content, &s); err == nil {
		return s
	}
	var v any
	if err := json.Unmarshal(b.Content, &v); err == nil {
		return Strings(v)
	}
	return string(b.Content)
}

// memory sizes the instruction files in context.
//
// Two sources, and both are needed. A harness snapshot has exact per-file counts
// but only for the files loaded at startup. The InstructionsLoaded hook catches
// the rest: a .claude/rules file pulled in mid-session because a glob matched
// the file being edited, which a startup probe cannot know about and the
// transcript never records.
func memory(events []event.Event, session string, snap harness.Snapshot) []Item {
	fromSnapshot := map[string]bool{}
	var out []Item
	for _, it := range snap.Memory {
		fromSnapshot[it.Name] = true
		out = append(out, Item{Name: it.Name, Tokens: it.Tokens, Count: 1, Note: it.Source + " startup"})
	}
	for _, it := range hookRules(events, session) {
		if fromSnapshot[it.Name] {
			continue
		}
		out = append(out, it)
	}
	return out
}

// hookRules sizes instruction files by reading them off disk, using the
// InstructionsLoaded hook to learn which files and why.
func hookRules(events []event.Event, session string) []Item {
	byPath := map[string]*Item{}
	var order []string
	for _, ev := range events {
		if ev.Event != "InstructionsLoaded" {
			continue
		}
		if session != "" && ev.Session != session {
			continue
		}
		path, _ := ev.Detail["file_path"].(string)
		if path == "" {
			continue
		}
		it, ok := byPath[path]
		if !ok {
			it = &Item{Name: path}
			byPath[path] = it
			order = append(order, path)
			if b, err := os.ReadFile(path); err == nil {
				it.Tokens = Estimate(string(b))
			}
		}
		it.Count++
		reason, _ := ev.Detail["load_reason"].(string)
		kind, _ := ev.Detail["memory_type"].(string)
		it.Note = strings.TrimSpace(kind + " " + reason)
	}
	out := make([]Item, 0, len(order))
	for _, p := range order {
		it := byPath[p]
		// A glob-matched rule is re-injected on every match, and each injection
		// is paid for again. Charging only once would hide the cost of a rule
		// whose globs are too broad.
		it.Tokens *= it.Count
		out = append(out, *it)
	}
	return out
}

// fitScale recovers the estimator's correction factor for this session,
// preferring the probe's measured constant over an inferred one, and falling
// back to a previously persisted fit when the session is too young to fit.
func fitScale(chain []transcript.Line, events []event.Event, snap harness.Snapshot) (float64, bool) {
	points := auditChain(chain, snap)
	if snap.OK() {
		if scale, ok := FitScale(points, snap.Static()); ok {
			return scale, true
		}
	}
	if c, ok := Fit(points); ok {
		return c.Scale, true
	}
	_, model, _ := transcript.LatestUsage(chain)
	if c, ok := LoadCalibration(model); ok {
		return c.Scale, true
	}
	return 0, false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// injectedField names, per attachment type, the single field whose text actually
// reaches the model.
//
// Summing every string in the attachment is the right default — the payload key
// differs by type and new types appear regularly — but it is wrong wherever an
// attachment repeats itself. A hook_success carries the same text in both
// `content` and `stdout`, so the default would charge a hook twice for one
// injection.
var injectedField = map[string]string{
	"hook_success": "content",
}

// attachmentText returns the text an attachment contributed to context.
func attachmentText(att map[string]any, kind string) string {
	if field, ok := injectedField[kind]; ok {
		s, _ := att[field].(string)
		return s
	}
	if kind == "hook_non_blocking_error" {
		// A failed hook injects the notice about it, not the whole of stderr,
		// and certainly not the stdout it never got to produce.
		s, _ := att["stderr"].(string)
		return s
	}
	return Strings(map[string]any(att))
}

// attachmentLabel names the drill-down row. For hook attachments that is the
// hook itself, so the breakdown says which hook is costing tokens rather than
// just that some hook is.
func attachmentLabel(att map[string]any, kind string) string {
	switch kind {
	case "hook_success", "hook_non_blocking_error":
		if name, _ := att["hookName"].(string); name != "" {
			return name
		}
	}
	return kind
}

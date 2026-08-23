// Package transcript reads Claude Code's session JSONL and works out which of
// its lines are actually in the model's context right now.
//
// The distinction matters more than it sounds. The file is append-only, so it
// accumulates rewound branches and pre-compaction history that the model can no
// longer see. Reading it top to bottom overcounts; following the parent chain
// backwards from the newest message counts exactly what is live.
package transcript

import (
	"bufio"
	"encoding/json"
	"os"
	"slices"
	"time"
)

// Usage is the token accounting the API returned for one request. This is the
// only ground truth in the whole program — every estimate is reconciled to it.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	Details                  struct {
		ThinkingTokens int `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

// ContextTokens is how much of the window this request occupied: everything the
// API billed as input, whether it was a cache hit or not.
func (u Usage) ContextTokens() int {
	return u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
}

// Block is one content block of a message.
type Block struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Text string `json:"text"`
	// Thinking carries the reasoning text — but Claude Code strips it before
	// writing the transcript, leaving only Signature. So thinking cost has to
	// come from usage.output_tokens_details, never from here.
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// Message is the API message carried by a user or assistant line.
type Message struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *Usage          `json:"usage"`
}

// Blocks normalizes the two shapes message content arrives in: a bare string
// for a typed user prompt, or an array of blocks for everything else.
func (m *Message) Blocks() []Block {
	if m == nil || len(m.Content) == 0 {
		return nil
	}
	var blocks []Block
	if err := json.Unmarshal(m.Content, &blocks); err == nil {
		return blocks
	}
	var text string
	if err := json.Unmarshal(m.Content, &text); err == nil {
		return []Block{{Type: "text", Text: text}}
	}
	return nil
}

// Line is one JSONL record. Most records are messages, but the file is also
// where the harness notes attachments, mode changes, and compaction.
type Line struct {
	Type        string          `json:"type"`
	Subtype     string          `json:"subtype"`
	UUID        string          `json:"uuid"`
	ParentUUID  *string         `json:"parentUuid"`
	SessionID   string          `json:"sessionId"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	Timestamp   time.Time       `json:"timestamp"`
	RequestID   string          `json:"requestId"`
	MessageID   string          `json:"messageId"`
	CWD         string          `json:"cwd"`
	GitBranch   string          `json:"gitBranch"`
	Version     string          `json:"version"`
	Mode        string          `json:"mode"`
	PermMode    string          `json:"permissionMode"`
	AITitle     string          `json:"aiTitle"`
	Message     *Message        `json:"message"`
	Attachment  map[string]any  `json:"attachment"`
	Compact     *CompactMeta    `json:"compactMetadata"`
	Effort      json.RawMessage `json:"effort"`
}

// CompactMeta describes a compaction event. preTokens is a free, exact record
// of how large the window had grown before it was collapsed.
type CompactMeta struct {
	Trigger    string `json:"trigger"`
	PreTokens  int    `json:"preTokens"`
	DurationMS int    `json:"durationMs"`
}

// IsMessage reports whether this line carries something the model saw.
func (l Line) IsMessage() bool { return l.Message != nil }

// Load parses a transcript. Malformed lines are skipped: the newest line is
// routinely half-written while Claude Code is mid-flush, and refusing to render
// because of that would make the dashboard useless exactly when it is wanted.
func Load(path string) ([]Line, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []Line
	sc := bufio.NewScanner(f)
	// A single tool result can be megabytes, and it arrives as one line.
	sc.Buffer(make([]byte, 0, 256<<10), 64<<20)
	for sc.Scan() {
		b := sc.Bytes()
		if len(b) == 0 {
			continue
		}
		var l Line
		if err := json.Unmarshal(b, &l); err != nil {
			continue
		}
		out = append(out, l)
	}
	return out, sc.Err()
}

// Chain returns the lines currently in context, oldest first.
//
// It walks parentUuid backwards from the newest message. That single rule gets
// two things right for free: a rewound branch is skipped, because nothing on it
// is an ancestor of the newest message; and pre-compaction history is dropped,
// because the compact_boundary line has a nil parentUuid — the chain simply
// ends there, which is precisely what the model can still see.
func Chain(lines []Line) []Line {
	byUUID := make(map[string]int, len(lines))
	for i, l := range lines {
		if l.UUID != "" {
			byUUID[l.UUID] = i
		}
	}

	// The file is append-only and chronological, so the last message in it is
	// the live leaf even after a rewind has created a new branch.
	leaf := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].IsMessage() {
			leaf = i
			break
		}
	}
	if leaf < 0 {
		return nil
	}

	inChain := map[string]bool{}
	var idx []int
	for i := leaf; ; {
		inChain[lines[i].UUID] = true
		idx = append(idx, i)
		p := lines[i].ParentUUID
		if p == nil || *p == "" {
			break
		}
		next, ok := byUUID[*p]
		if !ok {
			break
		}
		i = next
	}

	// Attachments and system notices hang off the chain rather than sitting in
	// it, so pick up anything whose parent is a chain member. They are real
	// context — a skill listing is thousands of tokens.
	for i, l := range lines {
		if l.IsMessage() || inChain[l.UUID] {
			continue
		}
		if l.ParentUUID != nil && inChain[*l.ParentUUID] {
			idx = append(idx, i)
		}
	}

	// Sort by file position: oldest first.
	slices.Sort(idx)
	out := make([]Line, 0, len(idx))
	for _, i := range idx {
		out = append(out, lines[i])
	}
	return out
}

// LatestUsage returns the newest request's usage, which is the current size of
// the context window.
//
// Usage is repeated on every content block of one API response, so the same
// numbers appear on several consecutive lines. Anything that sums usage without
// deduplicating by request will overcount by the number of blocks in the reply.
func LatestUsage(lines []Line) (Usage, string, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if l.Message != nil && l.Message.Usage != nil && l.Message.Usage.ContextTokens() > 0 {
			return *l.Message.Usage, l.Message.Model, true
		}
	}
	return Usage{}, "", false
}

// Requests collapses lines into one entry per API request, deduplicated by
// requestId, so output and thinking tokens can be summed without double
// counting.
func Requests(lines []Line) []Usage {
	seen := map[string]bool{}
	var out []Usage
	for _, l := range lines {
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
		out = append(out, *l.Message.Usage)
	}
	return out
}

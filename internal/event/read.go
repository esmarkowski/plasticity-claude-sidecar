package event

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
)

// Load reads the whole event log. The dashboard calls this on start and after
// every change notification; a full re-read is fine because bulky fields were
// never stored, so even a long day of sessions is a few megabytes.
//
// Malformed lines are skipped rather than fatal. A half-written final line is
// the normal case when a hook is mid-write, and losing the newest event for a
// few milliseconds is preferable to refusing to render.
func Load(path string) ([]Event, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return decode(f)
}

func decode(r io.Reader) ([]Event, error) {
	var out []Event
	sc := bufio.NewScanner(r)
	// Events are small, but a long Bash command in a target can push a line
	// past the 64KB default.
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out, sc.Err()
}

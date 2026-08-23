package attrib

import (
	"encoding/json"
	"path"
	"sort"
	"strings"
)

// toolFamily names the sub-kind of a tool call, or "" for a tool that has none.
//
// Bash is the only tool worth splitting, and it is the one that most needs it:
// it is a single name covering every program on the machine, so "600 Bash calls"
// says nothing that "400 git, 80 gh, 40 bin/dev" does not say better.
func toolFamily(tool string, input json.RawMessage) string {
	if tool != "Bash" {
		return ""
	}
	var in struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return ""
	}
	return program(in.Command)
}

// wrappers run another program, and are not the work. `mise exec -- bundle exec
// rspec` costs what rspec costs; filing it under mise would put most of a
// session in this project under a single row, which is the opposite of the point.
//
// The value is the subcommand that makes it a wrapper, empty when the command
// always is. `mise exec` runs something else; `mise run` is the thing being run,
// and its task name is the interesting part. Alternatives are separated by "|".
var wrappers = map[string]string{
	"sudo": "", "env": "", "time": "", "exec": "", "nohup": "", "command": "",
	"builtin": "", "xargs": "", "then": "", "else": "", "do": "", "npx": "",
	"mise": "exec|x", "bundle": "exec", "pnpm": "exec|dlx", "yarn": "dlx",
	"poetry": "run", "uv": "run", "rye": "run", "pipenv": "run",
	"rbenv": "exec", "pyenv": "exec", "asdf": "exec",
	// The command a shell is handed with -c is the one that ran. Splitting on
	// whitespace walks straight into the quoted string, so its first word is
	// already the next field.
	"bash": "-c", "sh": "-c", "zsh": "-c",
}

// housekeeping is shell bookkeeping rather than the work. The program worth
// naming is in the next segment, which is why a whole `cd app && ...` segment is
// skipped instead of reporting `app`.
var housekeeping = map[string]bool{
	"cd": true, "export": true, "set": true, "unset": true, "source": true,
	".": true, "alias": true, "pushd": true, "popd": true,
}

// program names the program a shell command runs: `cd app && bin/rails test` is
// bin/rails, not cd, and `mise exec -- rspec` is rspec, not mise.
//
// Deliberately crude — a label for a bar chart, not a shell parser. Splitting on
// operators without regard for quoting can only mislabel a row, and never the
// first segment, which is where the answer usually is.
func program(cmd string) string {
	for _, seg := range strings.Split(operators.Replace(cmd), "\n") {
		if p := segmentProgram(strings.Fields(seg)); p != "" {
			return p
		}
	}
	return ""
}

// segmentProgram resolves one command segment, following wrappers through to the
// command they wrap.
func segmentProgram(fields []string) string {
	for i := 0; i < len(fields); i++ {
		f := word(fields, i)
		// A separator, or a FOO=bar assignment for whatever follows it.
		if f == "" || f == "--" || strings.ContainsRune(f, '=') {
			continue
		}
		if marker, ok := wrappers[f]; ok {
			if marker == "" {
				continue
			}
			next := i + 1
			for next < len(fields) && (word(fields, next) == "" ||
				strings.ContainsRune(word(fields, next), '=')) {
				next++
			}
			if next >= len(fields) || !oneOf(marker, word(fields, next)) {
				// Used without the subcommand that makes it a wrapper, so this
				// is the program after all.
				return shorten(f)
			}
			i = next
			continue
		}
		// Flags belong to whatever was skipped to get here; a program never
		// starts with one.
		if strings.HasPrefix(f, "-") {
			continue
		}
		if housekeeping[f] {
			return ""
		}
		return shorten(f)
	}
	return ""
}

// word reads a field with the shell punctuation that survives whitespace
// splitting taken off.
func word(fields []string, i int) string {
	return strings.Trim(fields[i], "(){}\"'`")
}

func oneOf(alternatives, s string) bool {
	for _, a := range strings.Split(alternatives, "|") {
		if a == s {
			return true
		}
	}
	return false
}

// operators flattens every way one command line holds more than one command.
// Listed longest-first, since Replacer matches in argument order.
var operators = strings.NewReplacer("||", "\n", "&&", "\n", "|", "\n", ";", "\n")

// shorten keeps the part of a program name that identifies it. An absolute path
// is noise — /opt/homebrew/bin/rg and /usr/bin/rg are both rg — but a relative
// one is how a project spells its own scripts, and bin/dev is not "dev".
func shorten(prog string) string {
	prog = strings.TrimPrefix(prog, "./")
	if strings.HasPrefix(prog, "/") || strings.HasPrefix(prog, "$") || strings.HasPrefix(prog, "~") {
		return path.Base(prog)
	}
	return prog
}

// maxChildren caps a breakdown. Past a handful the shades that tie a row to its
// segment in the bar stop being tellable apart, and the tail is long: a session
// touches dozens of programs and cares about five.
const maxChildren = 6

// childKey identifies the item a set of sub-items belongs to.
func childKey(b Bucket, name string) string { return string(b) + "\x00" + name }

// rankChildren orders a breakdown largest first and folds the tail into "other",
// which also absorbs whatever the parent counted but could not attribute.
//
// The parts sum back to the whole deliberately. A breakdown whose shares add up
// to 80% invites the reader to work out what the other 20% was, and the honest
// answer — commands too small to list, and commands we could not read — is worth
// one row rather than a puzzle.
func rankChildren(kids map[string]*Item, total int) []Item {
	if len(kids) == 0 {
		return nil
	}
	out := make([]Item, 0, len(kids))
	for _, c := range kids {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tokens != out[j].Tokens {
			return out[i].Tokens > out[j].Tokens
		}
		return out[i].Name < out[j].Name
	})

	kept, other := out, Item{Name: "other"}
	if len(out) > maxChildren {
		kept, other.Count = out[:maxChildren-1], 0
		for _, c := range out[maxChildren-1:] {
			other.Tokens += c.Tokens
			other.Count += c.Count
		}
	}
	// Whatever the parent counted that no child claimed: a Bash call whose input
	// would not parse, or a result with no matching call.
	accounted := other.Tokens
	for _, c := range kept {
		accounted += c.Tokens
	}
	if rest := total - accounted; rest > 0 {
		other.Tokens += rest
	}
	if other.Tokens > 0 {
		kept = append(kept, other)
	}
	return kept
}

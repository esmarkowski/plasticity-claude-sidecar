package attrib

import (
	"encoding/json"
	"testing"
)

// The whole point is that `cd app && bin/rails test` is bin/rails work, not cd
// work. A breakdown that answers "cd" for a third of the session is worse than
// no breakdown, and one that answers "mise" for all of it is no better.
func TestProgramNamesTheRealCommand(t *testing.T) {
	cases := map[string]string{
		"git status":                       "git",
		"gh pr list --limit 5":             "gh",
		"bin/dev":                          "bin/dev",
		"./bin/setup":                      "bin/setup",
		"cd app && bin/rails test":         "bin/rails",
		"cd /tmp; ls -la":                  "ls",
		"git diff | head -20":              "git",
		"RAILS_ENV=test bundle exec rspec": "rspec",
		"sudo systemctl restart nginx":     "systemctl",
		"/opt/homebrew/bin/rg pattern":     "rg",
		"$HOME/.claude/hooks/audit.sh":     "audit.sh",
		"python3 - <<'PY'\nprint(1)\nPY":   "python3",
		"grep \"a|b\" file.txt":            "grep",
		"export FOO=1 && mise run test":    "mise",
		"time go build ./...":              "go",
		"":                                 "",
	}
	for cmd, want := range wrapped {
		cases[cmd] = want
	}
	for cmd, want := range cases {
		if got := program(cmd); got != want {
			t.Errorf("program(%q) = %q, want %q", cmd, got, want)
		}
	}
}

// A wrapper is not the work. This project runs almost everything through
// `mise exec --`, which without unwrapping files three quarters of a session
// under one name and says nothing at all.
var wrapped = map[string]string{
	"mise exec -- bundle exec rspec":              "rspec",
	"mise exec -- ruby -v":                        "ruby",
	"MISE_LOCKED=false mise exec -- git fetch":    "git",
	"mise x -- rubocop -a":                        "rubocop",
	"cd app && mise exec -- bin/rails db:migrate": "bin/rails",
	// mise itself, when it is not wrapping anything.
	"mise run test": "mise",
	"mise install":  "mise",
	// A shell handed a command with -c is running that command.
	"bash -c \"git status\"": "git",
	"bash script.sh":         "bash",
	"npx prettier --write .": "prettier",
	"xargs rm":               "rm",
}

// Only Bash is split. Every other tool name already says what it did, and a Read
// broken down by file would be the rules tab with worse labels.
func TestToolFamilyOnlySplitsBash(t *testing.T) {
	if got := toolFamily("Bash", json.RawMessage(`{"command":"git log"}`)); got != "git" {
		t.Errorf("Bash family = %q, want git", got)
	}
	if got := toolFamily("Read", json.RawMessage(`{"file_path":"/tmp/x"}`)); got != "" {
		t.Errorf("Read got a family: %q", got)
	}
	// Malformed input is a row we cannot label, not a crash.
	if got := toolFamily("Bash", json.RawMessage(`not json`)); got != "" {
		t.Errorf("unparseable input produced %q", got)
	}
}

// A breakdown that does not add up to its parent invites the reader to work out
// what the rest was.
func TestRankChildrenSumsBackToTheParent(t *testing.T) {
	kids := map[string]*Item{
		"git":  {Name: "git", Tokens: 500, Count: 20},
		"gh":   {Name: "gh", Tokens: 300, Count: 10},
		"mise": {Name: "mise", Tokens: 100, Count: 5},
	}
	got := rankChildren(kids, 1000)
	if len(got) != 4 {
		t.Fatalf("got %d rows, want 3 commands plus the unattributed remainder: %+v", len(got), got)
	}
	if got[0].Name != "git" || got[1].Name != "gh" || got[2].Name != "mise" {
		t.Errorf("not ordered largest first: %+v", got)
	}
	last := got[len(got)-1]
	if last.Name != "other" || last.Tokens != 100 {
		t.Errorf("remainder row = %+v, want other with the 100 no child claimed", last)
	}

	sum := 0
	for _, c := range got {
		sum += c.Tokens
	}
	if sum != 1000 {
		t.Errorf("children sum to %d, want the parent's 1000", sum)
	}
}

// The tail is long — a session touches dozens of programs — and the shades that
// tie a row to its segment in the bar run out well before the tail does.
func TestRankChildrenFoldsTheTail(t *testing.T) {
	kids := map[string]*Item{}
	for i, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		kids[name] = &Item{Name: name, Tokens: 100 - i, Count: 1}
	}
	got := rankChildren(kids, 100*9-36)
	if len(got) != maxChildren {
		t.Fatalf("got %d rows, want %d", len(got), maxChildren)
	}
	if got[len(got)-1].Name != "other" {
		t.Errorf("tail not folded: %+v", got)
	}
	// Four folded rows, so the fold has to carry their four uses too.
	if got[len(got)-1].Count != 4 {
		t.Errorf("folded uses = %d, want 4", got[len(got)-1].Count)
	}
}

func TestRankChildrenEmpty(t *testing.T) {
	if got := rankChildren(nil, 500); got != nil {
		t.Errorf("a tool with no sub-kinds got children: %+v", got)
	}
}

// A bucket's items have to be corrected by the same factor the bucket was, or an
// item reports a share of its own category above 100% — which is exactly what
// "Read 721,858 · 164% share" was.
func TestCorrectedScalesItemsWithTheirBucket(t *testing.T) {
	if got := corrected(1000, 0.5); got != 500 {
		t.Errorf("corrected(1000, 0.5) = %d", got)
	}
	// A bucket that was never rescaled has a zero factor, which is not a licence
	// to zero the count.
	if got := corrected(1000, 0); got != 1000 {
		t.Errorf("an unscaled bucket's item came out as %d", got)
	}
	if got := corrected(1000, 1); got != 1000 {
		t.Errorf("a factor of one changed the count to %d", got)
	}
}

// The parts have to move with the whole. Scaling only the parent left the "other"
// row absorbing the difference and reporting commands that never ran.
func TestCorrectedAllKeepsABreakdownConsistent(t *testing.T) {
	kids := map[string]*Item{
		"git": {Name: "git", Tokens: 600, Count: 10},
		"gh":  {Name: "gh", Tokens: 400, Count: 5},
	}
	scaled := correctedAll(kids, 0.5)
	got := rankChildren(scaled, corrected(1000, 0.5))

	sum := 0
	for _, c := range got {
		sum += c.Tokens
	}
	if sum != 500 {
		t.Errorf("the breakdown sums to %d, want the scaled parent's 500: %+v", sum, got)
	}
	for _, c := range got {
		if c.Name == "other" {
			t.Errorf("scaling invented an 'other' row: %+v", got)
		}
	}
	// And the originals are untouched, since the map is the running tally.
	if kids["git"].Tokens != 600 {
		t.Errorf("correctedAll mutated the tally: git is now %d", kids["git"].Tokens)
	}
}

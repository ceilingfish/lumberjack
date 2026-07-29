package covcheck

import (
	"strings"
	"testing"
)

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"main.go", "main.go", true},
		{"main.go", "cmd/main.go", true}, // no "/": basename match, at any depth
		{"MenuBarView.swift", "macos/Sources/LumberjackMenuBar/MenuBarView.swift", true},
		{"internal/database/migrations/embed.go", "internal/database/migrations/embed.go", true},
		{"internal/database/migrations/embed.go", "internal/database/embed.go", false},
		{"**/*.pb.go", "pkg/client/lumberjack/v1/lumberjack.pb.go", true},
		{"**/*.pb.go", "lumberjack.pb.go", true},
		{"**/*.pb.go", "pkg/client/lumberjack/v1/lumberjack_grpc.pb.go", true},
		{"**/*.grpc.swift", "macos/Sources/LumberjackMenuBar/Generated/lumberjack/v1/lumberjack.grpc.swift", true},
		{"**/*.pb.go", "internal/present/color.go", false},
	}
	for _, c := range cases {
		if got := MatchGlob(c.pattern, c.path); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestParseExclusions(t *testing.T) {
	input := `
# a comment
main.go

**/*.pb.go # not a trailing comment, just part of the glob check below
`
	patterns, err := ParseExclusions(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseExclusions: %v", err)
	}
	want := []string{"main.go", "**/*.pb.go # not a trailing comment, just part of the glob check below"}
	if len(patterns) != len(want) {
		t.Fatalf("got %d patterns, want %d: %v", len(patterns), len(want), patterns)
	}
	if patterns[0] != want[0] {
		t.Errorf("patterns[0] = %q, want %q", patterns[0], want[0])
	}
}

func TestParseProfile(t *testing.T) {
	const module = "github.com/example/mod"
	input := `mode: atomic
github.com/example/mod/pkg/a.go:3.2,5.3 2 1
github.com/example/mod/pkg/a.go:7.2,9.3 1 0
github.com/example/mod/main.go:7.13,9.2 1 0
`
	entries, err := ParseProfile(strings.NewReader(input), module)
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	if entries[0].file != "pkg/a.go" || entries[0].stmts != 2 || entries[0].count != 1 {
		t.Errorf("entries[0] = %+v", entries[0])
	}
	if entries[2].file != "main.go" {
		t.Errorf("entries[2].file = %q, want main.go", entries[2].file)
	}
}

func TestParseProfileMalformed(t *testing.T) {
	if _, err := ParseProfile(strings.NewReader("mode: atomic\nnot a valid line\n"), "m"); err == nil {
		t.Fatal("expected an error for a malformed profile line")
	}
}

func TestGate(t *testing.T) {
	const module = "github.com/example/mod"
	profile := `mode: atomic
github.com/example/mod/main.go:7.13,9.2 3 0
github.com/example/mod/good/good.go:1.1,3.2 6 5
github.com/example/mod/bad/bad.go:1.1,3.2 4 1
github.com/example/mod/bad/bad.go:4.1,10.2 16 0
`
	entries, err := ParseProfile(strings.NewReader(profile), module)
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}

	packages := []Package{
		{ImportPath: module, Dir: ".", GoFiles: []string{"main.go"}, TestFileCount: 0},
		{ImportPath: module + "/good", Dir: "good", GoFiles: []string{"good.go"}, TestFileCount: 1},
		{ImportPath: module + "/bad", Dir: "bad", GoFiles: []string{"bad.go"}, TestFileCount: 1},
		{ImportPath: module + "/untested", Dir: "untested", GoFiles: []string{"predicate.go"}, TestFileCount: 0},
	}

	exclusions := []string{"main.go"} // root package's only file is excluded

	results, global := Gate(packages, entries, exclusions)
	ApplyThreshold(results, 80)

	byDir := map[string]Result{}
	for _, r := range results {
		byDir[r.Dir] = r
	}

	if r := byDir["."]; !r.Excluded || !r.Pass {
		t.Errorf("root package: got %+v, want excluded and passing", r)
	}
	if r := byDir["good"]; r.Percent != 100 || !r.Pass {
		t.Errorf("good package: got %+v, want 100%% and passing", r)
	}
	if r := byDir["bad"]; r.Percent != 20 || r.Pass {
		t.Errorf("bad package: got %+v, want 20%% and failing", r)
	}
	if r := byDir["untested"]; !r.NoTests || r.Pass {
		t.Errorf("untested package: got %+v, want NoTests and failing", r)
	}

	// Global total: only good (6/6 covered) and bad (4/20 covered) count;
	// main.go is excluded from both numerator and denominator.
	wantGlobal := 100 * float64(6+4) / float64(6+20)
	if diff := global - wantGlobal; diff > 0.01 || diff < -0.01 {
		t.Errorf("global = %v, want %v", global, wantGlobal)
	}
}

func TestGateNoFailures(t *testing.T) {
	const module = "github.com/example/mod"
	profile := `mode: atomic
github.com/example/mod/good/good.go:1.1,3.2 4 4
`
	entries, _ := ParseProfile(strings.NewReader(profile), module)
	packages := []Package{
		{ImportPath: module + "/good", Dir: "good", GoFiles: []string{"good.go"}, TestFileCount: 1},
	}
	results, _ := Gate(packages, entries, nil)
	ApplyThreshold(results, 80)
	for _, r := range results {
		if !r.Pass {
			t.Errorf("expected all packages to pass, got %+v", r)
		}
	}
}

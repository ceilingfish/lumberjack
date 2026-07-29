// Package covcheck implements the per-package coverage gate used by
// scripts/coverage.sh. It parses a `go test -coverprofile` output file,
// aggregates statement coverage per package, and fails any package (not
// just the global average) that falls below a configurable threshold.
//
// Packages that contain non-excluded Go source but no test files are
// treated as failing at 0%, since Go's coverage profile omits them
// entirely otherwise (see issue #31).
package covcheck

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Package holds the coverage-relevant facts about a Go package, gathered
// independently of the coverage profile (typically via `go list`) so a
// package cannot escape the gate by omission from the profile.
type Package struct {
	// ImportPath is the package's import path, e.g.
	// "github.com/ceilingfish/lumberjack/internal/present".
	ImportPath string
	// Dir is the package's directory relative to the module root, using
	// forward slashes, e.g. "internal/present". The module root package
	// itself is ".".
	Dir string
	// GoFiles lists the package's non-test .go source file names (no
	// directory component).
	GoFiles []string
	// TestFileCount is the number of _test.go files (internal + external
	// test packages) belonging to this package.
	TestFileCount int
}

// Result is one package's outcome after the gate has run.
type Result struct {
	Dir        string
	Percent    float64
	Statements int
	Excluded   bool // every source file in the package is on the exclusion list
	NoTests    bool // has non-excluded source but zero test files
	Pass       bool
}

// profileEntry is one parsed line of a go coverage profile.
type profileEntry struct {
	file  string // relative to module root, forward slashes
	stmts int
	count int
}

var profileLineRE = regexp.MustCompile(`^(.+):(\d+\.\d+),(\d+\.\d+) (\d+) (\d+)$`)

// ParseProfile reads a go coverage profile (as produced by
// `go test -coverprofile`) and returns its entries with file paths made
// relative to modulePath (the Go module's import path, e.g.
// "github.com/ceilingfish/lumberjack").
func ParseProfile(r io.Reader, modulePath string) ([]profileEntry, error) {
	var entries []profileEntry
	scanner := bufio.NewScanner(r)
	prefix := modulePath + "/"
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		m := profileLineRE.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("covcheck: malformed profile line: %q", line)
		}
		stmts, err := strconv.Atoi(m[4])
		if err != nil {
			return nil, fmt.Errorf("covcheck: bad statement count in %q: %w", line, err)
		}
		count, err := strconv.Atoi(m[5])
		if err != nil {
			return nil, fmt.Errorf("covcheck: bad hit count in %q: %w", line, err)
		}
		file := strings.TrimPrefix(m[1], prefix)
		entries = append(entries, profileEntry{file: file, stmts: stmts, count: count})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// MatchGlob reports whether relPath (a slash-separated path relative to the
// repository root) matches pattern. Patterns without a "/" match against the
// file's base name at any depth (e.g. "MenuBarView.swift" or "main.go").
// Patterns with a "/" are matched against the full relative path, segment by
// segment, where a "**" segment matches zero or more path segments (e.g.
// "**/*.pb.go").
func MatchGlob(pattern, relPath string) bool {
	if !strings.Contains(pattern, "/") {
		ok, _ := filepath.Match(pattern, filepath.Base(relPath))
		return ok
	}
	return doubleStarMatch(strings.Split(pattern, "/"), strings.Split(relPath, "/"))
}

func doubleStarMatch(pat, name []string) bool {
	if len(pat) == 0 {
		return len(name) == 0
	}
	if pat[0] == "**" {
		if doubleStarMatch(pat[1:], name) {
			return true
		}
		if len(name) > 0 && doubleStarMatch(pat, name[1:]) {
			return true
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	ok, err := filepath.Match(pat[0], name[0])
	if err != nil || !ok {
		return false
	}
	return doubleStarMatch(pat[1:], name[1:])
}

// ParseExclusions reads an exclusion list file: one glob pattern per line,
// blank lines and "#"-prefixed comments ignored.
func ParseExclusions(r io.Reader) ([]string, error) {
	var patterns []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns, scanner.Err()
}

func excluded(patterns []string, relPath string) bool {
	for _, p := range patterns {
		if MatchGlob(p, relPath) {
			return true
		}
	}
	return false
}

// Gate computes per-package results from a coverage profile, the package
// set (typically from `go list`), and a list of exclusion glob patterns.
// It returns the results (one per non-excluded package) and the global
// total percentage across all non-excluded statements.
func Gate(packages []Package, profile []profileEntry, exclusions []string) (results []Result, globalPercent float64) {
	// Sum statements/coverage per package directory from the profile,
	// skipping excluded files.
	type agg struct {
		stmts, covered int
	}
	byDir := make(map[string]*agg)
	var totalStmts, totalCovered int
	for _, e := range profile {
		if excluded(exclusions, e.file) {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(e.file))
		a := byDir[dir]
		if a == nil {
			a = &agg{}
			byDir[dir] = a
		}
		a.stmts += e.stmts
		totalStmts += e.stmts
		if e.count > 0 {
			a.covered += e.stmts
			totalCovered += e.stmts
		}
	}

	for _, pkg := range packages {
		nonExcluded := 0
		for _, f := range pkg.GoFiles {
			rel := f
			if pkg.Dir != "." {
				rel = pkg.Dir + "/" + f
			}
			if !excluded(exclusions, rel) {
				nonExcluded++
			}
		}
		if nonExcluded == 0 {
			// Nothing coverable in this package once exclusions are
			// applied (e.g. the root package, which is just main.go).
			results = append(results, Result{Dir: pkg.Dir, Excluded: true, Pass: true})
			continue
		}
		if pkg.TestFileCount == 0 {
			// Real code, no test files: reported and gated at 0% rather
			// than being silently absent from the profile. Whether that
			// fails the run is still governed by the threshold, like any
			// other package — this is what lets the gate ratchet from 0
			// upward as zero-test packages gain their first test.
			results = append(results, Result{Dir: pkg.Dir, NoTests: true, Percent: 0})
			continue
		}
		a := byDir[pkg.Dir]
		var pct float64
		var stmts int
		if a != nil && a.stmts > 0 {
			pct = 100 * float64(a.covered) / float64(a.stmts)
			stmts = a.stmts
		}
		results = append(results, Result{Dir: pkg.Dir, Percent: pct, Statements: stmts})
	}

	if totalStmts > 0 {
		globalPercent = 100 * float64(totalCovered) / float64(totalStmts)
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Percent != results[j].Percent {
			return results[i].Percent < results[j].Percent
		}
		return results[i].Dir < results[j].Dir
	})

	return results, globalPercent
}

// ApplyThreshold sets Pass on every gated (non-excluded) result according to
// threshold, leaving already-decided results (excluded, no-tests) alone.
func ApplyThreshold(results []Result, threshold float64) {
	for i := range results {
		if results[i].Excluded || results[i].NoTests {
			continue
		}
		results[i].Pass = results[i].Percent >= threshold
	}
}

// Command covcheck is the per-package coverage gate invoked by
// scripts/coverage.sh. See covcheck.go for the gating logic.
//
// Usage: covcheck <profile> <threshold> <exclusions-file>
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ceilingfish/lumberjack/scripts/covcheck"
)

// listPackage mirrors the subset of `go list -json` output covcheck needs.
type listPackage struct {
	ImportPath   string
	Dir          string
	GoFiles      []string
	TestGoFiles  []string
	XTestGoFiles []string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 3 {
		_, _ = fmt.Fprintln(stderr, "usage: covcheck <profile> <threshold> <exclusions-file>")
		return 2
	}
	profilePath, thresholdArg, exclusionsPath := args[0], args[1], args[2]

	threshold, err := strconv.ParseFloat(thresholdArg, 64)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "covcheck: invalid threshold %q: %v\n", thresholdArg, err)
		return 2
	}

	modulePath, moduleRoot, err := moduleInfo()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "covcheck: %v\n", err)
		return 2
	}

	packages, err := listPackages(moduleRoot)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "covcheck: %v\n", err)
		return 2
	}

	profileFile, err := os.Open(profilePath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "covcheck: %v\n", err)
		return 2
	}
	defer func() { _ = profileFile.Close() }()

	profile, err := covcheck.ParseProfile(profileFile, modulePath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "covcheck: %v\n", err)
		return 2
	}

	exclusionsFile, err := os.Open(exclusionsPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "covcheck: %v\n", err)
		return 2
	}
	defer func() { _ = exclusionsFile.Close() }()

	exclusions, err := covcheck.ParseExclusions(exclusionsFile)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "covcheck: %v\n", err)
		return 2
	}

	results, global := covcheck.Gate(packages, profile, exclusions)
	covcheck.ApplyThreshold(results, threshold)

	return report(stdout, stderr, results, global, threshold)
}

func report(stdout, stderr io.Writer, results []covcheck.Result, global, threshold float64) int {
	var failing []covcheck.Result
	for _, r := range results {
		if r.Excluded {
			continue
		}
		switch {
		case r.NoTests:
			_, _ = fmt.Fprintf(stdout, "  0.0%%  %s  (no test files)\n", r.Dir)
		default:
			_, _ = fmt.Fprintf(stdout, "%5.1f%%  %s\n", r.Percent, r.Dir)
		}
		if !r.Pass {
			failing = append(failing, r)
		}
	}
	_, _ = fmt.Fprintln(stdout, "-------")
	_, _ = fmt.Fprintf(stdout, "%5.1f%%  TOTAL (informational; the gate is per-package)\n", global)

	if len(failing) > 0 {
		_, _ = fmt.Fprintf(stderr, "FAIL: %d package(s) below the %g%% per-package floor:\n", len(failing), threshold)
		for _, r := range failing {
			if r.NoTests {
				_, _ = fmt.Fprintf(stderr, "  %s: no test files\n", r.Dir)
			} else {
				_, _ = fmt.Fprintf(stderr, "  %s: %.1f%%\n", r.Dir, r.Percent)
			}
		}
		return 1
	}
	return 0
}

// moduleInfo returns the current module's import path and its root
// directory on disk.
func moduleInfo() (modulePath, moduleRoot string, err error) {
	path, err := goList("-m")
	if err != nil {
		return "", "", err
	}
	gomod, err := goEnv("GOMOD")
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(path), filepath.Dir(strings.TrimSpace(gomod)), nil
}

func goList(args ...string) (string, error) {
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go list %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

func goEnv(name string) (string, error) {
	cmd := exec.Command("go", "env", name)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go env %s: %w", name, err)
	}
	return string(out), nil
}

// listPackages runs `go list -json ./...` and converts its output into the
// package set covcheck needs, with directories made relative to the module
// root. Using `go list` (rather than the coverage profile) means a package
// with no test files is still discovered and gated.
func listPackages(moduleRoot string) ([]covcheck.Package, error) {
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = moduleRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -json ./...: %w", err)
	}

	var packages []covcheck.Package
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var lp listPackage
		if err := dec.Decode(&lp); err != nil {
			return nil, fmt.Errorf("decoding go list output: %w", err)
		}
		relDir, err := filepath.Rel(moduleRoot, lp.Dir)
		if err != nil {
			return nil, fmt.Errorf("relativizing %q: %w", lp.Dir, err)
		}
		packages = append(packages, covcheck.Package{
			ImportPath:    lp.ImportPath,
			Dir:           filepath.ToSlash(relDir),
			GoFiles:       lp.GoFiles,
			TestFileCount: len(lp.TestGoFiles) + len(lp.XTestGoFiles),
		})
	}
	return packages, nil
}

// Package doctor implements `lumberjack doctor`: it verifies the host
// prerequisites Lumberjack shells out to (git and the gh CLI) and reports
// their location and version. It is deliberately CLI-local and daemon-free so
// it works even when no daemon is running (see docs/prd.md and the proto
// header comment).
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/ceilingfish/lumberjack/internal/github"
	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/ceilingfish/lumberjack/internal/worktree"
)

// Check is the result of one prerequisite probe.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"` // "<path> (<version>)" on success, or the failure reason
}

// Run gathers every prerequisite check, writes a report to w in the given
// format, and returns ok=false if any check failed so the caller can exit
// non-zero.
func Run(ctx context.Context, w io.Writer, format present.Format) (bool, error) {
	checks := []Check{checkGit(ctx), checkGH(ctx), checkGHAuth(ctx)}
	return report(w, checks, format)
}

// checkGit verifies git is resolvable and reports its path and version.
func checkGit(ctx context.Context) Check {
	g, err := worktree.NewGit()
	if err != nil {
		return Check{Name: "git", Detail: err.Error()}
	}
	ver, err := g.Version(ctx)
	if err != nil {
		return Check{Name: "git", Detail: err.Error()}
	}
	return Check{Name: "git", OK: true, Detail: fmt.Sprintf("%s (%s)", g.Path(), ver)}
}

// checkGH verifies the gh CLI is resolvable and reports its path and version.
func checkGH(ctx context.Context) Check {
	c, err := github.NewClient()
	if err != nil {
		return Check{Name: "gh", Detail: err.Error()}
	}
	ver, err := c.Version(ctx)
	if err != nil {
		return Check{Name: "gh", Detail: err.Error()}
	}
	return Check{Name: "gh", OK: true, Detail: fmt.Sprintf("%s (%s)", c.Path(), ver)}
}

// checkGHAuth verifies gh is authenticated. It is separate from checkGH so the
// report distinguishes "gh missing" from "gh present but not logged in".
func checkGHAuth(ctx context.Context) Check {
	c, err := github.NewClient()
	if err != nil {
		return Check{Name: "gh auth", Detail: "gh not available"}
	}
	if err := c.AuthStatus(ctx); err != nil {
		return Check{Name: "gh auth", Detail: "not authenticated — run `gh auth login`"}
	}
	return Check{Name: "gh auth", OK: true, Detail: "authenticated"}
}

// report renders checks per format and returns whether all passed.
func report(w io.Writer, checks []Check, format present.Format) (bool, error) {
	allOK := true
	for _, c := range checks {
		if !c.OK {
			allOK = false
		}
	}
	if format == present.JSON {
		return allOK, present.WriteJSONArray(w, checks)
	}

	color := format == present.Color
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range checks {
		mark := present.StatusOK("ok", color)
		if !c.OK {
			mark = present.StatusErr("FAIL", color)
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", mark, c.Name, c.Detail); err != nil {
			return false, err
		}
	}
	if err := tw.Flush(); err != nil {
		return false, err
	}
	return allOK, nil
}

// ErrChecksFailed is returned by the command layer when any check fails, so
// Cobra exits non-zero (the report itself is already on stdout).
var ErrChecksFailed = errors.New("one or more prerequisite checks failed")

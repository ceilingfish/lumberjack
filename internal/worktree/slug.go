// Package worktree resolves branch names to worktree directories and performs
// the git worktree operations the daemon owns (create, remove, list) plus the
// live reconciliation checks (dirty tree, local-only commits) that decide
// whether a worktree can be safely removed.
//
// Only the daemon imports this package: it is the sole owner of the working
// trees (see AGENTS.md, "daemon and client").
package worktree

import (
	"path/filepath"
	"regexp"
	"strings"
)

// issuePrefix matches a leading GitHub issue reference such as "#12345-" so
// that a branch like "fix/#12345-bugfix-id" slugs to "bugfix-id" rather than
// carrying the issue number into the directory name (see docs/prd.md).
var issuePrefix = regexp.MustCompile(`^#\d+[-_/]?`)

// unsafeChars matches any run of characters that cannot appear in a portable
// directory name; each run collapses to a single hyphen.
var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Slug reduces a branch name to the trailing component used when building a
// worktree directory name. It takes the segment after the final "/", strips a
// leading issue reference, and replaces filesystem-unsafe characters:
//
//	feature/my-feature   -> my-feature
//	my-feature           -> my-feature
//	fix/#12345-bugfix-id -> bugfix-id
//
// If stripping leaves nothing usable, it falls back to the sanitized full
// branch name so a directory can always be derived.
func Slug(branch string) string {
	base := branch
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = issuePrefix.ReplaceAllString(base, "")

	slug := sanitize(base)
	if slug == "" {
		// Nothing survived (e.g. the branch was entirely an issue ref); fall
		// back to the whole branch so we never produce an empty directory.
		slug = sanitize(strings.ReplaceAll(branch, "/", "-"))
	}
	return slug
}

// sanitize collapses unsafe character runs to hyphens and trims stray hyphens.
func sanitize(s string) string {
	s = unsafeChars.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// DirectoryName builds the worktree directory name from the repo's stored
// dir_prefix and a branch: "<prefix>-<slug(branch)>". Decoupling the prefix
// from the on-disk folder name means renaming the main checkout never breaks
// worktree naming (see docs/schema.md, dir_prefix).
func DirectoryName(dirPrefix, branch string) string {
	return dirPrefix + "-" + Slug(branch)
}

// Path is the absolute worktree path: parentDir/<prefix>-<slug(branch)>.
func Path(parentDir, dirPrefix, branch string) string {
	return filepath.Join(parentDir, DirectoryName(dirPrefix, branch))
}

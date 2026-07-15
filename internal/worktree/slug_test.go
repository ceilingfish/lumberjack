package worktree

import (
	"path/filepath"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := []struct {
		branch string
		want   string
	}{
		// Examples from docs/prd.md.
		{"feature/my-feature", "my-feature"},
		{"my-feature", "my-feature"},
		{"fix/#12345-bugfix-id", "bugfix-id"},
		// Nested prefixes: only the final segment survives.
		{"user/feature/foo", "foo"},
		// Unsafe characters collapse to hyphens and trim.
		{"feature/weird name!!", "weird-name"},
		{"a//b", "b"},
		// A branch that is only an issue ref falls back to the sanitized whole.
		{"#999", "999"},
		{"", ""},
	}
	for _, c := range cases {
		if got := Slug(c.branch); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.branch, got, c.want)
		}
	}
}

func TestDirectoryName(t *testing.T) {
	if got := DirectoryName("my_repo", "feature/my-feature"); got != "my_repo-my-feature" {
		t.Errorf("DirectoryName = %q, want my_repo-my-feature", got)
	}
	if got := DirectoryName("my_repo", "fix/#12345-bugfix-id"); got != "my_repo-bugfix-id" {
		t.Errorf("DirectoryName = %q, want my_repo-bugfix-id", got)
	}
}

func TestPath(t *testing.T) {
	got := Path("/path/to", "my_repo", "feature/my-feature")
	if want := filepath.Join("/path/to", "my_repo-my-feature"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

package schema

import "testing"

// Matches resolves a user-supplied reference to a worktree by any of three
// alternatives, so each is exercised on its own — and, just as importantly, a
// reference that looks close but matches none of them must not resolve.
func TestWorktreeMatches(t *testing.T) {
	w := &Worktree{
		BranchName:    "feature/login",
		DirectoryPath: "/home/dev/Code/app-feature-login",
	}

	tests := []struct {
		name string
		ref  string
		want bool
	}{
		{"branch name", "feature/login", true},
		{"full directory path", "/home/dev/Code/app-feature-login", true},
		{"base name of the directory path", "app-feature-login", true},
		{"unrelated reference", "main", false},
		{"empty reference", "", false},
		{"parent of the directory path", "/home/dev/Code", false},
		{"base name of the branch, not the directory", "login", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := w.Matches(tt.ref); got != tt.want {
				t.Errorf("Matches(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

func TestWorktreeMatchesAmbiguousReference(t *testing.T) {
	tests := []struct {
		name     string
		worktree Worktree
		ref      string
	}{
		{
			name: "branch name is also the directory base name",
			worktree: Worktree{
				BranchName:    "app-feature-login",
				DirectoryPath: "/home/dev/Code/app-feature-login",
			},
			ref: "app-feature-login",
		},
		{
			name: "branch name is also the full directory path",
			worktree: Worktree{
				BranchName:    "/home/dev/Code/app",
				DirectoryPath: "/home/dev/Code/app",
			},
			ref: "/home/dev/Code/app",
		},
		{
			name: "directory is at the filesystem root, so path and base name coincide",
			worktree: Worktree{
				BranchName:    "feature/login",
				DirectoryPath: "/app",
			},
			ref: "/app",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.worktree.Matches(tt.ref) {
				t.Errorf("Matches(%q) = false, want true", tt.ref)
			}
		})
	}
}

func TestWorktreeMatchesZeroValue(t *testing.T) {
	var w Worktree

	tests := []struct {
		name string
		ref  string
		want bool
	}{
		{"empty reference matches an unpopulated worktree", "", true},
		{"filepath.Base of an empty path leaks a dot reference", ".", true},
		{"any real reference still does not match", "main", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := w.Matches(tt.ref); got != tt.want {
				t.Errorf("Matches(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

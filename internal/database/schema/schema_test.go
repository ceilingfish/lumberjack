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

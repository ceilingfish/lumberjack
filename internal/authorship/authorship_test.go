package authorship

import (
	"testing"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

func ptr(a schema.Authorship) *schema.Authorship { return &a }

func TestIdentitiesContains(t *testing.T) {
	ids := NewIdentities("Ceilingfish", "", "ceilingfish-parallax")
	cases := map[string]bool{
		"ceilingfish":          true,
		"CEILINGFISH":          true,
		"ceilingfish-parallax": true,
		"dmitri":               false,
		"":                     false,
	}
	for login, want := range cases {
		if got := ids.Contains(login); got != want {
			t.Errorf("Contains(%q) = %v, want %v", login, got, want)
		}
	}
}

func TestResolve(t *testing.T) {
	ids := NewIdentities("me")
	cases := []struct {
		name string
		in   Inputs
		want schema.Authorship
	}{
		{"override mine beats a PR by someone else", Inputs{Override: ptr(schema.AuthorshipMine), HasPR: true, PRAuthor: "dmitri"}, schema.AuthorshipMine},
		{"override theirs beats local origin", Inputs{Override: ptr(schema.AuthorshipTheirs), Origin: schema.OriginLocal}, schema.AuthorshipTheirs},
		{"PR by an identity is mine", Inputs{HasPR: true, PRAuthor: "ME"}, schema.AuthorshipMine},
		{"PR by someone else is theirs even when created locally", Inputs{HasPR: true, PRAuthor: "dmitri", Origin: schema.OriginLocal}, schema.AuthorshipTheirs},
		{"PR with no recorded author is theirs", Inputs{HasPR: true, Origin: schema.OriginLocal}, schema.AuthorshipTheirs},
		{"no PR and created locally is mine", Inputs{Origin: schema.OriginLocal}, schema.AuthorshipMine},
		{"no PR, adopted, commits by me", Inputs{Origin: schema.OriginAdopted, Commits: ptr(schema.AuthorshipMine)}, schema.AuthorshipMine},
		{"no PR, adopted, commits by others", Inputs{Origin: schema.OriginAdopted, Commits: ptr(schema.AuthorshipTheirs)}, schema.AuthorshipTheirs},
		{"no evidence at all is theirs", Inputs{Origin: schema.OriginAdopted}, schema.AuthorshipTheirs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Resolve(tc.in, ids); got != tc.want {
				t.Errorf("Resolve = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCommitsVerdict(t *testing.T) {
	ids := NewIdentities("me", "me-work")
	cases := []struct {
		name      string
		logins    []string
		truncated bool
		want      schema.Authorship
	}{
		{"every commit by an identity", []string{"me", "ME-WORK"}, false, schema.AuthorshipMine},
		{"a commit by someone else", []string{"me", "dmitri"}, false, schema.AuthorshipTheirs},
		{"a commit with no resolved author", []string{"me", ""}, false, schema.AuthorshipTheirs},
		{"no commits of its own", nil, false, schema.AuthorshipTheirs},
		{"too many commits to see them all", []string{"me"}, true, schema.AuthorshipTheirs},
	}
	for _, tc := range cases {
		if got := CommitsVerdict(tc.logins, tc.truncated, ids); got != tc.want {
			t.Errorf("%s: CommitsVerdict = %q, want %q", tc.name, got, tc.want)
		}
	}
}

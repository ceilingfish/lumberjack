package authorship

import (
	"strings"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

type Identities struct {
	logins map[string]struct{}
}

func NewIdentities(logins ...string) Identities {
	set := make(map[string]struct{}, len(logins))
	for _, l := range logins {
		if l != "" {
			set[strings.ToLower(l)] = struct{}{}
		}
	}
	return Identities{logins: set}
}

func (i Identities) Contains(login string) bool {
	_, ok := i.logins[strings.ToLower(login)]
	return login != "" && ok
}

type Inputs struct {
	Override *schema.Authorship
	HasPR    bool
	PRAuthor string
	Origin   schema.Origin
	Commits  *schema.Authorship
}

func Resolve(in Inputs, ids Identities) schema.Authorship {
	switch {
	case in.Override != nil:
		return *in.Override
	case in.HasPR:
		if ids.Contains(in.PRAuthor) {
			return schema.AuthorshipMine
		}
		return schema.AuthorshipTheirs
	case in.Origin == schema.OriginLocal:
		return schema.AuthorshipMine
	case in.Commits != nil:
		return *in.Commits
	}
	return schema.AuthorshipTheirs
}

func CommitsVerdict(logins []string, truncated bool, ids Identities) schema.Authorship {
	if truncated || len(logins) == 0 {
		return schema.AuthorshipTheirs
	}
	for _, l := range logins {
		if !ids.Contains(l) {
			return schema.AuthorshipTheirs
		}
	}
	return schema.AuthorshipMine
}

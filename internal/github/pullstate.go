package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const pullStateBatch = 25

type Review struct {
	Login string
	State string
}

type Check struct {
	Required bool
	Status   string
	Outcome  string
}

type Event struct {
	Login string
	At    time.Time
}

type PullState struct {
	Number            int64
	URL               string
	IsDraft           bool
	UpdatedAt         time.Time
	MergeStateStatus  string
	ReviewDecision    string
	BaseRefName       string
	Additions         int
	Deletions         int
	LatestReviews     []Review
	RequestedLogins   []string
	UnresolvedThreads int
	Checks            []Check
	Activity          []Event
}

const pullStateFields = `
number url isDraft updatedAt mergeStateStatus reviewDecision baseRefName additions deletions
latestReviews(first: 50) { nodes { state author { login } } }
reviewRequests(first: 50) { nodes { requestedReviewer { ... on User { login } ... on Bot { login } ... on Mannequin { login } } } }
comments(last: 20) { nodes { createdAt author { login } } }
reviews(last: 20) { nodes { submittedAt author { login } } }
reviewThreads(first: 100) { nodes { isResolved comments(last: 10) { nodes { createdAt author { login } } } } }
commits(last: 1) { nodes { commit { statusCheckRollup { contexts(first: 100) { nodes {
  __typename
  ... on CheckRun { status conclusion isRequired(pullRequestNumber: %[1]d) }
  ... on StatusContext { state isRequired(pullRequestNumber: %[1]d) }
} } } } } }`

func pullStateQuery(repo RepoInfo, numbers []int64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "query { repository(owner: %s, name: %s) {", strconv.Quote(repo.Owner), strconv.Quote(repo.Name))
	for _, n := range numbers {
		fmt.Fprintf(&b, "\npr%d: pullRequest(number: %d) {", n, n)
		fmt.Fprintf(&b, pullStateFields, n)
		b.WriteString("\n}")
	}
	b.WriteString("\n} }")
	return b.String()
}

type gqlLogin struct {
	Login string `json:"login"`
}

type gqlEvent struct {
	CreatedAt   time.Time `json:"createdAt"`
	SubmittedAt time.Time `json:"submittedAt"`
	Author      *gqlLogin `json:"author"`
}

func (e gqlEvent) event() Event {
	ev := Event{At: e.CreatedAt}
	if ev.At.IsZero() {
		ev.At = e.SubmittedAt
	}
	if e.Author != nil {
		ev.Login = e.Author.Login
	}
	return ev
}

type gqlPull struct {
	Number           int64     `json:"number"`
	URL              string    `json:"url"`
	IsDraft          bool      `json:"isDraft"`
	UpdatedAt        time.Time `json:"updatedAt"`
	MergeStateStatus string    `json:"mergeStateStatus"`
	ReviewDecision   string    `json:"reviewDecision"`
	BaseRefName      string    `json:"baseRefName"`
	Additions        int       `json:"additions"`
	Deletions        int       `json:"deletions"`
	LatestReviews    struct {
		Nodes []struct {
			State  string    `json:"state"`
			Author *gqlLogin `json:"author"`
		} `json:"nodes"`
	} `json:"latestReviews"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer *gqlLogin `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	Comments struct {
		Nodes []gqlEvent `json:"nodes"`
	} `json:"comments"`
	Reviews struct {
		Nodes []gqlEvent `json:"nodes"`
	} `json:"reviews"`
	ReviewThreads struct {
		Nodes []struct {
			IsResolved bool `json:"isResolved"`
			Comments   struct {
				Nodes []gqlEvent `json:"nodes"`
			} `json:"comments"`
		} `json:"nodes"`
	} `json:"reviewThreads"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					Contexts struct {
						Nodes []struct {
							Typename   string `json:"__typename"`
							Status     string `json:"status"`
							Conclusion string `json:"conclusion"`
							State      string `json:"state"`
							IsRequired bool   `json:"isRequired"`
						} `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

func (p gqlPull) state() PullState {
	ps := PullState{
		Number: p.Number, URL: p.URL, IsDraft: p.IsDraft, UpdatedAt: p.UpdatedAt,
		MergeStateStatus: p.MergeStateStatus, ReviewDecision: p.ReviewDecision,
		BaseRefName: p.BaseRefName, Additions: p.Additions, Deletions: p.Deletions,
	}
	for _, r := range p.LatestReviews.Nodes {
		rv := Review{State: r.State}
		if r.Author != nil {
			rv.Login = r.Author.Login
		}
		ps.LatestReviews = append(ps.LatestReviews, rv)
	}
	for _, r := range p.ReviewRequests.Nodes {
		if r.RequestedReviewer != nil && r.RequestedReviewer.Login != "" {
			ps.RequestedLogins = append(ps.RequestedLogins, r.RequestedReviewer.Login)
		}
	}
	for _, e := range p.Comments.Nodes {
		ps.Activity = append(ps.Activity, e.event())
	}
	for _, e := range p.Reviews.Nodes {
		ps.Activity = append(ps.Activity, e.event())
	}
	for _, t := range p.ReviewThreads.Nodes {
		if !t.IsResolved {
			ps.UnresolvedThreads++
		}
		for _, e := range t.Comments.Nodes {
			ps.Activity = append(ps.Activity, e.event())
		}
	}
	for _, c := range p.Commits.Nodes {
		if c.Commit.StatusCheckRollup == nil {
			continue
		}
		for _, ctx := range c.Commit.StatusCheckRollup.Contexts.Nodes {
			check := Check{Required: ctx.IsRequired, Status: ctx.Status, Outcome: ctx.Conclusion}
			if ctx.Typename == "StatusContext" {
				check.Status, check.Outcome = "", ctx.State
			}
			ps.Checks = append(ps.Checks, check)
		}
	}
	return ps
}

func (c *Client) PullStates(ctx context.Context, repo RepoInfo, numbers []int64) (map[int64]PullState, error) {
	out := make(map[int64]PullState, len(numbers))
	for start := 0; start < len(numbers); start += pullStateBatch {
		batch := numbers[start:min(start+pullStateBatch, len(numbers))]
		if err := c.pullStateBatch(ctx, repo, batch, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c *Client) pullStateBatch(ctx context.Context, repo RepoInfo, numbers []int64, into map[int64]PullState) error {
	raw, err := c.run(ctx, "", "api", "graphql", "--hostname", repo.Host, "-f", "query="+pullStateQuery(repo, numbers))
	if err != nil {
		return err
	}
	var resp struct {
		Data struct {
			Repository map[string]*gqlPull `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return fmt.Errorf("parsing pull request states: %w", err)
	}
	if len(resp.Errors) > 0 {
		msgs := make([]error, len(resp.Errors))
		for i, e := range resp.Errors {
			msgs[i] = errors.New(e.Message)
		}
		return fmt.Errorf("fetching pull request states: %w", errors.Join(msgs...))
	}
	for _, p := range resp.Data.Repository {
		if p != nil {
			into[p.Number] = p.state()
		}
	}
	return nil
}

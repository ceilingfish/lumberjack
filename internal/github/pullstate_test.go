package github

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

const pullStateJSON = `{"data":{"repository":{
"pr7":{"number":7,"url":"https://github.com/o/n/pull/7","isDraft":true,"updatedAt":"2026-11-01T10:00:00Z",
 "mergeStateStatus":"DIRTY","reviewDecision":"CHANGES_REQUESTED","baseRefName":"main","additions":10,"deletions":4,
 "latestReviews":{"nodes":[{"state":"APPROVED","author":{"login":"dmitri"}},{"state":"COMMENTED","author":null}]},
 "reviewRequests":{"nodes":[{"requestedReviewer":{"login":"me"}},{"requestedReviewer":{}},{"requestedReviewer":null}]},
 "comments":{"nodes":[{"createdAt":"2026-11-02T10:00:00Z","author":{"login":"coderabbitai"}}]},
 "reviews":{"nodes":[{"submittedAt":"2026-11-03T10:00:00Z","author":{"login":"dmitri"}}]},
 "reviewThreads":{"nodes":[
   {"isResolved":false,"comments":{"nodes":[{"createdAt":"2026-11-04T10:00:00Z","author":null}]}},
   {"isResolved":true,"comments":{"nodes":[]}}]},
 "commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"nodes":[
   {"__typename":"CheckRun","status":"COMPLETED","conclusion":"FAILURE","isRequired":true},
   {"__typename":"StatusContext","state":"PENDING","isRequired":false}]}}}}]}},
"pr8":{"number":8,"commits":{"nodes":[{"commit":{"statusCheckRollup":null}}]}},
"pr9":null
}}}`

func TestPullStates(t *testing.T) {
	var gotArgs []string
	c := fakeClient(func(args ...string) (string, error) {
		gotArgs = args
		return pullStateJSON, nil
	})
	states, err := c.PullStates(context.Background(), RepoInfo{Owner: "o", Name: "n", Host: "github.com"}, []int64{7, 8, 9})
	if err != nil {
		t.Fatalf("PullStates: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("states = %+v, want PRs 7 and 8", states)
	}
	pr := states[7]
	if pr.URL != "https://github.com/o/n/pull/7" || !pr.IsDraft || pr.MergeStateStatus != "DIRTY" ||
		pr.ReviewDecision != "CHANGES_REQUESTED" || pr.BaseRefName != "main" || pr.Additions != 10 || pr.Deletions != 4 {
		t.Errorf("pr = %+v", pr)
	}
	if !pr.UpdatedAt.Equal(time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("updatedAt = %v", pr.UpdatedAt)
	}
	if !slices.Equal(pr.LatestReviews, []Review{{Login: "dmitri", State: "APPROVED"}, {State: "COMMENTED"}}) {
		t.Errorf("reviews = %+v", pr.LatestReviews)
	}
	if !slices.Equal(pr.RequestedLogins, []string{"me"}) {
		t.Errorf("requested = %v", pr.RequestedLogins)
	}
	if pr.UnresolvedThreads != 1 {
		t.Errorf("unresolved = %d", pr.UnresolvedThreads)
	}
	wantActivity := []Event{
		{Login: "coderabbitai", At: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)},
		{Login: "dmitri", At: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)},
		{At: time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)},
	}
	if len(pr.Activity) != len(wantActivity) {
		t.Fatalf("activity = %+v", pr.Activity)
	}
	for i, e := range wantActivity {
		if pr.Activity[i].Login != e.Login || !pr.Activity[i].At.Equal(e.At) {
			t.Errorf("activity[%d] = %+v, want %+v", i, pr.Activity[i], e)
		}
	}
	wantChecks := []Check{
		{Required: true, Status: "COMPLETED", Outcome: "FAILURE"},
		{Required: false, Outcome: "PENDING"},
	}
	if !slices.Equal(pr.Checks, wantChecks) {
		t.Errorf("checks = %+v", pr.Checks)
	}
	if len(states[8].Checks) != 0 {
		t.Errorf("PR without a rollup has checks %+v", states[8].Checks)
	}

	query := gotArgs[len(gotArgs)-1]
	for _, want := range []string{`repository(owner: "o", name: "n")`, "pr7: pullRequest(number: 7)", "isRequired(pullRequestNumber: 9)"} {
		if !strings.Contains(query, want) {
			t.Errorf("query lacks %q:\n%s", want, query)
		}
	}
	if !slices.Contains(gotArgs, "graphql") || !slices.Contains(gotArgs, "github.com") {
		t.Errorf("args = %v", gotArgs[:len(gotArgs)-1])
	}
}

func TestPullStatesBatches(t *testing.T) {
	var batches [][]string
	c := fakeClient(func(args ...string) (string, error) {
		batches = append(batches, args)
		return `{"data":{"repository":{}}}`, nil
	})
	numbers := make([]int64, pullStateBatch+1)
	for i := range numbers {
		numbers[i] = int64(i + 1)
	}
	if _, err := c.PullStates(context.Background(), RepoInfo{}, numbers); err != nil {
		t.Fatalf("PullStates: %v", err)
	}
	if len(batches) != 2 {
		t.Errorf("batches = %d, want 2", len(batches))
	}
	if states, err := c.PullStates(context.Background(), RepoInfo{}, nil); err != nil || len(states) != 0 || len(batches) != 2 {
		t.Errorf("no PRs: states = %v, err = %v, batches = %d; want nothing asked", states, err, len(batches))
	}
}

func TestPullStatesKeepsPartialData(t *testing.T) {
	c := fakeClient(func(...string) (string, error) {
		return `{"data":{"repository":{"pr1":{"number":1},"pr2":null}},"errors":[{"message":"gone"}]}`, errors.New("exit status 1")
	})
	states, err := c.PullStates(context.Background(), RepoInfo{}, []int64{1, 2})
	if err != nil {
		t.Fatalf("PullStates: %v", err)
	}
	if _, ok := states[1]; !ok || len(states) != 1 {
		t.Errorf("states = %v, want only PR 1", states)
	}
}

func TestPullStatesFailures(t *testing.T) {
	cases := map[string]func(...string) (string, error){
		"gh":   func(...string) (string, error) { return "", errors.New("HTTP 502") },
		"json": func(...string) (string, error) { return "{", nil },
		"graphql": func(...string) (string, error) {
			return `{"errors":[{"message":"rate limited"},{"message":"bad"}]}`, nil
		},
	}
	for name, run := range cases {
		c := fakeClient(run)
		if _, err := c.PullStates(context.Background(), RepoInfo{}, []int64{1}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

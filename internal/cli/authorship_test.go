package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/present"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
)

func TestParseAuthorship(t *testing.T) {
	mine, err := ParseAuthorship("mine")
	if err != nil || mine == nil || *mine != lumberjackv1.Authorship_AUTHORSHIP_MINE {
		t.Errorf("mine = %v, %v", mine, err)
	}
	theirs, err := ParseAuthorship("theirs")
	if err != nil || theirs == nil || *theirs != lumberjackv1.Authorship_AUTHORSHIP_THEIRS {
		t.Errorf("theirs = %v, %v", theirs, err)
	}
	if auto, err := ParseAuthorship("auto"); err != nil || auto != nil {
		t.Errorf("auto = %v, %v; want nil, which clears the override", auto, err)
	}
	if _, err := ParseAuthorship("ours"); err == nil || !strings.Contains(err.Error(), "ours") {
		t.Errorf("err = %v, want a rejection naming the value", err)
	}
}

func TestEmitAuthorship(t *testing.T) {
	cases := []struct {
		wt   *lumberjackv1.Worktree
		want string
	}{
		{&lumberjackv1.Worktree{BranchName: "a", Authorship: lumberjackv1.Authorship_AUTHORSHIP_MINE, AuthorshipOverridden: true}, "a is now mine (set by hand)"},
		{&lumberjackv1.Worktree{BranchName: "b", Authorship: lumberjackv1.Authorship_AUTHORSHIP_THEIRS}, "b is now theirs (derived)"},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		if err := EmitAuthorship(&out, present.Structured, tc.wt); err != nil {
			t.Fatalf("EmitAuthorship: %v", err)
		}
		if strings.TrimSpace(out.String()) != tc.want {
			t.Errorf("out = %q, want %q", out.String(), tc.want)
		}
	}

	var raw bytes.Buffer
	if err := EmitAuthorship(&raw, present.JSON, cases[0].wt); err != nil {
		t.Fatalf("EmitAuthorship json: %v", err)
	}
	if !strings.Contains(raw.String(), `"authorship"`) {
		t.Errorf("json = %q, want the worktree object", raw.String())
	}
}

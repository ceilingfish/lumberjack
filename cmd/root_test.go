package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCmdRejectsBadFormatBeforeActing(t *testing.T) {
	noDaemon(t)
	setupRepo(t)

	commands := [][]string{
		{"delete", "n"},
		{"init", "."},
		{"list"},
		{"set-login", "work"},
		{"status"},
		{"sync"},
		{"sync-all"},
		{"tidy"},
		{"worktrees"},
		{"worktree", "add", "feature/x"},
		{"worktree", "delete", "feature/x"},
		{"doctor"},
		{"daemon", "start"},
		{"daemon", "stop"},
		{"daemon", "status"},
		{"setup-steps", "add", "make build"},
		{"setup-steps", "list"},
		{"setup-steps", "remove", "make build"},
		{"setup-steps", "run"},
	}
	for _, args := range commands {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, err := run(t, "", append([]string{"--format", "yaml"}, args...)...)
			if err == nil {
				t.Fatalf("%v accepted --format yaml (%s)", args, out)
			}
			if !strings.Contains(err.Error(), "yaml") {
				t.Errorf("%v: err = %v, want it to name the bad format", args, err)
			}
		})
	}
}

func TestCmdWithoutADaemonSocketReportsTheDialFailure(t *testing.T) {
	noDaemon(t)

	_, err := run(t, "", "list")
	if err == nil {
		t.Fatal("expected a dial failure with no resolvable socket path")
	}
	if !strings.Contains(err.Error(), "socket") {
		t.Errorf("err = %v, want it to name the unresolvable socket", err)
	}
}

func TestCmdDoctorFailsWhenPrerequisitesAreMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	out, err := run(t, "", "doctor")
	if err == nil {
		t.Fatalf("doctor passed with an empty PATH (%s)", out)
	}
	if !strings.Contains(err.Error(), "prerequisite") {
		t.Errorf("err = %v, want the failed-checks error", err)
	}
}

func TestCmdSetupStepsOutsideAWorktree(t *testing.T) {
	t.Chdir(t.TempDir())

	for _, args := range [][]string{
		{"setup-steps", "add", "make build"},
		{"setup-steps", "list"},
		{"setup-steps", "remove", "make build"},
		{"setup-steps", "run"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := run(t, "", args...); err == nil {
				t.Errorf("%v succeeded outside a git worktree", args)
			}
		})
	}
}

func TestExecuteRunsTheRootCommand(t *testing.T) {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = devnull.Close() })

	prevArgs, prevOut := os.Args, os.Stdout
	os.Args, os.Stdout = []string{"lumberjack", "--help"}, devnull
	t.Cleanup(func() { os.Args, os.Stdout = prevArgs, prevOut })

	Execute()
}

func TestExecuteReportsSuccessAndFailure(t *testing.T) {
	if args := os.Getenv("LUMBERJACK_EXECUTE_ARGS"); args != "" {
		os.Args = append([]string{"lumberjack"}, strings.Fields(args)...)
		Execute()
		return
	}

	cases := []struct {
		name    string
		args    string
		wantErr bool
	}{
		{name: "help succeeds", args: "--help"},
		{name: "unknown command exits non-zero", args: "no-such-command", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestExecuteReportsSuccessAndFailure")
			cmd.Env = append(os.Environ(), "LUMBERJACK_EXECUTE_ARGS="+c.args)
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			err := cmd.Run()
			if (err != nil) != c.wantErr {
				t.Fatalf("Execute %q: err = %v, wantErr = %v (%s)", c.args, err, c.wantErr, out.String())
			}
			if c.wantErr && !strings.Contains(out.String(), "lumberjack:") {
				t.Errorf("expected the error to be reported with the lumberjack prefix, got %q", out.String())
			}
		})
	}
}

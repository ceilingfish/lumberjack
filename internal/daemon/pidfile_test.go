package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestPIDFilePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := PIDFilePath()
	if err != nil {
		t.Fatalf("PIDFilePath: %v", err)
	}
	want := filepath.Join(home, ".lumberjack", "daemon.pid")
	if got != want {
		t.Errorf("PIDFilePath = %q, want %q", got, want)
	}
}

func TestReadPID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// No pid file yet: not running, no error.
	if pid, running, err := ReadPID(); err != nil || running || pid != 0 {
		t.Fatalf("no file: got (%d, %v, %v), want (0, false, nil)", pid, running, err)
	}

	path, err := PIDFilePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	// Our own pid is alive.
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pid, running, err := ReadPID()
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if pid != os.Getpid() || !running {
		t.Errorf("own pid: got (%d, %v), want (%d, true)", pid, running, os.Getpid())
	}

	// A pid that (almost certainly) isn't running reports not-alive.
	if err := os.WriteFile(path, []byte("2147483646\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, running, err := ReadPID(); err != nil || running {
		t.Errorf("dead pid: got running=%v err=%v, want false/nil", running, err)
	}

	// A malformed pid file surfaces an error.
	if err := os.WriteFile(path, []byte("not-a-pid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadPID(); err == nil {
		t.Error("malformed pid file: expected error, got nil")
	}
}

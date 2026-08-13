package daemon

import (
	"context"
	"os"
	"os/exec"
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

func TestPIDFilePathFailsWithoutAHomeDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	if _, err := PIDFilePath(); err == nil {
		t.Error("expected an error with no home directory, got nil")
	}
	if _, _, err := ReadPID(); err == nil {
		t.Error("ReadPID: expected an error with no home directory, got nil")
	}
	lc := &recordingLifecycle{}
	if err := writePIDFile(lc); err == nil {
		t.Error("writePIDFile: expected an error with no home directory, got nil")
	}
}

func TestWritePIDFileRecordsThenRemovesThePID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lc := &recordingLifecycle{}
	if err := writePIDFile(lc); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}
	if len(lc.hooks) != 1 {
		t.Fatalf("hooks appended = %d, want 1", len(lc.hooks))
	}
	hook := lc.hooks[0]

	if err := hook.OnStart(context.Background()); err != nil {
		t.Fatalf("OnStart: %v", err)
	}
	if pid, running, err := ReadPID(); err != nil || !running || pid != os.Getpid() {
		t.Errorf("after OnStart: (%d, %v, %v), want (%d, true, nil)", pid, running, err, os.Getpid())
	}

	if err := hook.OnStop(context.Background()); err != nil {
		t.Fatalf("OnStop: %v", err)
	}
	path, err := PIDFilePath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("pid file still present after OnStop: %v", err)
	}
	if err := hook.OnStop(context.Background()); err != nil {
		t.Errorf("second OnStop: %v", err)
	}
}

func TestWritePIDFileSurfacesFilesystemFailures(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "home")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", blocked)
	lc := &recordingLifecycle{}
	if err := writePIDFile(lc); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}
	if err := lc.hooks[0].OnStart(context.Background()); err == nil {
		t.Error("expected OnStart to fail when the pid dir cannot be created")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := PIDFilePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path, "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}
	lc = &recordingLifecycle{}
	if err := writePIDFile(lc); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}
	if err := lc.hooks[0].OnStart(context.Background()); err == nil {
		t.Error("expected OnStart to fail when the pid file cannot be written")
	}
	if err := lc.hooks[0].OnStop(context.Background()); err == nil {
		t.Error("expected OnStop to fail when the pid file cannot be removed")
	}
	if _, _, err := ReadPID(); err == nil {
		t.Error("expected ReadPID to fail on an unreadable pid file")
	}
}

func TestReadPIDStaleFileFromKilledProcess(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not available")
	}
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting a process to kill: %v", err)
	}
	dead := cmd.Process.Pid
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	_ = cmd.Wait()

	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := PIDFilePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(dead)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	pid, running, err := ReadPID()
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if pid != dead {
		t.Errorf("pid = %d, want the recorded %d", pid, dead)
	}
	if running {
		t.Error("a killed process's pid file must report not running")
	}
}

func TestReadPIDRejectsNonPositivePIDs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := PIDFilePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, recorded := range []string{"0", "-1"} {
		if err := os.WriteFile(path, []byte(recorded+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, running, err := ReadPID(); err != nil || running {
			t.Errorf("pid %q: got running=%v err=%v, want false/nil", recorded, running, err)
		}
	}
}

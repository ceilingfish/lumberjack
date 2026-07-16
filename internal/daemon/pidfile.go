package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"go.uber.org/fx"
)

// PIDFilePath resolves the daemon's pid file (~/.lumberjack/daemon.pid). The
// daemon writes it on startup and removes it on shutdown; the CLI's
// `daemon status` reads it to report the live process id.
func PIDFilePath() (string, error) {
	dir, err := lumberjackDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.pid"), nil
}

// writePIDFile registers a lifecycle hook that records the running process's pid
// on start and removes the file on stop. It mirrors runServer's shape so the pid
// file's lifetime tracks the daemon's exactly.
func writePIDFile(lc fx.Lifecycle) error {
	path, err := PIDFilePath()
	if err != nil {
		return err
	}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return fmt.Errorf("creating pid file dir: %w", err)
			}
			pid := strconv.Itoa(os.Getpid())
			if err := os.WriteFile(path, []byte(pid+"\n"), 0o600); err != nil {
				return fmt.Errorf("writing pid file: %w", err)
			}
			return nil
		},
		OnStop: func(context.Context) error {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing pid file: %w", err)
			}
			return nil
		},
	})
	return nil
}

// ReadPID returns the pid recorded in the pid file and whether that process is
// currently alive. A missing pid file (or a pid whose process has gone) reports
// running=false with no error, which is the normal "daemon not running" case.
func ReadPID() (pid int, running bool, err error) {
	path, err := PIDFilePath()
	if err != nil {
		return 0, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("reading pid file: %w", err)
	}
	pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false, fmt.Errorf("parsing pid file %s: %w", path, err)
	}
	return pid, processAlive(pid), nil
}

// processAlive reports whether a process with the given pid exists. signal 0
// performs the existence/permission check without delivering a signal.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

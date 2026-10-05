package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// instance is one checkout's docs viewer. A server holds a POSIX write lock
// on dir/lock for its whole life and publishes its PID and URL in
// dir/state.json once it listens. The kernel drops the lock when the process
// dies, so a crashed server never looks alive, and F_GETLK names the holder's
// PID without taking the lock, so probing never races a starting server.
type instance struct {
	dir string // The lock and state files.
	log string // Output of a background server.
}

type state struct {
	PID int    `json:"pid"`
	URL string `json:"url"`
}

const (
	startWait = 30 * time.Second
	stopWait  = 10 * time.Second
	pollEvery = 50 * time.Millisecond
)

func (in instance) lockPath() string  { return filepath.Join(in.dir, "lock") }
func (in instance) statePath() string { return filepath.Join(in.dir, "state.json") }

func (in instance) openLock() (*os.File, error) {
	if err := os.MkdirAll(in.dir, 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(in.lockPath(), os.O_CREATE|os.O_RDWR, 0o600)
}

// hold takes the lock for a server's lifetime and clears stale state. Only
// the returned release may close the lock file: closing any descriptor of it
// drops a POSIX lock.
func (in instance) hold() (release func(), err error) {
	f, err := in.openLock()
	if err != nil {
		return nil, err
	}
	lock := syscall.Flock_t{Type: syscall.F_WRLCK}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &lock); err != nil {
		f.Close()
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EACCES) {
			if s, ok := in.read(); ok {
				return nil, fmt.Errorf("already running at %s (pid %d); the stop command stops it", s.URL, s.PID)
			}
			return nil, errors.New("already running; the stop command stops it")
		}
		return nil, err
	}
	_ = os.Remove(in.statePath())
	return func() {
		_ = os.Remove(in.statePath())
		f.Close()
	}, nil
}

// holder returns the PID of the running server, or 0.
func (in instance) holder() (int, error) {
	f, err := in.openLock()
	if err != nil {
		return 0, err
	}
	defer f.Close()
	lock := syscall.Flock_t{Type: syscall.F_WRLCK}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_GETLK, &lock); err != nil {
		return 0, err
	}
	if lock.Type == syscall.F_UNLCK {
		return 0, nil
	}
	return int(lock.Pid), nil
}

// publish records the server's URL once it is listening.
func (in instance) publish(url string) error {
	data, err := json.Marshal(state{PID: os.Getpid(), URL: url})
	if err != nil {
		return err
	}
	tmp := in.statePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, in.statePath())
}

func (in instance) read() (state, bool) {
	var s state
	data, err := os.ReadFile(in.statePath())
	if err != nil || json.Unmarshal(data, &s) != nil || s.PID == 0 {
		return state{}, false
	}
	return s, true
}

// start runs argv as a detached server that appends to the log, and waits
// until it publishes its URL.
func (in instance) start(ctx context.Context, argv []string) error {
	if pid, err := in.holder(); err != nil {
		return err
	} else if pid != 0 {
		in.report(pid)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(in.log), 0o700); err != nil {
		return err
	}
	log, err := os.OpenFile(in.log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	offset, err := log.Seek(0, io.SeekEnd)
	if err != nil {
		log.Close()
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = log, log
	// A new session keeps the terminal's Ctrl-C and hangup away from it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	log.Close()
	if err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	timeout := time.NewTimer(startWait)
	defer timeout.Stop()
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			return fmt.Errorf("server exited during startup (%v):\n%s", err, tail(in.log, offset))
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return ctx.Err()
		case <-timeout.C:
			_ = cmd.Process.Kill()
			return fmt.Errorf("server did not start within %s; see %s", startWait, in.log)
		case <-tick.C:
			if s, ok := in.read(); ok && s.PID == cmd.Process.Pid {
				fmt.Printf("docs viewer: %s (pid %d, log %s)\n", s.URL, s.PID, in.log)
				return nil
			}
		}
	}
}

// stop sends SIGTERM to the running server and waits for it to release the
// lock, then falls back to SIGKILL.
func (in instance) stop() error {
	pid, err := in.holder()
	if err != nil {
		return err
	}
	if pid == 0 {
		fmt.Println("docs viewer: not running")
		return nil
	}
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		if err := syscall.Kill(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		for deadline := time.Now().Add(stopWait); time.Now().Before(deadline); time.Sleep(pollEvery) {
			if now, err := in.holder(); err != nil {
				return err
			} else if now != pid {
				fmt.Printf("docs viewer: stopped (pid %d)\n", pid)
				return nil
			}
		}
	}
	return fmt.Errorf("pid %d still holds %s", pid, in.lockPath())
}

func (in instance) status() error {
	pid, err := in.holder()
	if err != nil {
		return err
	}
	if pid == 0 {
		fmt.Println("docs viewer: not running")
		return nil
	}
	in.report(pid)
	return nil
}

func (in instance) report(pid int) {
	if s, ok := in.read(); ok && s.PID == pid {
		fmt.Printf("docs viewer: %s (pid %d, log %s)\n", s.URL, pid, in.log)
		return
	}
	fmt.Printf("docs viewer: starting (pid %d, log %s)\n", pid, in.log)
}

// tail returns what the log gained after offset, capped to its last 4 KiB.
func tail(path string, offset int64) string {
	data, err := os.ReadFile(path)
	if err != nil || int64(len(data)) < offset {
		return ""
	}
	data = data[offset:]
	if len(data) > 4<<10 {
		data = data[len(data)-4<<10:]
	}
	return string(data)
}

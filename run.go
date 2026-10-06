package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func runCommand(dir string, argv []string) (int, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	if e := cmd.Start(); e != nil {
		return 0, e
	}
	done := make(chan struct{})
	defer close(done)
	// signal forward. os->cmd
	go func() {
		for {
			select {
			case s := <-signals:
				if s != syscall.SIGINT {
					cmd.Process.Signal(s)
				}
			case <-done:
				return
			}
		}
	}()
	e := cmd.Wait()
	if e == nil {
		return 0, nil
	}
	exit, ok := errors.AsType[*exec.ExitError](e)
	if !ok {
		return 0, e
	}
	if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal()), nil
	}
	return exit.ExitCode(), nil
}

const mountTimeout = 5 * time.Second

type mountOps struct {
	mount   func(v *view) error
	unmount func(path string) error
}

var systemMountOps = mountOps{mount: startMount, unmount: unmount}

func waitMounted(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if mounted, _ := isMounted(path); mounted {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("%s did not appear within %v", path, timeout)
}

// startMount runs vw mount in its own session, so Ctrl-C or a closed terminal does not unmount a
// view other sessions may be using, and returns once the view is mounted.
func startMount(v *view) error {
	self, e := os.Executable()
	if e != nil {
		return e
	}
	ready, w, e := os.Pipe()
	if e != nil {
		return e
	}
	defer ready.Close()
	cmd := exec.Command(self, "mount", "--ready-fd", "3", v.Path)
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{w}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	e = cmd.Start()
	w.Close()
	if e != nil {
		return e
	}
	var signalled [1]byte
	if n, _ := ready.Read(signalled[:]); n == 0 {
		cmd.Wait()
		return fmt.Errorf("mounting %s failed", v.Mount)
	}
	cmd.Process.Release()
	return waitMounted(v.Mount, mountTimeout)
}

func unmount(path string) error {
	out, e := exec.Command("umount", path).CombinedOutput()
	if e != nil {
		return fmt.Errorf("%w: %s", e, strings.TrimSpace(string(out)))
	}
	return nil
}

// startSession mounts the view if needed and joins its session.
func startSession(v *view, dir string, ops mountOps) (*session, error) {
	lock, e := lockExclusive(filepath.Join(dir, "start.lock"))
	if e != nil {
		return nil, e
	}
	defer lock.Close()
	mounted, e := isMounted(v.Mount)
	if e != nil {
		return nil, e
	}
	if !mounted {
		if e := ops.mount(v); e != nil {
			return nil, e
		}
		if e := os.WriteFile(filepath.Join(dir, "started"), nil, 0o644); e != nil {
			return nil, e
		}
	}
	return joinSession(filepath.Join(dir, "session.lock"))
}

// endSession leaves the session and, as the last one out, unmounts a view that startSession mounted.
// Problems are warnings on warn, so they never replace the command's exit status.
func endSession(v *view, dir string, sess *session, ops mountOps, warn io.Writer) {
	lock, e := lockExclusive(filepath.Join(dir, "start.lock"))
	if e != nil {
		fmt.Fprintf(warn, "vw: warning: %v\n", e)
		sess.close()
		return
	}
	defer lock.Close()
	defer sess.close()
	last, e := sess.leave()
	if e != nil {
		fmt.Fprintf(warn, "vw: warning: %v\n", e)
		return
	}
	started := filepath.Join(dir, "started")
	if _, e := os.Stat(started); !last || e != nil {
		return
	}
	if e := ops.unmount(v.Mount); e != nil {
		fmt.Fprintf(warn, "vw: warning: leaving %s mounted: %v\n", v.Mount, e)
		return
	}
	os.Remove(started)
}

// runView runs argv with the view as its working directory and returns its exit status. It mounts
// the view if needed and unmounts it when the last session ends.
func runView(arg string, argv []string) (int, error) {
	return runViewWith(arg, argv, systemMountOps, os.Stderr)
}

func runViewWith(arg string, argv []string, ops mountOps, warn io.Writer) (int, error) {
	v, e := openView(arg)
	if e != nil {
		return 0, e
	}
	dir, e := viewDataDir(v)
	if e != nil {
		return 0, e
	}
	sess, e := startSession(v, dir, ops)
	if e != nil {
		return 0, e
	}
	code, e := runCommand(v.Mount, argv)
	endSession(v, dir, sess, ops, warn)
	return code, e
}

func runCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	argv := args[1:]
	if len(argv) > 0 && argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	code, e := runView(args[0], argv)
	if e != nil {
		fatal(e)
	}
	os.Exit(code)
}

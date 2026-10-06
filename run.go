package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
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

// runView runs argv with the mounted view as its working directory and returns its exit status.
// It fails without running anything when the view is not mounted.
func runView(arg string, argv []string) (int, error) {
	v, e := openView(arg)
	if e != nil {
		return 0, e
	}
	mounted, e := isMounted(v.Mount)
	if e != nil {
		return 0, e
	}
	if !mounted {
		return 0, fmt.Errorf("%s is not mounted; run vw mount %s first", arg, arg)
	}
	dir, e := viewDataDir(v)
	if e != nil {
		return 0, e
	}
	sess, e := joinSession(filepath.Join(dir, "session.lock"))
	if e != nil {
		return 0, e
	}
	defer sess.close()
	return runCommand(v.Mount, argv)
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

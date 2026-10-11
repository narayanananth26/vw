package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mountedViewAtDev(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return writeViewFile(t, t.TempDir(), "x.view", "mount = \"/dev\"\n")
}

func TestRunViewReturnsTheCommandExitStatus(t *testing.T) {
	view := mountedViewAtDev(t)
	for _, want := range []int{0, 3} {
		got, err := runView(view, []string{"sh", "-c", "exit " + string(rune('0'+want))})
		if err != nil || got != want {
			t.Errorf("runView exit %d = %d, %v", want, got, err)
		}
	}
}

func TestRunViewStartsInTheMount(t *testing.T) {
	view := mountedViewAtDev(t)
	got, err := runView(view, []string{"sh", "-c", `test "$(pwd -P)" = /dev`})
	if err != nil || got != 0 {
		t.Errorf("runView = %d, %v, want the command to start in /dev", got, err)
	}
}

func TestRunViewReportsASignalledCommandAs128PlusTheSignal(t *testing.T) {
	view := mountedViewAtDev(t)
	got, err := runView(view, []string{"sh", "-c", "kill -TERM $$"})
	if err != nil || got != 128+15 {
		t.Errorf("runView = %d, %v, want 143", got, err)
	}
}

func TestRunViewFailsOnACommandThatCannotStart(t *testing.T) {
	view := mountedViewAtDev(t)
	if _, err := runView(view, []string{"vw-no-such-command"}); err == nil {
		t.Error("runView succeeded, want an error")
	}
}

func TestRunViewLeavesTheSessionWhenTheCommandEnds(t *testing.T) {
	view := mountedViewAtDev(t)
	if _, err := runView(view, []string{"true"}); err != nil {
		t.Fatal(err)
	}
	v, err := openView(view)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := viewDataDir(v)
	if err != nil {
		t.Fatal(err)
	}
	s := mustJoin(t, filepath.Join(dir, "session.lock"))
	defer s.close()
	if !mustLeave(t, s) {
		t.Error("a finished run still holds its session")
	}
}

type fakeOps struct {
	mounts, unmounts []string
	mountErr         error
	unmountErr       error
}

func (self *fakeOps) ops() mountOps {
	return mountOps{
		mount: func(v *view) error {
			self.mounts = append(self.mounts, v.Mount)
			if self.mountErr != nil {
				return self.mountErr
			}
			return os.MkdirAll(v.Mount, 0o755)
		},
		unmount: func(path string) error {
			self.unmounts = append(self.unmounts, path)
			return self.unmountErr
		},
	}
}

func unmountedView(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	mount := filepath.Join(dir, "mnt")
	return writeViewFile(t, dir, "idle.view", "mount = \""+mount+"\"\n"), mount
}

func startedMarker(t *testing.T, view string) string {
	t.Helper()
	v, err := openView(view)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := viewDataDir(v)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "started")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestRunViewMountsAnUnmountedViewAndUnmountsItAfterwards(t *testing.T) {
	view, mount := unmountedView(t)
	var fake fakeOps
	var warn bytes.Buffer
	got, err := runViewWith(view, []string{"sh", "-c", "exit 4"}, fake.ops(), &warn)
	if err != nil || got != 4 {
		t.Fatalf("runViewWith = %d, %v, want 4", got, err)
	}
	if len(fake.mounts) != 1 || fake.mounts[0] != mount || len(fake.unmounts) != 1 || fake.unmounts[0] != mount {
		t.Errorf("mounts = %v, unmounts = %v, want one of each at %s", fake.mounts, fake.unmounts, mount)
	}
	if exists(startedMarker(t, view)) || warn.Len() != 0 {
		t.Errorf("marker left behind or warning %q", warn.String())
	}
}

func TestRunViewLeavesAViewItDidNotMount(t *testing.T) {
	view := mountedViewAtDev(t)
	var fake fakeOps
	if _, err := runViewWith(view, []string{"true"}, fake.ops(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(fake.mounts) != 0 || len(fake.unmounts) != 0 {
		t.Errorf("mounts = %v, unmounts = %v, want none", fake.mounts, fake.unmounts)
	}
}

func TestRunViewDoesNotRunWhenTheMountFails(t *testing.T) {
	view, _ := unmountedView(t)
	fake := fakeOps{mountErr: errors.New("boom")}
	got, err := runViewWith(view, []string{"sh", "-c", "exit 9"}, fake.ops(), &bytes.Buffer{})
	if err == nil || got != 0 {
		t.Errorf("runViewWith = %d, %v, want an error and no command", got, err)
	}
	if exists(startedMarker(t, view)) {
		t.Error("a failed mount left a started marker")
	}
	v, _ := openView(view)
	dir, _ := viewDataDir(v)
	s := mustJoin(t, filepath.Join(dir, "session.lock"))
	defer s.close()
	if !mustLeave(t, s) {
		t.Error("a failed mount left a session behind")
	}
}

func TestOnlyTheLastSessionUnmountsAViewRunStarted(t *testing.T) {
	view := mountedViewAtDev(t)
	marker := startedMarker(t, view)
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	v, _ := openView(view)
	dir, _ := viewDataDir(v)
	var fake fakeOps
	ops := fake.ops()
	a, err := startSession(v, dir, ops)
	if err != nil {
		t.Fatal(err)
	}
	b, err := startSession(v, dir, ops)
	if err != nil {
		t.Fatal(err)
	}
	endSession(v, dir, a, ops, &bytes.Buffer{})
	if len(fake.unmounts) != 0 {
		t.Fatal("unmounted while a session was still running")
	}
	endSession(v, dir, b, ops, &bytes.Buffer{})
	if len(fake.unmounts) != 1 || exists(marker) {
		t.Errorf("unmounts = %v, marker present = %v, want one unmount and no marker", fake.unmounts, exists(marker))
	}
}

func TestRunViewKeepsTheExitStatusWhenUnmountFails(t *testing.T) {
	view, mount := unmountedView(t)
	fake := fakeOps{unmountErr: errors.New("Resource busy")}
	var warn bytes.Buffer
	got, err := runViewWith(view, []string{"sh", "-c", "exit 4"}, fake.ops(), &warn)
	if err != nil || got != 4 {
		t.Fatalf("runViewWith = %d, %v, want 4", got, err)
	}
	if !strings.Contains(warn.String(), mount) || !strings.Contains(warn.String(), "Resource busy") {
		t.Errorf("warning = %q, want the mount point and the reason", warn.String())
	}
	if !exists(startedMarker(t, view)) {
		t.Error("marker removed although the view is still mounted")
	}
}

package main

import (
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

func TestRunViewRefusesAnUnmountedView(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	view := writeViewFile(t, dir, "idle.view", "mount = \""+filepath.Join(dir, "idle")+"\"\n")
	_, err := runView(view, []string{"true"})
	if err == nil || !strings.Contains(err.Error(), "not mounted") {
		t.Errorf("runView error = %v, want one saying the view is not mounted", err)
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

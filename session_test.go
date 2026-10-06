package main

import (
	"path/filepath"
	"testing"
	"time"
)

func lockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state", "lock")
}

func mustJoin(t *testing.T, path string) *session {
	t.Helper()
	s, err := joinSession(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustLeave(t *testing.T, s *session) bool {
	t.Helper()
	last, err := s.leave()
	if err != nil {
		t.Fatal(err)
	}
	return last
}

func TestOnlyTheLastSessionToLeaveIsLast(t *testing.T) {
	path := lockPath(t)
	a, b := mustJoin(t, path), mustJoin(t, path)
	defer a.close()
	defer b.close()
	if mustLeave(t, a) {
		t.Error("the first session to leave was reported as last while another is running")
	}
	if !mustLeave(t, b) {
		t.Error("the final session was not reported as last")
	}
}

func TestASessionThatDiesWithoutLeavingDoesNotCount(t *testing.T) {
	path := lockPath(t)
	a, b := mustJoin(t, path), mustJoin(t, path)
	defer b.close()
	a.close()
	if !mustLeave(t, b) {
		t.Error("a closed session still counted as running")
	}
}

func TestJoiningWaitsForTheLastSessionToFinishUnmounting(t *testing.T) {
	path := lockPath(t)
	last := mustJoin(t, path)
	if !mustLeave(t, last) {
		t.Fatal("a lone session was not reported as last")
	}
	joined := make(chan *session)
	go func() {
		s, err := joinSession(path)
		if err != nil {
			t.Error(err)
		}
		joined <- s
	}()
	select {
	case <-joined:
		t.Fatal("joined while the last session was still unmounting")
	case <-time.After(100 * time.Millisecond):
	}
	last.close()
	select {
	case s := <-joined:
		s.close()
	case <-time.After(5 * time.Second):
		t.Fatal("did not join after the last session closed")
	}
}

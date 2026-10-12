package main

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestLockExclusiveWaitsForTheHolderToClose(t *testing.T) {
	path := lockPath(t)
	first, err := lockExclusive(path)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() {
		second, err := lockExclusive(path)
		if err == nil {
			second.Close()
		}
		got <- err
	}()
	select {
	case <-got:
		t.Fatal("a second exclusive lock was granted while the first was held")
	case <-time.After(100 * time.Millisecond):
	}
	first.Close()
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the second lock never arrived after the first was released")
	}
}

func TestSessionsActiveSeesJoinedSessions(t *testing.T) {
	path := lockPath(t)
	if active, err := sessionsActive(path); err != nil || active {
		t.Fatalf("no lock file: sessionsActive = %v, %v, want false", active, err)
	}
	s := mustJoin(t, path)
	if active, err := sessionsActive(path); err != nil || !active {
		t.Errorf("joined: sessionsActive = %v, %v, want true", active, err)
	}
	mustLeave(t, s)
	s.close()
	if active, err := sessionsActive(path); err != nil || active {
		t.Errorf("left: sessionsActive = %v, %v, want false", active, err)
	}
}

func TestActiveSessionsListsRecordedSessionsUntilTheyClose(t *testing.T) {
	dir := t.TempDir()
	if got, err := activeSessions(dir); err != nil || len(got) != 0 {
		t.Fatalf("no sessions dir: activeSessions = %v, %v, want none", got, err)
	}
	s := mustJoin(t, filepath.Join(dir, "session.lock"))
	if err := s.record(dir, []string{"nvim", "."}); err != nil {
		t.Fatal(err)
	}
	want := []sessionInfo{{os.Getpid(), []string{"nvim", "."}}}
	if got, err := activeSessions(dir); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("recorded: activeSessions = %v, %v, want %v", got, err, want)
	}
	s.close()
	if got, err := activeSessions(dir); err != nil || len(got) != 0 {
		t.Errorf("closed: activeSessions = %v, %v, want none", got, err)
	}
}

func TestActiveSessionsRemovesRecordsOfDeadSessions(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "sessions", "4242")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte(`{"pid":4242,"command":["sh"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := activeSessions(dir); err != nil || len(got) != 0 {
		t.Errorf("activeSessions = %v, %v, want none", got, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale record still exists: %v", err)
	}
}

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type session struct {
	file       *os.File
	recordFile *os.File
	recordPath string
}

// sessionInfo describes a running session: the process and the command it runs.
type sessionInfo struct {
	PID     int      `json:"pid"`
	Command []string `json:"command"`
}

func joinSession(lockPath string) (*session, error) {
	if e := os.MkdirAll(filepath.Dir(lockPath), 0o755); e != nil {
		return nil, e
	}
	file, e := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if e != nil {
		return nil, e
	}
	if e := syscall.Flock(int(file.Fd()), syscall.LOCK_SH); e != nil {
		file.Close()
		return nil, e
	}
	return &session{file: file}, nil
}

// lockExclusive blocks until it holds an exclusive lock on path, which closing the file releases.
func lockExclusive(path string) (*os.File, error) {
	if e := os.MkdirAll(filepath.Dir(path), 0o755); e != nil {
		return nil, e
	}
	file, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if e != nil {
		return nil, e
	}
	if e := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); e != nil {
		file.Close()
		return nil, e
	}
	return file, nil
}

// sessionsActive reports whether any session holds the lock at lockPath.
func sessionsActive(lockPath string) (bool, error) {
	file, e := os.Open(lockPath)
	if errors.Is(e, os.ErrNotExist) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	defer file.Close()
	switch e := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e {
	case nil:
		return false, nil
	case syscall.EWOULDBLOCK:
		return true, nil
	default:
		return false, e
	}
}

func (self *session) leave() (bool, error) {
	fd := int(self.file.Fd())
	if e := syscall.Flock(fd, syscall.LOCK_UN); e != nil {
		return false, e
	}
	switch e := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e {
	case nil:
		return true, nil
	case syscall.EWOULDBLOCK:
		return false, nil
	default:
		return false, e
	}
}

// record writes this session's pid and argv to dir/sessions/<pid> for activeSessions. The record
// stays locked until close removes it.
func (self *session) record(dir string, argv []string) error {
	sessions := filepath.Join(dir, "sessions")
	if e := os.MkdirAll(sessions, 0o755); e != nil {
		return e
	}
	pid := os.Getpid()
	data, e := json.Marshal(sessionInfo{pid, argv})
	if e != nil {
		return e
	}
	// Written under a dot name and renamed, so a reader never sees an unlocked record.
	tmp := filepath.Join(sessions, "."+strconv.Itoa(pid))
	file, e := os.OpenFile(tmp, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if e != nil {
		return e
	}
	path := filepath.Join(sessions, strconv.Itoa(pid))
	if e := syscall.Flock(int(file.Fd()), syscall.LOCK_SH); e == nil {
		if _, e = file.Write(data); e == nil {
			e = os.Rename(tmp, path)
		}
		if e == nil {
			self.recordFile, self.recordPath = file, path
			return nil
		}
	}
	file.Close()
	os.Remove(tmp)
	return e
}

// activeSessions lists the sessions recorded under dir/sessions that are still running. A record
// whose lock is free belongs to a session that died, and is removed.
func activeSessions(dir string) ([]sessionInfo, error) {
	sessions := filepath.Join(dir, "sessions")
	entries, e := os.ReadDir(sessions)
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var list []sessionInfo
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(sessions, entry.Name())
		info, live, e := readRecord(path)
		if e != nil {
			return nil, e
		}
		if live {
			list = append(list, info)
		}
	}
	return list, nil
}

func readRecord(path string) (sessionInfo, bool, error) {
	var info sessionInfo
	file, e := os.Open(path)
	if errors.Is(e, os.ErrNotExist) {
		return info, false, nil
	}
	if e != nil {
		return info, false, e
	}
	defer file.Close()
	switch e := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e {
	case nil:
		os.Remove(path)
		return info, false, nil
	case syscall.EWOULDBLOCK:
	default:
		return info, false, e
	}
	data, e := os.ReadFile(path)
	if e != nil {
		return info, false, e
	}
	return info, json.Unmarshal(data, &info) == nil, nil
}

func (self *session) close() error {
	if self.recordFile != nil {
		os.Remove(self.recordPath)
		self.recordFile.Close()
	}
	return self.file.Close()
}

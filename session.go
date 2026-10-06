package main

import (
	"os"
	"path/filepath"
	"syscall"
)

type session struct {
	file *os.File
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

func (self *session) close() error {
	return self.file.Close()
}

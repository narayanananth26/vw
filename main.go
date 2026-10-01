package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/winfsp/cgofuse/examples/shared"
	"github.com/winfsp/cgofuse/fuse"

	"vw/core"
)

func trace(vals ...any) func(vals ...any) {
	uid, gid, _ := fuse.Getcontext()
	return shared.Trace(1, fmt.Sprintf("[uid=%v,gid=%v]", uid, gid), vals...)
}

func errno(err error) int {
	if nil == err {
		return 0
	}
	if en, ok := errors.AsType[syscall.Errno](err); ok {
		return -int(en)
	}
	return -fuse.EIO
}

var (
	_host *fuse.FileSystemHost
)

type viewFS struct {
	fuse.FileSystemBase
	root string
}

func (self *viewFS) real(path string) string {
	return filepath.Join(self.root, path)
}

func (self *viewFS) Statfs(path string, stat *fuse.Statfs_t) (errc int) {
	defer trace(path)(&errc, stat)
	path = self.real(path)
	stgo := syscall.Statfs_t{}
	errc = errno(syscall_Statfs(path, &stgo))
	copyFusestatfsFromGostatfs(stat, &stgo)
	return
}

func (self *viewFS) Mknod(path string, mode uint32, dev uint64) (errc int) {
	defer trace(path, mode, dev)(&errc)
	defer setuidgid()()
	path = self.real(path)
	return errno(syscall.Mknod(path, mode, int(dev)))
}

func (self *viewFS) Mkdir(path string, mode uint32) (errc int) {
	defer trace(path, mode)(&errc)
	defer setuidgid()()
	path = self.real(path)
	return errno(syscall.Mkdir(path, mode))
}

func (self *viewFS) Unlink(path string) (errc int) {
	defer trace(path)(&errc)
	path = self.real(path)
	return errno(syscall.Unlink(path))
}

func (self *viewFS) Rmdir(path string) (errc int) {
	defer trace(path)(&errc)
	path = self.real(path)
	return errno(syscall.Rmdir(path))
}

func (self *viewFS) Link(oldpath string, newpath string) (errc int) {
	defer trace(oldpath, newpath)(&errc)
	defer setuidgid()()
	oldpath = self.real(oldpath)
	newpath = self.real(newpath)
	return errno(syscall.Link(oldpath, newpath))
}

func (self *viewFS) Symlink(target string, newpath string) (errc int) {
	defer trace(target, newpath)(&errc)
	defer setuidgid()()
	newpath = self.real(newpath)
	return errno(syscall.Symlink(target, newpath))
}

func (self *viewFS) Readlink(path string) (errc int, target string) {
	defer trace(path)(&errc, &target)
	path = self.real(path)
	buff := [1024]byte{}
	n, e := syscall.Readlink(path, buff[:])
	if nil != e {
		return errno(e), ""
	}
	return 0, string(buff[:n])
}

func (self *viewFS) Rename(oldpath string, newpath string) (errc int) {
	defer trace(oldpath, newpath)(&errc)
	defer setuidgid()()
	oldpath = self.real(oldpath)
	newpath = self.real(newpath)
	return errno(syscall.Rename(oldpath, newpath))
}

func (self *viewFS) Chmod(path string, mode uint32) (errc int) {
	defer trace(path, mode)(&errc)
	path = self.real(path)
	return errno(syscall.Chmod(path, mode))
}

func (self *viewFS) Chown(path string, uid uint32, gid uint32) (errc int) {
	defer trace(path, uid, gid)(&errc)
	path = self.real(path)
	return errno(syscall.Lchown(path, int(uid), int(gid)))
}

func (self *viewFS) Utimens(path string, tmsp1 []fuse.Timespec) (errc int) {
	defer trace(path, tmsp1)(&errc)
	path = self.real(path)
	tmsp := [2]syscall.Timespec{}
	tmsp[0].Sec, tmsp[0].Nsec = tmsp1[0].Sec, tmsp1[0].Nsec
	tmsp[1].Sec, tmsp[1].Nsec = tmsp1[1].Sec, tmsp1[1].Nsec
	return errno(syscall.UtimesNano(path, tmsp[:]))
}

func (self *viewFS) Create(path string, flags int, mode uint32) (errc int, fh uint64) {
	defer trace(path, flags, mode)(&errc, &fh)
	defer setuidgid()()
	return self.open(path, flags, mode)
}

func (self *viewFS) Open(path string, flags int) (errc int, fh uint64) {
	defer trace(path, flags)(&errc, &fh)
	return self.open(path, flags, 0)
}

func (self *viewFS) open(path string, flags int, mode uint32) (errc int, fh uint64) {
	path = self.real(path)
	f, e := syscall.Open(path, flags, mode)
	if nil != e {
		return errno(e), ^uint64(0)
	}
	return 0, uint64(f)
}

func (self *viewFS) Getattr(path string, stat *fuse.Stat_t, fh uint64) (errc int) {
	defer trace(path, fh)(&errc, stat)
	stgo := syscall.Stat_t{}
	if ^uint64(0) == fh {
		path = self.real(path)
		errc = errno(syscall.Lstat(path, &stgo))
	} else {
		errc = errno(syscall.Fstat(int(fh), &stgo))
	}
	copyFusestatFromGostat(stat, &stgo)
	return
}

func (self *viewFS) Truncate(path string, size int64, fh uint64) (errc int) {
	defer trace(path, size, fh)(&errc)
	if ^uint64(0) == fh {
		path = self.real(path)
		errc = errno(syscall.Truncate(path, size))
	} else {
		errc = errno(syscall.Ftruncate(int(fh), size))
	}
	return
}

func (self *viewFS) Read(path string, buff []byte, ofst int64, fh uint64) (n int) {
	defer trace(path, buff, ofst, fh)(&n)
	n, e := syscall.Pread(int(fh), buff, ofst)
	if nil != e {
		return errno(e)
	}
	return n
}

func (self *viewFS) Write(path string, buff []byte, ofst int64, fh uint64) (n int) {
	defer trace(path, buff, ofst, fh)(&n)
	n, e := syscall.Pwrite(int(fh), buff, ofst)
	if nil != e {
		return errno(e)
	}
	return n
}

func (self *viewFS) Release(path string, fh uint64) (errc int) {
	defer trace(path, fh)(&errc)
	return errno(syscall.Close(int(fh)))
}

func (self *viewFS) Fsync(path string, datasync bool, fh uint64) (errc int) {
	defer trace(path, datasync, fh)(&errc)
	return errno(syscall.Fsync(int(fh)))
}

func (self *viewFS) Opendir(path string) (errc int, fh uint64) {
	defer trace(path)(&errc, &fh)
	path = self.real(path)
	f, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if nil != e {
		return errno(e), ^uint64(0)
	}
	return 0, uint64(f)
}

func (self *viewFS) Readdir(path string,
	fill func(name string, stat *fuse.Stat_t, ofst int64) bool,
	ofst int64,
	fh uint64) (errc int) {
	defer trace(path, fill, ofst, fh)(&errc)
	path = self.real(path)
	file, e := os.Open(path)
	if nil != e {
		return errno(e)
	}
	defer file.Close()
	nams, e := file.Readdirnames(0)
	if nil != e {
		return errno(e)
	}
	nams = append([]string{".", ".."}, nams...)
	for _, name := range nams {
		if !fill(name, nil, 0) {
			break
		}
	}
	return 0
}

func (self *viewFS) Releasedir(path string, fh uint64) (errc int) {
	defer trace(path, fh)(&errc)
	return errno(syscall.Close(int(fh)))
}

type memberFlags []string

func (self *memberFlags) String() string {
	return strings.Join(*self, " ")
}

func (self *memberFlags) Set(spec string) error {
	*self = append(*self, spec)
	return nil
}

// loadMembers parses each --member spec and points it at a real, symlink-free directory.
func loadMembers(specs []string) ([]core.Member, error) {
	members := make([]core.Member, 0, len(specs))
	for _, spec := range specs {
		m, e := core.ParseMember(spec)
		if nil != e {
			return nil, e
		}
		abs, e := filepath.Abs(m.Path)
		if nil != e {
			return nil, e
		}
		m.Path, e = filepath.EvalSymlinks(abs)
		if nil != e {
			return nil, e
		}
		fi, e := os.Stat(m.Path)
		if nil != e {
			return nil, e
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("%s is not a directory", m.Path)
		}
		members = append(members, m)
	}
	return members, nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "vw: %v\n", err)
	os.Exit(1)
}

func main() {
	syscall.Umask(0)
	usage := "usage: vw mount --member path[:name[:ro]]... <mountpoint> [fuse opts...]"
	if 2 > len(os.Args) || "mount" != os.Args[1] {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var specs memberFlags
	flags := flag.NewFlagSet("mount", flag.ExitOnError)
	flags.Usage = func() { fmt.Fprintln(os.Stderr, usage) }
	flags.Var(&specs, "member", "folder to show, as path[:name[:ro]]; repeat for more")
	flags.Parse(os.Args[2:])
	if 0 == len(specs) || 1 > flags.NArg() {
		flags.Usage()
		os.Exit(2)
	}
	members, err := loadMembers(specs)
	if nil != err {
		fatal(err)
	}
	if _, err := core.New(members); nil != err {
		fatal(err)
	}
	if 1 < len(members) {
		fatal(errors.New("more than one --member is not supported yet"))
	}
	mountpoint := flags.Arg(0)
	if fi, err := os.Stat(mountpoint); nil != err || !fi.IsDir() {
		fatal(fmt.Errorf("%s is not a directory", mountpoint))
	}
	fs := viewFS{root: members[0].Path}
	_host = fuse.NewFileSystemHost(&fs)
	// Mount returns false after Ctrl-C too, so its result can't tell a failed mount from a clean exit.
	_host.Mount(mountpoint, flags.Args()[1:])
}

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
	view    *core.View
	mounted fuse.Timespec
}

// real maps a view path to the real path inside a member, or an errno when it has none.
func (self *viewFS) real(path string) (string, int) {
	r := self.view.Resolve(path)
	if core.InMember != r.Kind {
		return "", -fuse.ENOENT
	}
	return r.Real, 0
}

func (self *viewFS) realWrite(path string) (string, int) {
	real, e := self.view.ResolveWrite(path)
	return real, errno(e)
}

func (self *viewFS) realRemove(path string) (string, int) {
	real, e := self.view.ResolveRemove(path)
	return real, errno(e)
}

// rootStat describes the view root, which has no real directory behind it.
func (self *viewFS) rootStat(stat *fuse.Stat_t) {
	*stat = fuse.Stat_t{}
	stat.Mode = fuse.S_IFDIR | 0555
	stat.Nlink = uint32(2 + len(self.view.Names()))
	stat.Uid, stat.Gid = uint32(os.Getuid()), uint32(os.Getgid())
	stat.Atim, stat.Mtim, stat.Ctim, stat.Birthtim = self.mounted, self.mounted, self.mounted, self.mounted
}

func (self *viewFS) Statfs(path string, stat *fuse.Statfs_t) (errc int) {
	defer trace(path)(&errc, stat)
	r := self.view.Resolve(path)
	if core.Root == r.Kind {
		r = self.view.Resolve("/" + self.view.Names()[0])
	}
	if core.InMember != r.Kind {
		return -fuse.ENOENT
	}
	stgo := syscall.Statfs_t{}
	errc = errno(syscall_Statfs(r.Real, &stgo))
	copyFusestatfsFromGostatfs(stat, &stgo)
	return
}

func (self *viewFS) Mknod(path string, mode uint32, dev uint64) (errc int) {
	defer trace(path, mode, dev)(&errc)
	defer setuidgid()()
	path, errc = self.realWrite(path)
	if 0 != errc {
		return
	}
	return errno(syscall.Mknod(path, mode, int(dev)))
}

func (self *viewFS) Mkdir(path string, mode uint32) (errc int) {
	defer trace(path, mode)(&errc)
	defer setuidgid()()
	path, errc = self.realWrite(path)
	if 0 != errc {
		return
	}
	return errno(syscall.Mkdir(path, mode))
}

func (self *viewFS) Unlink(path string) (errc int) {
	defer trace(path)(&errc)
	path, errc = self.realRemove(path)
	if 0 != errc {
		return
	}
	return errno(syscall.Unlink(path))
}

func (self *viewFS) Rmdir(path string) (errc int) {
	defer trace(path)(&errc)
	path, errc = self.realRemove(path)
	if 0 != errc {
		return
	}
	return errno(syscall.Rmdir(path))
}

func (self *viewFS) Link(oldpath string, newpath string) (errc int) {
	defer trace(oldpath, newpath)(&errc)
	defer setuidgid()()
	oldpath, errc = self.real(oldpath)
	if 0 != errc {
		return
	}
	newpath, errc = self.real(newpath)
	if 0 != errc {
		return
	}
	return errno(syscall.Link(oldpath, newpath))
}

func (self *viewFS) Symlink(target string, newpath string) (errc int) {
	defer trace(target, newpath)(&errc)
	defer setuidgid()()
	newpath, errc = self.realWrite(newpath)
	if 0 != errc {
		return
	}
	return errno(syscall.Symlink(target, newpath))
}

func (self *viewFS) Readlink(path string) (errc int, target string) {
	defer trace(path)(&errc, &target)
	path, errc = self.real(path)
	if 0 != errc {
		return
	}
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
	oldpath, errc = self.real(oldpath)
	if 0 != errc {
		return
	}
	newpath, errc = self.real(newpath)
	if 0 != errc {
		return
	}
	return errno(syscall.Rename(oldpath, newpath))
}

func (self *viewFS) Chmod(path string, mode uint32) (errc int) {
	defer trace(path, mode)(&errc)
	path, errc = self.realWrite(path)
	if 0 != errc {
		return
	}
	return errno(syscall.Chmod(path, mode))
}

func (self *viewFS) Chown(path string, uid uint32, gid uint32) (errc int) {
	defer trace(path, uid, gid)(&errc)
	path, errc = self.realWrite(path)
	if 0 != errc {
		return
	}
	return errno(syscall.Lchown(path, int(uid), int(gid)))
}

func (self *viewFS) Utimens(path string, tmsp1 []fuse.Timespec) (errc int) {
	defer trace(path, tmsp1)(&errc)
	path, errc = self.realWrite(path)
	if 0 != errc {
		return
	}
	tmsp := [2]syscall.Timespec{}
	tmsp[0].Sec, tmsp[0].Nsec = tmsp1[0].Sec, tmsp1[0].Nsec
	tmsp[1].Sec, tmsp[1].Nsec = tmsp1[1].Sec, tmsp1[1].Nsec
	return errno(syscall.UtimesNano(path, tmsp[:]))
}

func (self *viewFS) Create(path string, flags int, mode uint32) (errc int, fh uint64) {
	defer trace(path, flags, mode)(&errc, &fh)
	defer setuidgid()()
	path, errc = self.realWrite(path)
	return self.open(path, errc, flags, mode)
}

func (self *viewFS) Open(path string, flags int) (errc int, fh uint64) {
	defer trace(path, flags)(&errc, &fh)
	if syscall.O_RDONLY != flags&syscall.O_ACCMODE || 0 != flags&syscall.O_TRUNC {
		path, errc = self.realWrite(path)
	} else {
		path, errc = self.real(path)
	}
	return self.open(path, errc, flags, 0)
}

func (self *viewFS) open(path string, errc int, flags int, mode uint32) (int, uint64) {
	if 0 != errc {
		return errc, ^uint64(0)
	}
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
		if core.Root == self.view.Resolve(path).Kind {
			self.rootStat(stat)
			return 0
		}
		path, errc = self.real(path)
		if 0 != errc {
			return
		}
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
		path, errc = self.realWrite(path)
		if 0 != errc {
			return
		}
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

// Opendir hands out no file descriptor for the view root, so Releasedir must not close one.
func (self *viewFS) Opendir(path string) (errc int, fh uint64) {
	defer trace(path)(&errc, &fh)
	if core.Root == self.view.Resolve(path).Kind {
		return 0, ^uint64(0)
	}
	path, errc = self.real(path)
	if 0 != errc {
		return errc, ^uint64(0)
	}
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
	var nams []string
	switch r := self.view.Resolve(path); r.Kind {
	case core.Root:
		nams = self.view.Names()
	case core.InMember:
		var e error
		if nams, e = listDir(r.Real); nil != e {
			return errno(e)
		}
	default:
		return -fuse.ENOENT
	}
	nams = append([]string{".", ".."}, nams...)
	for _, name := range nams {
		if !fill(name, nil, 0) {
			break
		}
	}
	return 0
}

func listDir(path string) ([]string, error) {
	file, e := os.Open(path)
	if nil != e {
		return nil, e
	}
	defer file.Close()
	return file.Readdirnames(0)
}

func (self *viewFS) Releasedir(path string, fh uint64) (errc int) {
	defer trace(path, fh)(&errc)
	if ^uint64(0) == fh {
		return 0
	}
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
	view, err := core.New(members)
	if nil != err {
		fatal(err)
	}
	mountpoint := flags.Arg(0)
	if fi, err := os.Stat(mountpoint); nil != err || !fi.IsDir() {
		fatal(fmt.Errorf("%s is not a directory", mountpoint))
	}
	fs := viewFS{view: view, mounted: fuse.Now()}
	_host = fuse.NewFileSystemHost(&fs)
	// Mount returns false after Ctrl-C too, so its result can't tell a failed mount from a clean exit.
	_host.Mount(mountpoint, flags.Args()[1:])
}

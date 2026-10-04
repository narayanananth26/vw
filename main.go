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
	if err == nil {
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
	scratch string
	mounted fuse.Timespec
}

func (self *viewFS) real(path string) (string, int) {
	r := self.view.Resolve(path)
	if r.Real == "" {
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

func (self *viewFS) realPair(oldpath, newpath string) (string, string, int) {
	oldreal, newreal, e := self.view.ResolvePair(oldpath, newpath)
	return oldreal, newreal, errno(e)
}

// rootStat describes the view root, which has no real directory behind it.
func (self *viewFS) rootStat(stat *fuse.Stat_t) {
	*stat = fuse.Stat_t{}
	stat.Mode = fuse.S_IFDIR | 0755
	stat.Nlink = uint32(2 + len(self.view.Names()))
	stat.Uid, stat.Gid = uint32(os.Getuid()), uint32(os.Getgid())
	stat.Atim, stat.Mtim, stat.Ctim, stat.Birthtim = self.mounted, self.mounted, self.mounted, self.mounted
}

func (self *viewFS) Statfs(path string, stat *fuse.Statfs_t) (errc int) {
	defer trace(path)(&errc, stat)
	r := self.view.Resolve(path)
	if r.Kind == core.Root {
		r = self.view.Resolve("/" + self.view.Names()[0])
	}
	if r.Real == "" {
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
	if errc != 0 {
		return
	}
	return errno(syscall.Mknod(path, mode, int(dev)))
}

func (self *viewFS) Mkdir(path string, mode uint32) (errc int) {
	defer trace(path, mode)(&errc)
	defer setuidgid()()
	path, errc = self.realWrite(path)
	if errc != 0 {
		return
	}
	return errno(syscall.Mkdir(path, mode))
}

func (self *viewFS) Unlink(path string) (errc int) {
	defer trace(path)(&errc)
	path, errc = self.realRemove(path)
	if errc != 0 {
		return
	}
	return errno(syscall.Unlink(path))
}

func (self *viewFS) Rmdir(path string) (errc int) {
	defer trace(path)(&errc)
	path, errc = self.realRemove(path)
	if errc != 0 {
		return
	}
	return errno(syscall.Rmdir(path))
}

func (self *viewFS) Link(oldpath string, newpath string) (errc int) {
	defer trace(oldpath, newpath)(&errc)
	defer setuidgid()()
	oldpath, newpath, errc = self.realPair(oldpath, newpath)
	if errc != 0 {
		return
	}
	return errno(syscall.Link(oldpath, newpath))
}

func (self *viewFS) Symlink(target string, newpath string) (errc int) {
	defer trace(target, newpath)(&errc)
	defer setuidgid()()
	newpath, errc = self.realWrite(newpath)
	if errc != 0 {
		return
	}
	return errno(syscall.Symlink(target, newpath))
}

func (self *viewFS) Readlink(path string) (errc int, target string) {
	defer trace(path)(&errc, &target)
	path, errc = self.real(path)
	if errc != 0 {
		return
	}
	buff := [1024]byte{}
	n, e := syscall.Readlink(path, buff[:])
	if e != nil {
		return errno(e), ""
	}
	return 0, string(buff[:n])
}

func (self *viewFS) Rename(oldpath string, newpath string) (errc int) {
	defer trace(oldpath, newpath)(&errc)
	defer setuidgid()()
	oldpath, newpath, errc = self.realPair(oldpath, newpath)
	if errc != 0 {
		return
	}
	return errno(syscall.Rename(oldpath, newpath))
}

func (self *viewFS) Chmod(path string, mode uint32) (errc int) {
	defer trace(path, mode)(&errc)
	path, errc = self.realWrite(path)
	if errc != 0 {
		return
	}
	return errno(syscall.Chmod(path, mode))
}

func (self *viewFS) Chown(path string, uid uint32, gid uint32) (errc int) {
	defer trace(path, uid, gid)(&errc)
	path, errc = self.realWrite(path)
	if errc != 0 {
		return
	}
	return errno(syscall.Lchown(path, int(uid), int(gid)))
}

func (self *viewFS) Utimens(path string, tmsp1 []fuse.Timespec) (errc int) {
	defer trace(path, tmsp1)(&errc)
	path, errc = self.realWrite(path)
	if errc != 0 {
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
	if flags&syscall.O_ACCMODE != syscall.O_RDONLY || flags&syscall.O_TRUNC != 0 {
		path, errc = self.realWrite(path)
	} else {
		path, errc = self.real(path)
	}
	return self.open(path, errc, flags, 0)
}

func (self *viewFS) open(path string, errc int, flags int, mode uint32) (int, uint64) {
	if errc != 0 {
		return errc, ^uint64(0)
	}
	f, e := syscall.Open(path, flags, mode)
	if e != nil {
		return errno(e), ^uint64(0)
	}
	return 0, uint64(f)
}

func (self *viewFS) Getattr(path string, stat *fuse.Stat_t, fh uint64) (errc int) {
	defer trace(path, fh)(&errc, stat)
	stgo := syscall.Stat_t{}
	if fh == ^uint64(0) {
		if self.view.Resolve(path).Kind == core.Root {
			self.rootStat(stat)
			return 0
		}
		path, errc = self.real(path)
		if errc != 0 {
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
	if fh == ^uint64(0) {
		path, errc = self.realWrite(path)
		if errc != 0 {
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
	if e != nil {
		return errno(e)
	}
	return n
}

func (self *viewFS) Write(path string, buff []byte, ofst int64, fh uint64) (n int) {
	defer trace(path, buff, ofst, fh)(&n)
	n, e := syscall.Pwrite(int(fh), buff, ofst)
	if e != nil {
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
	if self.view.Resolve(path).Kind == core.Root {
		return 0, ^uint64(0)
	}
	path, errc = self.real(path)
	if errc != 0 {
		return errc, ^uint64(0)
	}
	f, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if e != nil {
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
		entries, e := listDir(self.scratch)
		if e != nil {
			return errno(e)
		}
		scratchNames := make([]string, len(entries))
		for i, entry := range entries {
			scratchNames[i] = entry.Name()
		}
		for _, name := range self.view.RootEntries(scratchNames) {
			if self.view.Visible("/"+name, true) {
				nams = append(nams, name)
			}
		}
	case core.InMember:
		listed := self.view.Visible(path, true)
		entries, e := listDir(r.Real)
		if e != nil {
			return errno(e)
		}
		for _, entry := range entries {
			if !listed || self.view.Visible(filepath.Join(path, entry.Name()), entry.IsDir()) {
				nams = append(nams, entry.Name())
			}
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

func listDir(path string) ([]os.DirEntry, error) {
	file, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer file.Close()
	return file.ReadDir(0)
}

func (self *viewFS) Releasedir(path string, fh uint64) (errc int) {
	defer trace(path, fh)(&errc)
	if fh == ^uint64(0) {
		return 0
	}
	return errno(syscall.Close(int(fh)))
}

type listFlag []string

func (self *listFlag) String() string {
	return strings.Join(*self, " ")
}

func (self *listFlag) Set(value string) error {
	*self = append(*self, value)
	return nil
}

type memberFlag []core.Member

func (self *memberFlag) String() string {
	paths := make([]string, len(*self))
	for i, m := range *self {
		paths[i] = m.Path
	}
	return strings.Join(paths, " ")
}

func (self *memberFlag) Set(spec string) error {
	m, e := core.ParseMember(spec)
	if e != nil {
		return e
	}
	*self = append(*self, m)
	return nil
}

// memberFilterFlag adds a pattern to the member given by the closest --member before it.
type memberFilterFlag struct {
	members *memberFlag
	exclude bool
}

func (self *memberFilterFlag) String() string {
	return ""
}

func (self *memberFilterFlag) Set(pattern string) error {
	if len(*self.members) == 0 {
		return errors.New("must come after a --member")
	}
	m := &(*self.members)[len(*self.members)-1]
	if self.exclude {
		m.Exclude = append(m.Exclude, pattern)
	} else {
		m.Include = append(m.Include, pattern)
	}
	return nil
}

type mountFlags struct {
	include, exclude listFlag
	members          memberFlag
	scratch          string
}

func (self *mountFlags) register(set *flag.FlagSet) {
	set.Var(&self.include, "include", "show only paths matching this gitignore pattern in the whole view; repeat for more")
	set.Var(&self.exclude, "exclude", "hide paths matching this gitignore pattern in the whole view; repeat for more")
	set.Var(&self.members, "member", "folder to show, as path[:name[:ro]]; repeat for more")
	set.StringVar(&self.scratch, "scratch", "", "folder holding files created at the view root; default ~/.local/share/vw/views/<mountpoint name>/root")
	set.Var(&memberFilterFlag{members: &self.members}, "member-include", "like --include, for the closest --member before it")
	set.Var(&memberFilterFlag{members: &self.members, exclude: true}, "member-exclude", "like --exclude, for the closest --member before it")
}

func scratchDir(flagValue, mountpoint string) (string, error) {
	if flagValue != "" {
		return filepath.Abs(flagValue)
	}
	abs, e := filepath.Abs(mountpoint)
	if e != nil {
		return "", e
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(home, ".local", "share", "vw", "views", filepath.Base(abs), "root"), nil
}

// loadMembers points each member at a real, symlink-free directory or regular file.
func loadMembers(members []core.Member) ([]core.Member, error) {
	for i := range members {
		m := &members[i]
		abs, e := filepath.Abs(m.Path)
		if e != nil {
			return nil, e
		}
		m.Path, e = filepath.EvalSymlinks(abs)
		if e != nil {
			return nil, e
		}
		fi, e := os.Stat(m.Path)
		if e != nil {
			return nil, e
		}
		switch {
		case fi.IsDir():
		case fi.Mode().IsRegular():
			m.File = true
		default:
			return nil, fmt.Errorf("%s is not a directory or regular file", m.Path)
		}
	}
	return members, nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "vw: %v\n", err)
	os.Exit(1)
}

func main() {
	syscall.Umask(0)
	usage := "usage: vw mount [--include pattern]... [--exclude pattern]... [--scratch dir]\n" +
		"       --member path[:name[:ro]] [--member-include pattern]... [--member-exclude pattern]... ...\n" +
		"       <mountpoint> [fuse opts...]"
	if len(os.Args) < 2 || os.Args[1] != "mount" {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var mount mountFlags
	flags := flag.NewFlagSet("mount", flag.ExitOnError)
	flags.Usage = func() { fmt.Fprintln(os.Stderr, usage) }
	mount.register(flags)
	flags.Parse(os.Args[2:])
	if len(mount.members) == 0 || flags.NArg() < 1 {
		flags.Usage()
		os.Exit(2)
	}
	members, err := loadMembers(mount.members)
	if err != nil {
		fatal(err)
	}
	mountpoint := flags.Arg(0)
	if fi, err := os.Stat(mountpoint); err != nil || !fi.IsDir() {
		fatal(fmt.Errorf("%s is not a directory", mountpoint))
	}
	scratch, err := scratchDir(mount.scratch, mountpoint)
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		fatal(err)
	}
	view, err := core.New(members, core.WithInclude(mount.include...), core.WithExclude(mount.exclude...), core.WithScratch(scratch))
	if err != nil {
		fatal(err)
	}
	fs := viewFS{view: view, scratch: scratch, mounted: fuse.Now()}
	_host = fuse.NewFileSystemHost(&fs)
	// Mount returns false after Ctrl-C too, so its result can't tell a failed mount from a clean exit.
	_host.Mount(mountpoint, flags.Args()[1:])
}

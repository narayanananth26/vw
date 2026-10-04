package main

import (
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/winfsp/cgofuse/fuse"

	"vw/core"
)

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
	readyFD          int
}

func (self *mountFlags) register(set *flag.FlagSet) {
	set.Var(&self.include, "include", "show only paths matching this gitignore pattern in the whole view; repeat for more")
	set.Var(&self.exclude, "exclude", "hide paths matching this gitignore pattern in the whole view; repeat for more")
	set.Var(&self.members, "member", "folder to show, as path[:name[:ro]]; repeat for more")
	set.StringVar(&self.scratch, "scratch", "", "folder holding files created at the view root; default ~/.local/share/vw/views/<mountpoint name>/root")
	set.IntVar(&self.readyFD, "ready-fd", 0, "internal: file descriptor to write one byte to once the view is mounted")
	set.Var(&memberFilterFlag{members: &self.members}, "member-include", "like --include, for the closest --member before it")
	set.Var(&memberFilterFlag{members: &self.members, exclude: true}, "member-exclude", "like --exclude, for the closest --member before it")
}

// returns absolute path where scratch dir will be mounted
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

// loadMember points m at a real, symlink-free directory or regular file.
func loadMember(m core.Member) (core.Member, error) {
	abs, e := filepath.Abs(m.Path)
	if e != nil {
		return m, e
	}
	m.Path, e = filepath.EvalSymlinks(abs)
	if e != nil {
		return m, e
	}
	fi, e := os.Stat(m.Path)
	if e != nil {
		return m, e
	}
	switch {
	case fi.IsDir():
	case fi.Mode().IsRegular():
		m.File = true
	default:
		return m, fmt.Errorf("%s is not a directory or regular file", m.Path)
	}
	return m, nil
}

// loadMembers loads every member it can and reports the ones it had to leave out.
func loadMembers(members []core.Member) (loaded []core.Member, skipped []error) {
	for _, m := range members {
		m, e := loadMember(m)
		if e != nil {
			skipped = append(skipped, fmt.Errorf("member %q: %w", m.Name, e))
			continue
		}
		loaded = append(loaded, m)
	}
	return loaded, skipped
}

func deviceOf(path string) (int32, error) {
	fi, e := os.Stat(path)
	if e != nil {
		return 0, e
	}
	return fi.Sys().(*syscall.Stat_t).Dev, nil
}

// isMounted reports whether a filesystem is mounted at path, which holds when path is on a
// different device from its parent. A path that does not exist is not mounted.
func isMounted(path string) (bool, error) {
	abs, e := filepath.Abs(path)
	if e != nil {
		return false, e
	}
	dev, e := deviceOf(abs)
	if errors.Is(e, os.ErrNotExist) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	parent, e := deviceOf(filepath.Dir(abs))
	if e != nil {
		return false, e
	}
	return dev != parent, nil
}

// checkMountPoint refuses a directory that is already a mount or would hide files if mounted over.
func checkMountPoint(path string) error {
	mounted, e := isMounted(path)
	if e != nil {
		return e
	}
	if mounted {
		return fmt.Errorf("%s is already a mount point", path)
	}
	entries, e := os.ReadDir(path)
	if e != nil {
		return e
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s is not empty", path)
	}
	return nil
}

const mountUsage = "usage: vw mount <view|file> [--scratch dir] [fuse opts...]\n" +
	"       vw mount [--include pattern]... [--exclude pattern]... [--scratch dir]\n" +
	"           --member path[:name[:ro]] [--member-include pattern]... [--member-exclude pattern]... ...\n" +
	"           <mountpoint> [fuse opts...]"

type mountSpec struct {
	members          []core.Member
	include, exclude []string
	mountpoint       string
	scratch          string
	fuseOpts         []string
	readyFD          int
}

// viewScratchDir keys a view's scratch directory by its file path, so two views with the same
// name in different places keep separate root files.
func viewScratchDir(flagValue string, v *view) (string, error) {
	if flagValue != "" {
		return filepath.Abs(flagValue)
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256([]byte(v.Path))
	return filepath.Join(home, ".local", "share", "vw", "views", fmt.Sprintf("%s-%x", v.Name, sum[:4]), "root"), nil
}

func flagMountSpec(mount *mountFlags, args []string) (mountSpec, error) {
	scratch, e := scratchDir(mount.scratch, args[0])
	if e != nil {
		return mountSpec{}, e
	}
	return mountSpec{members: mount.members, include: mount.include, exclude: mount.exclude, mountpoint: args[0], scratch: scratch, fuseOpts: args[1:], readyFD: mount.readyFD}, nil
}

func viewMountSpec(mount *mountFlags, args []string) (mountSpec, error) {
	if len(mount.include) > 0 || len(mount.exclude) > 0 {
		return mountSpec{}, errors.New("--include and --exclude need --member; a view file sets its own filters")
	}
	path, e := resolveView(args[0])
	if e != nil {
		return mountSpec{}, e
	}
	v, e := loadViewFile(path)
	if e != nil {
		return mountSpec{}, e
	}
	if e := os.MkdirAll(v.Mount, 0o755); e != nil {
		return mountSpec{}, e
	}
	scratch, e := viewScratchDir(mount.scratch, v)
	if e != nil {
		return mountSpec{}, e
	}
	return mountSpec{members: v.Members, include: v.Include, exclude: v.Exclude, mountpoint: v.Mount, scratch: scratch, fuseOpts: args[1:], readyFD: mount.readyFD}, nil
}

// readyFile wraps the descriptor a parent passed in, or returns nil when there is none. Descriptors
// 0 to 2 are the standard streams, so they never count.
func readyFile(fd int) *os.File {
	if fd < 3 {
		return nil
	}
	return os.NewFile(uintptr(fd), "ready")
}

func mountCmd(args []string) {
	var mount mountFlags
	flags := flag.NewFlagSet("mount", flag.ExitOnError)
	flags.Usage = func() { fmt.Fprintln(os.Stderr, mountUsage) }
	mount.register(flags)
	flags.Parse(args)
	if flags.NArg() < 1 {
		flags.Usage()
		os.Exit(2)
	}
	build := viewMountSpec
	if len(mount.members) > 0 {
		build = flagMountSpec
	}
	spec, err := build(&mount, flags.Args())
	if err != nil {
		fatal(err)
	}
	members, skipped := loadMembers(spec.members)
	for _, e := range skipped {
		fmt.Fprintf(os.Stderr, "vw: warning: skipping %v\n", e)
	}
	if len(members) == 0 {
		fatal(errors.New("no usable members"))
	}
	if fi, err := os.Stat(spec.mountpoint); err != nil || !fi.IsDir() {
		fatal(fmt.Errorf("%s is not a directory", spec.mountpoint))
	}
	if err := checkMountPoint(spec.mountpoint); err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(spec.scratch, 0o755); err != nil {
		fatal(err)
	}
	view, err := core.New(members, core.WithInclude(spec.include...), core.WithExclude(spec.exclude...), core.WithScratch(spec.scratch))
	if err != nil {
		fatal(err)
	}
	fs := viewFS{view: view, scratch: spec.scratch, mounted: fuse.Now(), ready: readyFile(spec.readyFD)}
	_host = fuse.NewFileSystemHost(&fs)
	// Mount returns false after Ctrl-C too, so its result can't tell a failed mount from a clean exit.
	_host.Mount(spec.mountpoint, spec.fuseOpts)
}

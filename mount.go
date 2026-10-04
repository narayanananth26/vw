package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
}

func (self *mountFlags) register(set *flag.FlagSet) {
	set.Var(&self.include, "include", "show only paths matching this gitignore pattern in the whole view; repeat for more")
	set.Var(&self.exclude, "exclude", "hide paths matching this gitignore pattern in the whole view; repeat for more")
	set.Var(&self.members, "member", "folder to show, as path[:name[:ro]]; repeat for more")
	set.StringVar(&self.scratch, "scratch", "", "folder holding files created at the view root; default ~/.local/share/vw/views/<mountpoint name>/root")
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

const mountUsage = "usage: vw mount [--include pattern]... [--exclude pattern]... [--scratch dir]\n" +
	"       --member path[:name[:ro]] [--member-include pattern]... [--member-exclude pattern]... ...\n" +
	"       <mountpoint> [fuse opts...]"

func mountCmd(args []string) {
	var mount mountFlags
	flags := flag.NewFlagSet("mount", flag.ExitOnError)
	flags.Usage = func() { fmt.Fprintln(os.Stderr, mountUsage) }
	mount.register(flags)
	flags.Parse(args)
	if len(mount.members) == 0 || flags.NArg() < 1 {
		flags.Usage()
		os.Exit(2)
	}
	members, skipped := loadMembers(mount.members)
	for _, e := range skipped {
		fmt.Fprintf(os.Stderr, "vw: warning: skipping %v\n", e)
	}
	if len(members) == 0 {
		fatal(errors.New("no usable members"))
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

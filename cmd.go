package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/pelletier/go-toml/v2"
)

const usage = "usage: vw <command>\n" +
	"  mount <view|file> [--scratch dir] [fuse opts...]\n" +
	"  mount --member path[:name[:ro]]... <mountpoint> [fuse opts...]   see vw mount for all flags\n" +
	"  list [<view>]     list views, or one view's members\n" +
	"  path <view>     print a view's mount point\n" +
	"  edit <view>     open a view file in $EDITOR\n" +
	"  run <view> [--] <command> [args...]   run a command inside a view, mounting it if needed\n" +
	"  shell <view>    open $SHELL inside a view, mounting it if needed\n" +
	"  new <view> [path...]   create a view; a name goes in the central directory, a file path stays where it is"

// listViews prints the name of every central view.
func listViews(w io.Writer) error {
	dir, e := viewsDir()
	if e != nil {
		return e
	}
	entries, e := os.ReadDir(dir)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	var names []string
	for _, entry := range entries {
		if name, ok := strings.CutSuffix(entry.Name(), ".toml"); ok && !entry.IsDir() {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		fmt.Fprintln(w, name)
	}
	return nil
}

// listMembers prints the mount point of a view and then one line per member.
func listMembers(w io.Writer, arg string) error {
	v, e := openView(arg)
	if e != nil {
		return e
	}
	fmt.Fprintf(w, "mount %s\n", v.Mount)
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, m := range v.Members {
		var notes []string
		if m.ReadOnly {
			notes = append(notes, "ro")
		}
		if _, e := os.Stat(m.Path); e != nil {
			notes = append(notes, "missing")
		}
		fmt.Fprintf(table, "%s\t%s\t%s\n", m.Name, m.Path, strings.Join(notes, " "))
	}
	return table.Flush()
}

func openView(arg string) (*view, error) {
	path, e := resolveView(arg)
	if e != nil {
		return nil, e
	}
	return loadViewFile(path)
}

func listCmd(args []string) {
	var e error
	switch len(args) {
	case 0:
		e = listViews(os.Stdout)
	case 1:
		e = listMembers(os.Stdout, args[0])
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if e != nil {
		fatal(e)
	}
}

func pathCmd(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	v, e := openView(args[0])
	if e != nil {
		fatal(e)
	}
	fmt.Println(filepath.Clean(v.Mount))
}

// noticeIfMounted tells the user to remount when v is mounted, because a mounted view keeps the
// settings it mounted with.
func noticeIfMounted(v *view, arg string, w io.Writer) {
	if mounted, _ := isMounted(v.Mount); mounted {
		fmt.Fprintf(w, "%s is mounted; remount to apply your changes\n", arg)
	}
}

// editView opens a view file in the editor, then says to remount if the view is mounted.
func editView(arg string, editor []string, notice io.Writer) error {
	path, e := resolveView(arg)
	if e != nil {
		return e
	}
	if _, e := os.Stat(path); e != nil {
		return e
	}
	before, _ := loadViewFile(path)
	cmd := exec.Command(editor[0], append(editor[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if e := cmd.Run(); e != nil {
		return e
	}
	if before != nil {
		noticeIfMounted(before, arg, notice)
	}
	return nil
}

func editCmd(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	editor := strings.Fields(os.Getenv("EDITOR"))
	if len(editor) == 0 {
		editor = []string{"vi"}
	}
	if e := editView(args[0], editor, os.Stderr); e != nil {
		fatal(e)
	}
}

// memberPath is how a view file records the member path p. A central view records it absolute, and
// a view file elsewhere records it relative to itself.
func memberPath(arg, file, p string) (string, error) {
	member, e := filepath.Abs(p)
	if e != nil {
		return "", e
	}
	if isViewFilePath(arg) {
		return filepath.Rel(filepath.Dir(file), member)
	}
	return member, nil
}

// newView writes a view file with one member per path and returns where it went.
func newView(arg string, paths []string) (string, error) {
	path, e := resolveView(arg)
	if e != nil {
		return "", e
	}
	file, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	var f viewFile
	for _, p := range paths {
		member, e := memberPath(arg, file, p)
		if e != nil {
			return "", e
		}
		f.Member = append(f.Member, viewMemberFile{Path: member})
	}
	data, e := toml.Marshal(f)
	if e != nil {
		return "", e
	}
	if e := os.MkdirAll(filepath.Dir(file), 0o755); e != nil {
		return "", e
	}
	out, e := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(e, os.ErrExist) {
		return "", fmt.Errorf("%s already exists", file)
	}
	if e != nil {
		return "", e
	}
	if _, e := out.Write(data); e != nil {
		out.Close()
		return "", e
	}
	return file, out.Close()
}

func newCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	path, e := newView(args[0], args[1:])
	if e != nil {
		fatal(e)
	}
	fmt.Println(path)
}

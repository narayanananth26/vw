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
)

const usage = "usage: vw <command>\n" +
	"  mount <view|file> [--scratch dir] [fuse opts...]\n" +
	"  mount --member path[:name[:ro]]... <mountpoint> [fuse opts...]   see vw mount for all flags\n" +
	"  ls [<view>]     list views, or one view's members\n" +
	"  path <view>     print a view's mount point\n" +
	"  edit <view>     open a view file in $EDITOR"

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

func lsCmd(args []string) {
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

// editView opens a view file in the editor. A view that is mounted keeps its old settings, so it
// says so once the editor exits.
func editView(arg string, editor []string, notice io.Writer) error {
	path, e := resolveView(arg)
	if e != nil {
		return e
	}
	if _, e := os.Stat(path); e != nil {
		return e
	}
	mounted := false
	if v, e := loadViewFile(path); e == nil {
		mounted, _ = isMounted(v.Mount)
	}
	cmd := exec.Command(editor[0], append(editor[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if e := cmd.Run(); e != nil {
		return e
	}
	if mounted {
		fmt.Fprintf(notice, "%s is mounted; remount to apply your changes\n", arg)
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

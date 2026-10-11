package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/pelletier/go-toml/v2"

	"vw/core"
)

const usage = "usage: vw <command>\n" +
	"  mount <view|file> [--scratch dir] [fuse opts...]\n" +
	"  mount --member path [--as name] [--ro]... <mountpoint> [fuse opts...]   see vw mount for all flags\n" +
	"  list [<view>] [--json]   list views, or one view's members\n" +
	"  path <view>     print a view's mount point\n" +
	"  edit <view>     open a view file in $EDITOR\n" +
	"  run <view> [--] <command> [args...]   run a command inside a view, mounting it if needed\n" +
	"  shell <view>    open $SHELL inside a view, mounting it if needed\n" +
	"  new <view> [path [--as name] [--ro]]... [--mount path]   create a view; a name goes in the central directory, a file path stays where it is\n" +
	"  add <view> <path> [--as name] [--ro]   add a member to a view\n" +
	"  rm <view> <member>   remove a member from a view"

// listViews prints the name of every central view, or a JSON array of their names and files.
func listViews(w io.Writer, asJSON bool) error {
	dir, e := viewsDir()
	if e != nil {
		return e
	}
	entries, e := os.ReadDir(dir)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	var names []string
	for _, entry := range entries {
		if name, ok := strings.CutSuffix(entry.Name(), ".toml"); ok && !entry.IsDir() {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if asJSON {
		type entry struct {
			Name string `json:"name"`
			File string `json:"file"`
		}
		list := []entry{}
		for _, name := range names {
			list = append(list, entry{name, filepath.Join(dir, name+".toml")})
		}
		return writeJSON(w, list)
	}
	for _, name := range names {
		fmt.Fprintln(w, name)
	}
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// listMembers prints the mount point of a view and then one line per member, or the same as JSON.
func listMembers(w io.Writer, arg string, asJSON bool) error {
	v, e := openView(arg)
	if e != nil {
		return e
	}
	if asJSON {
		type member struct {
			Name    string `json:"name"`
			Path    string `json:"path"`
			Ro      bool   `json:"ro"`
			Missing bool   `json:"missing"`
		}
		out := struct {
			Mount   string   `json:"mount"`
			Members []member `json:"members"`
		}{Mount: v.Mount, Members: []member{}}
		for _, m := range v.Members {
			_, e := os.Stat(m.Path)
			out.Members = append(out.Members, member{m.Name, m.Path, m.ReadOnly, e != nil})
		}
		return writeJSON(w, out)
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

// listCmd takes --json before or after the view.
func listCmd(args []string) {
	set := flag.NewFlagSet("list", flag.ExitOnError)
	asJSON := set.Bool("json", false, "print JSON")
	var positional []string
	for {
		set.Parse(args)
		if set.NArg() == 0 {
			break
		}
		positional = append(positional, set.Arg(0))
		args = set.Args()[1:]
	}
	var e error
	switch len(positional) {
	case 0:
		e = listViews(os.Stdout, *asJSON)
	case 1:
		e = listMembers(os.Stdout, positional[0], *asJSON)
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

// newView writes a view file with the given members and mount point, and returns where it went. An
// empty mount leaves the default.
func newView(arg string, members []core.Member, mount string) (string, error) {
	path, e := resolveView(arg)
	if e != nil {
		return "", e
	}
	file, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	var f viewFile
	if mount != "" {
		if f.Mount, e = memberPath(arg, file, mount); e != nil {
			return "", e
		}
	}
	for _, m := range members {
		p, e := memberPath(arg, file, m.Path)
		if e != nil {
			return "", e
		}
		entry := viewMemberFile{Path: p, Ro: m.ReadOnly}
		if m.Name != filepath.Base(m.Path) {
			entry.As = m.Name
		}
		f.Member = append(f.Member, entry)
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

// parseNewArgs reads <view> [path [--as name] [--ro]]... [--mount path], with flags anywhere. --as
// and --ro apply to the closest path before them.
func parseNewArgs(args []string) (view string, members []core.Member, mount string, err error) {
	var list memberFlag
	set := flag.NewFlagSet("new", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.Var(&memberAsFlag{members: &list}, "as", "")
	set.Var(&memberRoFlag{members: &list}, "ro", "")
	set.StringVar(&mount, "mount", "", "")
	for {
		if err = set.Parse(args); err != nil {
			return
		}
		if set.NArg() == 0 {
			break
		}
		if view == "" {
			view = set.Arg(0)
		} else if err = list.Set(set.Arg(0)); err != nil {
			return
		}
		args = set.Args()[1:]
	}
	if view == "" {
		err = errors.New("missing view")
	}
	return view, list, mount, err
}

func newCmd(args []string) {
	view, members, mount, e := parseNewArgs(args)
	if e != nil {
		fmt.Fprintf(os.Stderr, "vw: %v\n%s\n", e, usage)
		os.Exit(2)
	}
	path, e := newView(view, members, mount)
	if e != nil {
		fatal(e)
	}
	fmt.Println(path)
}

// addMember appends path to a view file as a member. It refuses a path that doesn't exist and a name
// another member already has, and says to remount if the view is mounted.
func addMember(arg, path, as string, ro bool, notice io.Writer) error {
	file, e := resolveView(arg)
	if e != nil {
		return e
	}
	f, e := readViewFile(file)
	if e != nil {
		return e
	}
	v, e := loadViewFile(file)
	if e != nil {
		return e
	}
	abs, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	if _, e := os.Stat(abs); e != nil {
		return e
	}
	name := as
	if name == "" {
		name = filepath.Base(abs)
	}
	for _, m := range v.Members {
		if m.Name == name {
			return fmt.Errorf("%s already has a member named %q", arg, name)
		}
	}
	absFile, e := filepath.Abs(file)
	if e != nil {
		return e
	}
	stored, e := memberPath(arg, absFile, abs)
	if e != nil {
		return e
	}
	f.Member = append(f.Member, viewMemberFile{Path: stored, As: as, Ro: ro})
	if e := saveViewFile(file, f); e != nil {
		return e
	}
	noticeIfMounted(v, arg, notice)
	return nil
}

// addCmd takes its flags before or after the positional arguments.
func addCmd(args []string) {
	set := flag.NewFlagSet("add", flag.ExitOnError)
	as := set.String("as", "", "name the member differently from its directory")
	ro := set.Bool("ro", false, "refuse writes to the member")
	var positional []string
	for {
		set.Parse(args)
		if set.NArg() == 0 {
			break
		}
		positional = append(positional, set.Arg(0))
		args = set.Args()[1:]
	}
	if len(positional) != 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if e := addMember(positional[0], positional[1], *as, *ro, os.Stderr); e != nil {
		fatal(e)
	}
}

// removeMember drops the member called name from a view file, and says to remount if the view is
// mounted.
func removeMember(arg, name string, notice io.Writer) error {
	file, e := resolveView(arg)
	if e != nil {
		return e
	}
	f, e := readViewFile(file)
	if e != nil {
		return e
	}
	v, e := loadViewFile(file)
	if e != nil {
		return e
	}
	i := slices.IndexFunc(v.Members, func(m core.Member) bool { return m.Name == name })
	if i < 0 {
		return fmt.Errorf("%s has no member named %q", arg, name)
	}
	f.Member = slices.Delete(f.Member, i, i+1)
	if e := saveViewFile(file, f); e != nil {
		return e
	}
	noticeIfMounted(v, arg, notice)
	return nil
}

func rmCmd(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if e := removeMember(args[0], args[1], os.Stderr); e != nil {
		fatal(e)
	}
}

package main

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/winfsp/cgofuse/fuse"

	"vw/core"
)

func parseMountFlags(args []string) (*mountFlags, error) {
	var mount mountFlags
	set := flag.NewFlagSet("mount", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	mount.register(set)
	return &mount, set.Parse(args)
}

func TestMountFlagsBindFiltersToTheClosestMember(t *testing.T) {
	mount, err := parseMountFlags([]string{
		"--exclude", "node_modules/", "--include", "/api/",
		"--member", "/a", "--member-exclude", "*.snap", "--member-include", "/src/",
		"--member", "/b:web:ro", "--member-exclude", "dist/",
		"--member-exclude", "*.log",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := memberFlag{
		{Name: "a", Path: "/a", Include: []string{"/src/"}, Exclude: []string{"*.snap"}},
		{Name: "web", Path: "/b", ReadOnly: true, Exclude: []string{"dist/", "*.log"}},
	}
	if !reflect.DeepEqual(want, mount.members) {
		t.Errorf("members = %+v, want %+v", mount.members, want)
	}
	if want := (listFlag{"node_modules/"}); !reflect.DeepEqual(want, mount.exclude) {
		t.Errorf("exclude = %v, want %v", mount.exclude, want)
	}
	if want := (listFlag{"/api/"}); !reflect.DeepEqual(want, mount.include) {
		t.Errorf("include = %v, want %v", mount.include, want)
	}
}

func TestMountFlagsRejectMemberFilterWithoutMember(t *testing.T) {
	for _, flagName := range []string{"--member-include", "--member-exclude"} {
		if _, err := parseMountFlags([]string{flagName, "x/", "--member", "/a"}); err == nil {
			t.Errorf("%s before any --member was accepted", flagName)
		}
	}
}

func TestMountFlagsRejectBadMemberSpec(t *testing.T) {
	if _, err := parseMountFlags([]string{"--member", "/a:b:rw"}); err == nil {
		t.Error("a member spec with an unknown option was accepted")
	}
}

func TestScratchDirDefaultsToMountpointName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := scratchDir("", "/x/surfaces")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".local/share/vw/views/surfaces/root"); want != got {
		t.Errorf("scratchDir = %q, want %q", got, want)
	}
}

func TestScratchDirFlagBecomesAbsolute(t *testing.T) {
	got, err := scratchDir("rel/scratch", "/x/surfaces")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) || filepath.Base(got) != "scratch" {
		t.Errorf("scratchDir = %q, want an absolute path ending in scratch", got)
	}
}

func listView(t *testing.T, fs *viewFS, path string) []string {
	t.Helper()
	var names []string
	errc := fs.Readdir(path, func(name string, _ *fuse.Stat_t, _ int64) bool {
		if name != "." && name != ".." {
			names = append(names, name)
		}
		return true
	}, 0, ^uint64(0))
	if errc != 0 {
		t.Fatalf("Readdir(%q) = %d", path, errc)
	}
	slices.Sort(names)
	return names
}

func TestReaddirHidesEntriesOnlyInListedDirectories(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"keep.txt", "node_modules/pkg/index.js"} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	view, err := core.New([]core.Member{{Name: "gw", Path: dir}}, core.WithExclude("node_modules/"))
	if err != nil {
		t.Fatal(err)
	}
	fs := &viewFS{view: view}
	tests := []struct {
		path string
		want []string
	}{
		{"/gw", []string{"keep.txt"}},
		{"/gw/node_modules", []string{"pkg"}},
		{"/gw/node_modules/pkg", []string{"index.js"}},
	}
	for _, tt := range tests {
		if got := listView(t, fs, tt.path); !slices.Equal(tt.want, got) {
			t.Errorf("Readdir(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestLoadMembersMarksFilesAndDirectories(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	members, err := loadMembers([]core.Member{{Name: "dir", Path: dir}, {Name: "notes.md", Path: file}})
	if err != nil {
		t.Fatal(err)
	}
	if members[0].File || !members[1].File {
		t.Errorf("File flags = %v, %v, want false, true", members[0].File, members[1].File)
	}
}

func TestLoadMembersRejectsMissingPath(t *testing.T) {
	if _, err := loadMembers([]core.Member{{Name: "x", Path: filepath.Join(t.TempDir(), "missing")}}); err == nil {
		t.Error("a missing path was accepted")
	}
}

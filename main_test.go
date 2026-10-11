package main

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
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
		"--member", "/b", "--as", "web", "--ro", "--member-exclude", "dist/",
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
	members, skipped := loadMembers([]core.Member{{Name: "dir", Path: dir}, {Name: "notes.md", Path: file}})
	if len(skipped) > 0 {
		t.Fatal(skipped)
	}
	if members[0].File || !members[1].File {
		t.Errorf("File flags = %v, %v, want false, true", members[0].File, members[1].File)
	}
}

func TestLoadMembersSkipsUnusablePaths(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	members, skipped := loadMembers([]core.Member{{Name: "missing", Path: missing}, {Name: "dir", Path: dir}})
	if len(members) != 1 || members[0].Name != "dir" {
		t.Errorf("members = %+v, want only dir", members)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0].Error(), `member "missing"`) {
		t.Errorf("skipped = %v, want one error naming the missing member", skipped)
	}
}

func TestIsMounted(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name, path string
		want       bool
	}{
		{"plain directory", dir, false},
		{"missing path", filepath.Join(dir, "missing"), false},
		{"devfs", "/dev", true},
	}
	for _, tt := range tests {
		got, err := isMounted(tt.path)
		if err != nil || got != tt.want {
			t.Errorf("%s: isMounted(%q) = %v, %v, want %v", tt.name, tt.path, got, err, tt.want)
		}
	}
}

func TestCheckMountPoint(t *testing.T) {
	full := t.TempDir()
	if err := os.WriteFile(filepath.Join(full, "keep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, path, want string
	}{
		{"empty directory", t.TempDir(), ""},
		{"directory with files", full, "not empty"},
		{"existing mount", "/dev", "already a mount point"},
	}
	for _, tt := range tests {
		err := checkMountPoint(tt.path)
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("%s: unexpected error %v", tt.name, err)
		case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
			t.Errorf("%s: error = %v, want one mentioning %q", tt.name, err, tt.want)
		}
	}
}

func TestViewMountSpecLoadsTheViewFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	mountpoint := filepath.Join(dir, "mnt")
	path := writeViewFile(t, dir, "team.view", "mount = \"mnt\"\nexclude = [\"dist/\"]\n[[member]]\npath = \"repo\"\n")
	spec, err := viewMountSpec(&mountFlags{}, []string{path, "-o", "noattrcache"})
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(mountpoint); err != nil || !fi.IsDir() {
		t.Errorf("mount point %s was not created: %v", mountpoint, err)
	}
	if spec.mountpoint != mountpoint || len(spec.members) != 1 || spec.members[0].Name != "repo" {
		t.Errorf("spec = %+v", spec)
	}
	if !reflect.DeepEqual(spec.exclude, []string{"dist/"}) || !reflect.DeepEqual(spec.fuseOpts, []string{"-o", "noattrcache"}) {
		t.Errorf("exclude = %v, fuseOpts = %v", spec.exclude, spec.fuseOpts)
	}
	if !strings.HasPrefix(spec.scratch, filepath.Join(home, ".local/share/vw/views/team-")) || filepath.Base(spec.scratch) != "root" {
		t.Errorf("scratch = %q", spec.scratch)
	}
}

func TestViewMountSpecKeepsScratchSeparatePerFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var scratch []string
	for range 2 {
		path := writeViewFile(t, t.TempDir(), "x.view", "")
		spec, err := viewMountSpec(&mountFlags{}, []string{path})
		if err != nil {
			t.Fatal(err)
		}
		scratch = append(scratch, spec.scratch)
	}
	if scratch[0] == scratch[1] {
		t.Errorf("two views named x share scratch %q", scratch[0])
	}
}

func TestViewMountSpecRejectsFlagFilters(t *testing.T) {
	path := writeViewFile(t, t.TempDir(), "x.view", "")
	for _, mount := range []mountFlags{{include: listFlag{"a"}}, {exclude: listFlag{"a"}}} {
		if _, err := viewMountSpec(&mount, []string{path}); err == nil {
			t.Errorf("filter flags %+v were accepted with a view file", mount)
		}
	}
}

func TestFlagMountSpecKeepsTheMountPointScratchDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	spec, err := flagMountSpec(&mountFlags{members: memberFlag{{Name: "a", Path: "/a"}}}, []string{"/x/surfaces", "-o", "ro"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".local/share/vw/views/surfaces/root"); spec.scratch != want || spec.mountpoint != "/x/surfaces" {
		t.Errorf("spec = %+v, want scratch %q", spec, want)
	}
}

func TestInitWritesOneByteToTheReadyDescriptor(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	(&viewFS{ready: w}).Init()
	got, err := io.ReadAll(r)
	if err != nil || len(got) != 1 {
		t.Errorf("read %v, %v, want one byte and then EOF", got, err)
	}
}

func TestInitWithoutAReadyDescriptor(t *testing.T) {
	(&viewFS{}).Init()
}

func TestMountFlagsReadTheReadyDescriptor(t *testing.T) {
	mount, err := parseMountFlags([]string{"--ready-fd", "5"})
	if err != nil || mount.readyFD != 5 {
		t.Errorf("readyFD = %d, %v, want 5", mount.readyFD, err)
	}
}

func TestReadyFileIgnoresStandardStreams(t *testing.T) {
	for _, fd := range []int{0, 1, 2, -1} {
		if readyFile(fd) != nil {
			t.Errorf("readyFile(%d) returned a file", fd)
		}
	}
}

func TestMountFlagsBindNameAndModeToTheClosestMember(t *testing.T) {
	mount, err := parseMountFlags([]string{
		"--member", "/a", "--as", "api", "--ro",
		"--member", "/b",
		"--member", "/c", "--ro=false",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := memberFlag{
		{Name: "api", Path: "/a", ReadOnly: true},
		{Name: "b", Path: "/b"},
		{Name: "c", Path: "/c"},
	}
	if !reflect.DeepEqual(want, mount.members) {
		t.Errorf("members = %+v, want %+v", mount.members, want)
	}
}

func TestMountFlagsRejectMemberOptionsWithoutMember(t *testing.T) {
	for _, args := range [][]string{{"--as", "x"}, {"--ro"}} {
		if _, err := parseMountFlags(append(args, "--member", "/a")); err == nil {
			t.Errorf("%v before any --member was accepted", args)
		}
	}
}

func newSidecarFS(t *testing.T) (fs *viewFS, real, side string) {
	t.Helper()
	real, side = t.TempDir(), t.TempDir()
	view, err := core.New([]core.Member{
		{Name: "gw", Path: real},
		{Name: "ro", Path: t.TempDir(), ReadOnly: true},
	}, core.WithExclude("node_modules/"), core.WithSidecars(side))
	if err != nil {
		t.Fatal(err)
	}
	return &viewFS{view: view}, real, side
}

func createSidecar(t *testing.T, fs *viewFS, path, content string) {
	t.Helper()
	errc, fh := fs.Create(path, syscall.O_CREAT|syscall.O_RDWR, 0o644)
	if errc != 0 {
		t.Fatalf("Create(%q) = %d", path, errc)
	}
	if n := fs.Write(path, []byte(content), 0, fh); n != len(content) {
		t.Fatalf("Write(%q) = %d", path, n)
	}
	if errc := fs.Release(path, fh); errc != 0 {
		t.Fatalf("Release(%q) = %d", path, errc)
	}
}

func TestSidecarsLandInTheSidecarDirectory(t *testing.T) {
	fs, real, side := newSidecarFS(t)
	createSidecar(t, fs, "/gw/._a.txt", "meta")
	if exists(filepath.Join(real, "._a.txt")) {
		t.Error("the sidecar reached the real folder")
	}
	if got, err := os.ReadFile(filepath.Join(side, "gw", "._a.txt")); err != nil || string(got) != "meta" {
		t.Errorf("sidecar file = %q, %v, want meta", got, err)
	}
	var stat fuse.Stat_t
	if errc := fs.Getattr("/gw/._a.txt", &stat, ^uint64(0)); errc != 0 || stat.Size != 4 {
		t.Errorf("Getattr = %d, size %d, want 0, 4", errc, stat.Size)
	}
	errc, fh := fs.Open("/gw/._a.txt", syscall.O_RDONLY)
	if errc != 0 {
		t.Fatalf("Open = %d", errc)
	}
	buf := make([]byte, 8)
	if n := fs.Read("/gw/._a.txt", buf, 0, fh); string(buf[:n]) != "meta" {
		t.Errorf("Read = %q, want meta", buf[:n])
	}
	fs.Release("/gw/._a.txt", fh)
}

func TestSidecarsCreateTheirDirectories(t *testing.T) {
	fs, real, side := newSidecarFS(t)
	createSidecar(t, fs, "/gw/sub/deeper/._a", "x")
	if !exists(filepath.Join(side, "gw", "sub", "deeper", "._a")) {
		t.Error("the sidecar was not created under its mirrored directory")
	}
	if exists(filepath.Join(real, "sub")) {
		t.Error("a sidecar write created a directory in the real folder")
	}
}

func TestReaddirHidesSidecarNames(t *testing.T) {
	fs, real, _ := newSidecarFS(t)
	for _, p := range []string{"keep.txt", "._old", "node_modules/._x", "node_modules/pkg/index.js"} {
		full := filepath.Join(real, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		path string
		want []string
	}{
		{"/gw", []string{"keep.txt"}},
		{"/gw/node_modules", []string{"pkg"}},
	}
	for _, tt := range tests {
		if got := listView(t, fs, tt.path); !slices.Equal(tt.want, got) {
			t.Errorf("Readdir(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestRenameAndUnlinkSidecars(t *testing.T) {
	fs, real, side := newSidecarFS(t)
	createSidecar(t, fs, "/gw/._a", "x")
	if errc := fs.Rename("/gw/._a", "/gw/._b"); errc != 0 {
		t.Fatalf("Rename = %d", errc)
	}
	if exists(filepath.Join(side, "gw", "._a")) || !exists(filepath.Join(side, "gw", "._b")) {
		t.Error("Rename did not move the sidecar")
	}
	if errc := fs.Unlink("/gw/._b"); errc != 0 {
		t.Fatalf("Unlink = %d", errc)
	}
	if exists(filepath.Join(side, "gw", "._b")) {
		t.Error("Unlink left the sidecar")
	}
	if entries, _ := os.ReadDir(real); len(entries) != 0 {
		t.Errorf("the real folder has %d entries, want 0", len(entries))
	}
}

func TestSidecarsHonourReadOnlyMembers(t *testing.T) {
	fs, _, _ := newSidecarFS(t)
	if errc, _ := fs.Create("/ro/._a", syscall.O_CREAT|syscall.O_RDWR, 0o644); errc != -int(syscall.EROFS) {
		t.Errorf("Create on a read-only member = %d, want EROFS", errc)
	}
}

func TestRenameDirectoryMovesItsSidecars(t *testing.T) {
	fs, real, side := newSidecarFS(t)
	if err := os.Mkdir(filepath.Join(real, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	createSidecar(t, fs, "/gw/d/._f", "x")
	if errc := fs.Rename("/gw/d", "/gw/e"); errc != 0 {
		t.Fatalf("Rename = %d", errc)
	}
	if !exists(filepath.Join(side, "gw", "e", "._f")) || exists(filepath.Join(side, "gw", "d")) {
		t.Error("the sidecars did not follow the renamed directory")
	}
}

func TestRenameFileLeavesSidecarTreesAlone(t *testing.T) {
	fs, real, side := newSidecarFS(t)
	createSidecar(t, fs, "/gw/d/._f", "x")
	if err := os.WriteFile(filepath.Join(real, "a.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if errc := fs.Rename("/gw/a.txt", "/gw/b.txt"); errc != 0 {
		t.Fatalf("Rename = %d", errc)
	}
	if !exists(filepath.Join(side, "gw", "d", "._f")) {
		t.Error("renaming a file disturbed another directory's sidecars")
	}
}

func TestRmdirDropsItsSidecars(t *testing.T) {
	fs, real, side := newSidecarFS(t)
	if err := os.Mkdir(filepath.Join(real, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	createSidecar(t, fs, "/gw/d/._f", "x")
	if errc := fs.Rmdir("/gw/d"); errc != 0 {
		t.Fatalf("Rmdir = %d", errc)
	}
	if exists(filepath.Join(side, "gw", "d")) {
		t.Error("Rmdir left the sidecar directory")
	}
}

func TestSidecarDirSitsNextToScratch(t *testing.T) {
	if got, want := sidecarDir("/x/views/a/root"), "/x/views/a/root.appledouble"; got != want {
		t.Errorf("sidecarDir = %q, want %q", got, want)
	}
}

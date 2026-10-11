package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"vw/core"
)

func TestListViewsShowsCentralViewNames(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	dir := filepath.Join(config, "vw", "views")
	if err := os.MkdirAll(filepath.Join(dir, "sub.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"surfaces.toml", "api.toml", "notes.txt"} {
		writeViewFile(t, dir, name, "")
	}
	var out bytes.Buffer
	if err := listViews(&out, false); err != nil {
		t.Fatal(err)
	}
	if want := "api\nsurfaces\n"; out.String() != want {
		t.Errorf("listViews = %q, want %q", out.String(), want)
	}
}

func TestListViewsWithoutAConfigDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out bytes.Buffer
	if err := listViews(&out, false); err != nil || out.Len() != 0 {
		t.Errorf("listViews = %q, %v, want no output and no error", out.String(), err)
	}
}

func TestListMembersMarksReadOnlyAndMissing(t *testing.T) {
	dir := t.TempDir()
	path := writeViewFile(t, dir, "x.view", "mount = \"/mnt/x\"\n"+
		"[[member]]\npath = \""+dir+"\"\nas = \"here\"\nro = true\n"+
		"[[member]]\npath = \""+filepath.Join(dir, "gone")+"\"\n")
	var out bytes.Buffer
	if err := listMembers(&out, path, false); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 || lines[0] != "mount /mnt/x" {
		t.Fatalf("output = %q", out.String())
	}
	if f := strings.Fields(lines[1]); len(f) != 3 || f[0] != "here" || f[2] != "ro" {
		t.Errorf("first member line = %q", lines[1])
	}
	if f := strings.Fields(lines[2]); len(f) != 3 || f[0] != "gone" || f[2] != "missing" {
		t.Errorf("second member line = %q", lines[2])
	}
}

func TestListViewsJSONGivesNamesAndFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	var out bytes.Buffer
	if err := listViews(&out, true); err != nil || strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("empty listViews = %q, %v, want []", out.String(), err)
	}
	views := filepath.Join(home, "vw", "views")
	if err := os.MkdirAll(views, 0o755); err != nil {
		t.Fatal(err)
	}
	writeViewFile(t, views, "b.toml", "")
	out.Reset()
	if err := listViews(&out, true); err != nil {
		t.Fatal(err)
	}
	var got []struct{ Name, File string }
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(views, "b.toml")
	if len(got) != 1 || got[0].Name != "b" || got[0].File != want {
		t.Errorf("listViews json = %+v, want b at %s", got, want)
	}
}

func TestListMembersJSONMarksReadOnlyAndMissing(t *testing.T) {
	dir := t.TempDir()
	path := writeViewFile(t, dir, "x.view", "mount = \"/mnt/x\"\n"+
		"[[member]]\npath = \""+dir+"\"\nas = \"here\"\nro = true\n"+
		"[[member]]\npath = \""+filepath.Join(dir, "gone")+"\"\n")
	var out bytes.Buffer
	if err := listMembers(&out, path, true); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Mount   string
		Members []struct {
			Name, Path  string
			Ro, Missing bool
		}
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Mount != "/mnt/x" || len(got.Members) != 2 {
		t.Fatalf("listMembers json = %+v", got)
	}
	if m := got.Members[0]; m.Name != "here" || m.Path != dir || !m.Ro || m.Missing {
		t.Errorf("first member = %+v", m)
	}
	if m := got.Members[1]; m.Name != "gone" || m.Ro || !m.Missing {
		t.Errorf("second member = %+v", m)
	}
}

func TestEditViewNotesAMountedView(t *testing.T) {
	dir := t.TempDir()
	mounted := writeViewFile(t, dir, "mounted.view", "mount = \"/dev\"\n")
	idle := writeViewFile(t, dir, "idle.view", "mount = \""+filepath.Join(dir, "idle")+"\"\n")
	tests := []struct {
		path string
		want string
	}{
		{mounted, mounted + " is mounted; remount to apply your changes\n"},
		{idle, ""},
	}
	for _, tt := range tests {
		var notice bytes.Buffer
		if err := editView(tt.path, []string{"true"}, &notice); err != nil {
			t.Fatal(err)
		}
		if notice.String() != tt.want {
			t.Errorf("editView(%q) notice = %q, want %q", tt.path, notice.String(), tt.want)
		}
	}
}

func TestEditViewRefusesAMissingViewAndAFailingEditor(t *testing.T) {
	dir := t.TempDir()
	if err := editView(filepath.Join(dir, "gone.view"), []string{"true"}, io.Discard); err == nil {
		t.Error("a missing view file was opened")
	}
	path := writeViewFile(t, dir, "x.view", "")
	if err := editView(path, []string{"false"}, io.Discard); err == nil {
		t.Error("a failing editor was reported as success")
	}
}

func pathMembers(paths ...string) []core.Member {
	var members []core.Member
	for _, p := range paths {
		members = append(members, core.Member{Name: filepath.Base(p), Path: p})
	}
	return members
}

func TestNewViewWritesACentralViewWithAbsolutePaths(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	repo := t.TempDir()
	path, err := newView("surfaces", pathMembers(repo, "relative/dir"), "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(config, "vw", "views", "surfaces.toml"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	v, err := loadViewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	if len(v.Members) != 2 || v.Members[0].Path != repo || v.Members[1].Path != filepath.Join(cwd, "relative/dir") {
		t.Errorf("members = %+v", v.Members)
	}
}

func TestNewViewWritesALocalViewWithRelativePaths(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repos", "api")
	path, err := newView(filepath.Join(dir, "team.view"), pathMembers(repo), "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "path = 'repos/api'") {
		t.Errorf("file = %q, want a path relative to the view file", data)
	}
	v, err := loadViewFile(path)
	if err != nil || v.Members[0].Path != repo {
		t.Errorf("members = %+v, %v, want %q", v.Members, err, repo)
	}
}

func TestNewViewRecordsNamesModesAndMount(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	repo, mount := t.TempDir(), t.TempDir()
	path, err := newView("surfaces", []core.Member{{Name: "backend", Path: repo, ReadOnly: true}}, mount)
	if err != nil {
		t.Fatal(err)
	}
	v, err := loadViewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if v.Mount != mount || len(v.Members) != 1 || v.Members[0].Name != "backend" || !v.Members[0].ReadOnly {
		t.Errorf("view = %+v", v)
	}
}

func TestNewViewRecordsALocalMountRelativeToTheFile(t *testing.T) {
	dir := t.TempDir()
	path, err := newView(filepath.Join(dir, "team.view"), nil, filepath.Join(dir, "out", "x"))
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "mount = 'out/x'") {
		t.Errorf("file = %q, want a mount relative to the view file", data)
	}
}

func TestNewViewOmitsTheDefaultNameAndMode(t *testing.T) {
	path, err := newView(filepath.Join(t.TempDir(), "x.view"), pathMembers("/a/api"), "")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "as =") || strings.Contains(string(data), "ro =") || strings.Contains(string(data), "mount =") {
		t.Errorf("file = %q, want only the path", data)
	}
}

func TestNewViewWithoutPathsWritesAnEmptyView(t *testing.T) {
	path, err := newView(filepath.Join(t.TempDir(), "empty.view"), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if v, err := loadViewFile(path); err != nil || len(v.Members) != 0 {
		t.Errorf("view = %+v, %v", v, err)
	}
}

func TestNewViewRefusesToOverwrite(t *testing.T) {
	path := writeViewFile(t, t.TempDir(), "x.view", "# mine\n")
	if _, err := newView(path, nil, ""); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v, want one saying the file already exists", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "# mine\n" {
		t.Errorf("existing file was changed to %q", data)
	}
}

func TestAddMemberRecordsAnAbsolutePathForACentralView(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	repo := t.TempDir()
	if _, err := newView("surfaces", nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := addMember("surfaces", repo, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	v, err := loadViewFile(filepath.Join(config, "vw", "views", "surfaces.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Members) != 1 || v.Members[0].Path != repo || v.Members[0].Name != filepath.Base(repo) {
		t.Errorf("members = %+v", v.Members)
	}
}

func TestAddMemberRecordsARelativePathForALocalView(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repos", "api")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	path := writeViewFile(t, dir, "team.view", "")
	if err := addMember(path, repo, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "path = 'repos/api'") {
		t.Errorf("view file = %q, want the path relative to the file", data)
	}
}

func TestAddMemberHonoursAsAndRo(t *testing.T) {
	dir := t.TempDir()
	path := writeViewFile(t, dir, "x.view", "")
	if err := addMember(path, dir, "backend", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	v, err := loadViewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Members) != 1 || v.Members[0].Name != "backend" || !v.Members[0].ReadOnly {
		t.Errorf("members = %+v", v.Members)
	}
}

func TestAddMemberKeepsExistingMembers(t *testing.T) {
	dir := t.TempDir()
	path := writeViewFile(t, dir, "x.view", "mount = \"~/views/x\"\n[[member]]\npath = \"~/code/api\"\nas = \"backend\"\nexclude = [\"*.snap\"]\n")
	if err := addMember(path, dir, "here", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := decodeViewFile(path, data)
	if err != nil {
		t.Fatal(err)
	}
	if f.Mount != "~/views/x" || len(f.Member) != 2 {
		t.Fatalf("view file = %+v", f)
	}
	if first := f.Member[0]; first.Path != "~/code/api" || first.As != "backend" || len(first.Exclude) != 1 {
		t.Errorf("first member = %+v", first)
	}
}

func TestAddMemberRefusesADuplicateNameAndAMissingPath(t *testing.T) {
	dir := t.TempDir()
	path := writeViewFile(t, dir, "x.view", "")
	if err := addMember(path, dir, "a", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := addMember(path, dir, "a", false, io.Discard); err == nil {
		t.Error("a duplicate member name was added")
	}
	if err := addMember(path, filepath.Join(dir, "gone"), "", false, io.Discard); err == nil {
		t.Error("a missing path was added")
	}
	if err := addMember(filepath.Join(dir, "gone.view"), dir, "", false, io.Discard); err == nil {
		t.Error("a member was added to a missing view")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("a refused add changed the view file:\n%s", after)
	}
}

func TestAddMemberNotesAMountedView(t *testing.T) {
	dir := t.TempDir()
	path := writeViewFile(t, dir, "mounted.view", "mount = \"/dev\"\n")
	var notice bytes.Buffer
	if err := addMember(path, dir, "", false, &notice); err != nil {
		t.Fatal(err)
	}
	if want := path + " is mounted; remount to apply your changes\n"; notice.String() != want {
		t.Errorf("notice = %q, want %q", notice.String(), want)
	}
}

func TestRemoveMemberDropsTheNamedMember(t *testing.T) {
	dir := t.TempDir()
	path := writeViewFile(t, dir, "x.view", "mount = \"~/views/x\"\n"+
		"[[member]]\npath = \""+dir+"\"\nas = \"alias\"\n"+
		"[[member]]\npath = \"~/code/api\"\nexclude = [\"*.snap\"]\n"+
		"[[member]]\npath = \""+dir+"\"\nas = \"other\"\n")
	if err := removeMember(path, "alias", io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := removeMember(path, "api", io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := decodeViewFile(path, data)
	if err != nil {
		t.Fatal(err)
	}
	if f.Mount != "~/views/x" || len(f.Member) != 1 || f.Member[0].As != "other" {
		t.Errorf("view file = %+v", f)
	}
}

func TestRemoveMemberRefusesAnUnknownMember(t *testing.T) {
	dir := t.TempDir()
	path := writeViewFile(t, dir, "x.view", "[[member]]\npath = \""+dir+"\"\nas = \"a\"\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := removeMember(path, "b", io.Discard); err == nil {
		t.Error("an unknown member was removed")
	}
	if err := removeMember(filepath.Join(dir, "gone.view"), "a", io.Discard); err == nil {
		t.Error("a member was removed from a missing view")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("a refused remove changed the view file:\n%s", after)
	}
}

func TestRemoveMemberNotesAMountedView(t *testing.T) {
	dir := t.TempDir()
	path := writeViewFile(t, dir, "mounted.view", "mount = \"/dev\"\n[[member]]\npath = \""+dir+"\"\nas = \"a\"\n")
	var notice bytes.Buffer
	if err := removeMember(path, "a", &notice); err != nil {
		t.Fatal(err)
	}
	if want := path + " is mounted; remount to apply your changes\n"; notice.String() != want {
		t.Errorf("notice = %q, want %q", notice.String(), want)
	}
}

func TestParseNewArgsBindsModifiersToTheClosestPath(t *testing.T) {
	view, members, mount, err := parseNewArgs([]string{"x", "/a", "--ro", "/b", "--as", "api", "--mount", "/m"})
	if err != nil {
		t.Fatal(err)
	}
	want := []core.Member{
		{Name: "a", Path: "/a", ReadOnly: true},
		{Name: "api", Path: "/b"},
	}
	if view != "x" || mount != "/m" || !reflect.DeepEqual(want, members) {
		t.Errorf("view = %q, members = %+v, mount = %q", view, members, mount)
	}
}

func TestParseNewArgsAcceptsFlagsBeforeTheView(t *testing.T) {
	view, members, mount, err := parseNewArgs([]string{"--mount", "/m", "x", "/a"})
	if err != nil {
		t.Fatal(err)
	}
	if view != "x" || mount != "/m" || !reflect.DeepEqual(pathMembers("/a"), members) {
		t.Errorf("view = %q, members = %+v, mount = %q", view, members, mount)
	}
}

func TestParseNewArgsRejectsBadInput(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"x", "--ro", "/a"},
		{"x", "--as", "n"},
		{"x", "/a", "--unknown"},
		{"x", ""},
	} {
		if _, _, _, err := parseNewArgs(args); err == nil {
			t.Errorf("parseNewArgs(%q) succeeded, want an error", args)
		}
	}
}

func TestNewFromParsedArgsWritesNamesAndModes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	repo := t.TempDir()
	view, members, mount, err := parseNewArgs([]string{"surfaces", repo, "--as", "backend", "--ro"})
	if err != nil {
		t.Fatal(err)
	}
	path, err := newView(view, members, mount)
	if err != nil {
		t.Fatal(err)
	}
	v, err := loadViewFile(path)
	if err != nil || len(v.Members) != 1 || v.Members[0].Name != "backend" || !v.Members[0].ReadOnly {
		t.Errorf("view = %+v, %v", v, err)
	}
}

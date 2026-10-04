package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err := listViews(&out); err != nil {
		t.Fatal(err)
	}
	if want := "api\nsurfaces\n"; out.String() != want {
		t.Errorf("listViews = %q, want %q", out.String(), want)
	}
}

func TestListViewsWithoutAConfigDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out bytes.Buffer
	if err := listViews(&out); err != nil || out.Len() != 0 {
		t.Errorf("listViews = %q, %v, want no output and no error", out.String(), err)
	}
}

func TestListMembersMarksReadOnlyAndMissing(t *testing.T) {
	dir := t.TempDir()
	path := writeViewFile(t, dir, "x.view", "mount = \"/mnt/x\"\n"+
		"[[member]]\npath = \""+dir+"\"\nas = \"here\"\nro = true\n"+
		"[[member]]\npath = \""+filepath.Join(dir, "gone")+"\"\n")
	var out bytes.Buffer
	if err := listMembers(&out, path); err != nil {
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

func TestNewViewWritesACentralViewWithAbsolutePaths(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	repo := t.TempDir()
	path, err := newView("surfaces", []string{repo, "relative/dir"})
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
	path, err := newView(filepath.Join(dir, "team.view"), []string{repo})
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

func TestNewViewWithoutPathsWritesAnEmptyView(t *testing.T) {
	path, err := newView(filepath.Join(t.TempDir(), "empty.view"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := loadViewFile(path); err != nil || len(v.Members) != 0 {
		t.Errorf("view = %+v, %v", v, err)
	}
}

func TestNewViewRefusesToOverwrite(t *testing.T) {
	path := writeViewFile(t, t.TempDir(), "x.view", "# mine\n")
	if _, err := newView(path, nil); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v, want one saying the file already exists", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "# mine\n" {
		t.Errorf("existing file was changed to %q", data)
	}
}

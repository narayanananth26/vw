package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"vw/core"
)

func writeViewFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadViewFile(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := writeViewFile(t, dir, "surfaces.view", `
mount = "~/views/s"
exclude = ["node_modules/", "dist/"]
include = ["/api/"]

[[member]]
path = "~/code/gl-api"
as = "api"
include = ["src/", "go.*"]
exclude = ["*.snap"]

[[member]]
path = "../docs"
ro = true

[[member]]
path = "/abs/notes.md"
`)
	got, err := loadViewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := &view{
		Name:    "surfaces",
		Mount:   filepath.Join(home, "views/s"),
		Include: []string{"/api/"},
		Exclude: []string{"node_modules/", "dist/"},
		Members: []core.Member{
			{Name: "api", Path: filepath.Join(home, "code/gl-api"), Include: []string{"src/", "go.*"}, Exclude: []string{"*.snap"}},
			{Name: "docs", Path: filepath.Join(filepath.Dir(dir), "docs"), ReadOnly: true},
			{Name: "notes.md", Path: "/abs/notes.md"},
		},
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("loadViewFile = %+v, want %+v", got, want)
	}
}

func TestLoadViewFileDefaultsMountPoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := writeViewFile(t, t.TempDir(), "team.toml", "")
	got, err := loadViewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "views", "team"); got.Mount != want {
		t.Errorf("Mount = %q, want %q", got.Mount, want)
	}
}

func TestLoadViewFileResolvesRelativePathsAgainstTheFile(t *testing.T) {
	dir := t.TempDir()
	path := writeViewFile(t, dir, "x.view", "[[member]]\npath = \"repos/a\"\n")
	got, err := loadViewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "repos/a"); got.Members[0].Path != want {
		t.Errorf("member path = %q, want %q", got.Members[0].Path, want)
	}
}

func TestLoadViewFileRejectsBadFiles(t *testing.T) {
	tests := []struct {
		name, content, want string
	}{
		{"unknown top-level key", "exlude = [\"a\"]\n", "exlude"},
		{"unknown member key", "[[member]]\npath = \"/a\"\nname = \"b\"\n", "name"},
		{"member without path", "[[member]]\nas = \"b\"\n", "no path"},
		{"invalid toml", "mount = \n", "x.view"},
	}
	for _, tt := range tests {
		path := writeViewFile(t, t.TempDir(), "x.view", tt.content)
		_, err := loadViewFile(path)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: error = %v, want one mentioning %q", tt.name, err, tt.want)
		}
	}
}

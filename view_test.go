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
		Path:    path,
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

func TestResolveView(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	central := filepath.Join(home, ".config", "vw", "views", "surfaces.toml")
	tests := []struct{ arg, want string }{
		{"surfaces", central},
		{"./team.view", "./team.view"},
		{"team.view", "team.view"},
		{"team.toml", "team.toml"},
		{"views/x", "views/x"},
		{"/abs/x", "/abs/x"},
	}
	for _, tt := range tests {
		got, err := resolveView(tt.arg)
		if err != nil || got != tt.want {
			t.Errorf("resolveView(%q) = %q, %v, want %q", tt.arg, got, err, tt.want)
		}
	}
}

func TestResolveViewHonoursXDGConfigHome(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	got, err := resolveView("surfaces")
	if want := filepath.Join(config, "vw", "views", "surfaces.toml"); err != nil || got != want {
		t.Errorf("resolveView = %q, %v, want %q", got, err, want)
	}
}

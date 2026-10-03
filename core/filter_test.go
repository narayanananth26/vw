package core

import (
	"os"
	"testing"
)

func TestVisibleExclude(t *testing.T) {
	view, err := New([]Member{
		{Name: "api", Path: "/home/api", Exclude: []string{"*.snap", "/build/", "!keep.snap"}},
		{Name: "web", Path: "/home/web"},
	}, WithExclude("node_modules/", "/web/dist"))
	if nil != err {
		t.Fatal(err)
	}
	tests := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"/", true, true},
		{"/api", true, true},
		{"/api/main.go", false, true},
		{"/api/node_modules", true, false},
		{"/api/node_modules", false, true},
		{"/api/node_modules/x/y.js", false, false},
		{"/api/pkg/node_modules/x", false, false},
		{"/web/dist", true, false},
		{"/web/dist/app.js", false, false},
		{"/api/dist", true, true},
		{"/api/a.snap", false, false},
		{"/api/keep.snap", false, true},
		{"/api/build", true, false},
		{"/api/build/out", false, false},
		{"/api/src/build", true, true},
		{"/web/a.snap", false, true},
		{"/api/../web/node_modules", true, false},
		{"/nope/x", false, true},
	}
	for _, tt := range tests {
		if got := view.Visible(tt.path, tt.isDir); tt.want != got {
			t.Errorf("Visible(%q, %v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
		}
	}
}

func TestVisibleKeepsGitWhateverTheFilters(t *testing.T) {
	view, err := New([]Member{
		{Name: "api", Path: "/home/api", Exclude: []string{".*"}},
	}, WithExclude(".git", "api/vendor/"))
	if nil != err {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/.git", "/api/.git/objects/ab", "/api/vendor/lib/.git"} {
		if !view.Visible(p, false) {
			t.Errorf("Visible(%q) = false, want true", p)
		}
	}
	if view.Visible("/api/.env", false) {
		t.Error("Visible(/api/.env) = true, want false")
	}
}

func TestVisibleWithoutFilters(t *testing.T) {
	view := newTestView(t)
	for _, p := range []string{"/", "/api", "/api/node_modules/x", "/docs/a.txt"} {
		if !view.Visible(p, false) {
			t.Errorf("Visible(%q) = false with no filters, want true", p)
		}
	}
}

func TestVisibleInclude(t *testing.T) {
	view, err := New([]Member{
		{Name: "api", Path: "/home/api", Include: []string{"/src/", "go.*"}},
		{Name: "web", Path: "/home/web", Include: []string{"/src/"}, Exclude: []string{"*.snap"}},
		{Name: "lib", Path: "/home/lib", Include: []string{"/pkg/x.go", "/gen/**/*.go"}},
		{Name: "cli", Path: "/home/cli"},
	})
	if nil != err {
		t.Fatal(err)
	}
	tests := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"/api", true, true},
		{"/api/src", true, true},
		{"/api/src/a/b.go", false, true},
		{"/api/go.mod", false, true},
		{"/api/sub/go.sum", false, true},
		{"/api/sub", true, true},
		{"/api/README.md", false, false},
		{"/web", true, true},
		{"/web/src/a/b.go", false, true},
		{"/web/src/a.snap", false, false},
		{"/web/docs", true, false},
		{"/web/README.md", false, false},
		{"/web/sub/src", true, false},
		{"/lib/pkg", true, true},
		{"/lib/pkg/x.go", false, true},
		{"/lib/pkg/y.go", false, false},
		{"/lib/other", true, false},
		{"/lib/gen", true, true},
		{"/lib/gen/a/b", true, true},
		{"/lib/gen/a/b.go", false, true},
		{"/lib/gen/a/b.txt", false, false},
		{"/web/.git/config", false, true},
		{"/cli/anything", false, true},
	}
	for _, tt := range tests {
		if got := view.Visible(tt.path, tt.isDir); tt.want != got {
			t.Errorf("Visible(%q, %v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
		}
	}
}

func TestVisibleViewInclude(t *testing.T) {
	view, err := New([]Member{
		{Name: "api", Path: "/home/api"},
		{Name: "web", Path: "/home/web"},
	}, WithInclude("/api/", "/web/src/"), WithExclude("*.snap"))
	if nil != err {
		t.Fatal(err)
	}
	tests := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"/", true, true},
		{"/api", true, true},
		{"/api/x/y.go", false, true},
		{"/api/x/y.snap", false, false},
		{"/web", true, true},
		{"/web/src/a.go", false, true},
		{"/web/docs", true, false},
		{"/web/a.go", false, false},
	}
	for _, tt := range tests {
		if got := view.Visible(tt.path, tt.isDir); tt.want != got {
			t.Errorf("Visible(%q, %v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
		}
	}
}

func TestVisibleGitignore(t *testing.T) {
	files := map[string]string{
		"/home/api/.gitignore":     "node_modules/\n*.log\n# comment\n\n!keep.log\r\n",
		"/home/api/pkg/.gitignore": "gen.go\n/local\n",
		"/home/web/.gitignore":     "*.log\n",
	}
	read := func(real string) ([]byte, error) {
		data, ok := files[real]
		if !ok {
			return nil, os.ErrNotExist
		}
		return []byte(data), nil
	}
	view, err := New([]Member{
		{Name: "api", Path: "/home/api", Gitignore: true},
		{Name: "web", Path: "/home/web"},
	}, WithGitignoreSource(read))
	if nil != err {
		t.Fatal(err)
	}
	tests := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"/api/src/main.go", false, true},
		{"/api/node_modules", true, false},
		{"/api/node_modules/x/y.js", false, false},
		{"/api/a.log", false, false},
		{"/api/keep.log", false, true},
		{"/api/pkg/gen.go", false, false},
		{"/api/pkg/x/gen.go", false, false},
		{"/api/gen.go", false, true},
		{"/api/pkg/local", false, false},
		{"/api/local", false, true},
		{"/api/pkg/sub/local", false, true},
		{"/api/.git/objects/ab", false, true},
		{"/web/a.log", false, true},
	}
	for _, tt := range tests {
		if got := view.Visible(tt.path, tt.isDir); tt.want != got {
			t.Errorf("Visible(%q, %v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
		}
	}
}

func TestVisibleGitignoreWithoutSource(t *testing.T) {
	view, err := New([]Member{{Name: "api", Path: "/home/api", Gitignore: true}})
	if nil != err {
		t.Fatal(err)
	}
	if !view.Visible("/api/a.log", false) {
		t.Error("Visible(/api/a.log) = false with no gitignore source, want true")
	}
}

package core

import "testing"

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

package core

import (
	"errors"
	"reflect"
	"slices"
	"syscall"
	"testing"
)

func TestParseMember(t *testing.T) {
	tests := []struct {
		spec string
		want Member
	}{
		{"/home/a", Member{Name: "a", Path: "/home/a"}},
		{"/home/a/", Member{Name: "a", Path: "/home/a/"}},
		{"/home/b:api", Member{Name: "api", Path: "/home/b"}},
		{"/home/b:api:ro", Member{Name: "api", Path: "/home/b", ReadOnly: true}},
		{"/home/b::ro", Member{Name: "b", Path: "/home/b", ReadOnly: true}},
	}
	for _, tt := range tests {
		got, err := ParseMember(tt.spec)
		if err != nil {
			t.Errorf("ParseMember(%q) error: %v", tt.spec, err)
			continue
		}
		if !reflect.DeepEqual(tt.want, got) {
			t.Errorf("ParseMember(%q) = %+v, want %+v", tt.spec, got, tt.want)
		}
	}
}

func TestParseMemberErrors(t *testing.T) {
	for _, spec := range []string{"", ":api", "/home/a:x:rw", "/home/a:x:ro:extra"} {
		if _, err := ParseMember(spec); err == nil {
			t.Errorf("ParseMember(%q) succeeded, want an error", spec)
		}
	}
}

func TestNew(t *testing.T) {
	view, err := New([]Member{
		{Name: "a", Path: "/home/a"},
		{Name: "api", Path: "/home/b", ReadOnly: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.members) != 2 {
		t.Errorf("got %d members, want 2", len(view.members))
	}
	if !view.members["api"].ReadOnly {
		t.Error("api lost its ReadOnly flag")
	}
}

func TestNewRejectsDuplicateNames(t *testing.T) {
	_, err := New([]Member{
		{Name: "api", Path: "/x/api"},
		{Name: "api", Path: "/y/api"},
	})
	if err == nil {
		t.Error("New accepted two members named api")
	}
}

func TestNewRejectsFiltersOnFileMember(t *testing.T) {
	for _, m := range []Member{
		{Name: "notes", Path: "/home/notes.md", File: true, Include: []string{"a"}},
		{Name: "notes", Path: "/home/notes.md", File: true, Exclude: []string{"a"}},
	} {
		if _, err := New([]Member{m}); err == nil {
			t.Errorf("New accepted filters on file member %+v", m)
		}
	}
}

func TestFileMember(t *testing.T) {
	view, err := New([]Member{
		{Name: "docs", Path: "/home/docs"},
		{Name: "notes.md", Path: "/home/notes.md", File: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := view.Resolve("/notes.md"); (Resolved{Kind: InMember, Real: "/home/notes.md", MemberRoot: true}) != got {
		t.Errorf("Resolve(/notes.md) = %+v", got)
	}
	if real, err := view.ResolveRemove("/notes.md"); real != "" || !errors.Is(err, syscall.EBUSY) {
		t.Errorf("ResolveRemove(/notes.md) = %q, %v, want EBUSY", real, err)
	}
	for _, pair := range [][2]string{{"/notes.md", "/docs/x"}, {"/docs/x", "/notes.md"}, {"/notes.md", "/other"}} {
		if _, _, err := view.ResolvePair(pair[0], pair[1]); !errors.Is(err, syscall.EBUSY) {
			t.Errorf("ResolvePair(%q, %q) error = %v, want EBUSY", pair[0], pair[1], err)
		}
	}
	if real, err := view.ResolveWrite("/notes.md"); real != "/home/notes.md" || err != nil {
		t.Errorf("ResolveWrite(/notes.md) = %q, %v", real, err)
	}
}

func TestNewRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b"} {
		if _, err := New([]Member{{Name: name, Path: "/home/a"}}); err == nil {
			t.Errorf("New accepted member name %q", name)
		}
	}
}

func TestNewRejectsDotPathWithoutName(t *testing.T) {
	m, err := ParseMember(".")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New([]Member{m}); err == nil {
		t.Error("New accepted a member named after the path .")
	}
}

func newTestView(t *testing.T) *View {
	t.Helper()
	view, err := New([]Member{
		{Name: "docs", Path: "/home/notes"},
		{Name: "api", Path: "/home/b", ReadOnly: true},
		{Name: "c", Path: "/home/c/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func TestResolve(t *testing.T) {
	view := newTestView(t)
	tests := []struct {
		path string
		want Resolved
	}{
		{"/", Resolved{Kind: Root}},
		{"", Resolved{Kind: Root}},
		{"/api", Resolved{Kind: InMember, Real: "/home/b", ReadOnly: true, MemberRoot: true}},
		{"/api/", Resolved{Kind: InMember, Real: "/home/b", ReadOnly: true, MemberRoot: true}},
		{"/api/src/main.go", Resolved{Kind: InMember, Real: "/home/b/src/main.go", ReadOnly: true}},
		{"/docs/a.txt", Resolved{Kind: InMember, Real: "/home/notes/a.txt"}},
		{"/c/x", Resolved{Kind: InMember, Real: "/home/c/x"}},
		{"/nope", Resolved{Kind: NotFound}},
		{"/nope/x", Resolved{Kind: NotFound}},
		{"/api/../docs/a.txt", Resolved{Kind: InMember, Real: "/home/notes/a.txt"}},
		{"/../api/x", Resolved{Kind: InMember, Real: "/home/b/x", ReadOnly: true}},
		{"/api/../../etc/passwd", Resolved{Kind: NotFound}},
	}
	for _, tt := range tests {
		if got := view.Resolve(tt.path); tt.want != got {
			t.Errorf("Resolve(%q) = %+v, want %+v", tt.path, got, tt.want)
		}
	}
}

func TestNames(t *testing.T) {
	want := []string{"api", "c", "docs"}
	if got := newTestView(t).Names(); !slices.Equal(want, got) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
}

func TestResolveWrite(t *testing.T) {
	view := newTestView(t)
	tests := []struct {
		path string
		real string
		err  error
	}{
		{"/docs/a.txt", "/home/notes/a.txt", nil},
		{"/docs", "/home/notes", nil},
		{"/api/a.txt", "", syscall.EROFS},
		{"/api", "", syscall.EROFS},
		{"/", "", syscall.EACCES},
		{"/newfile", "", syscall.EACCES},
		{"/nope/x", "", syscall.ENOENT},
	}
	for _, tt := range tests {
		real, err := view.ResolveWrite(tt.path)
		if tt.real != real || !errors.Is(err, tt.err) {
			t.Errorf("ResolveWrite(%q) = %q, %v, want %q, %v", tt.path, real, err, tt.real, tt.err)
		}
	}
}

func TestResolveRemove(t *testing.T) {
	view := newTestView(t)
	tests := []struct {
		path string
		real string
		err  error
	}{
		{"/docs/a.txt", "/home/notes/a.txt", nil},
		{"/docs", "", syscall.EBUSY},
		{"/docs/", "", syscall.EBUSY},
		{"/api/a.txt", "", syscall.EROFS},
		{"/api", "", syscall.EROFS},
		{"/", "", syscall.EACCES},
	}
	for _, tt := range tests {
		real, err := view.ResolveRemove(tt.path)
		if tt.real != real || !errors.Is(err, tt.err) {
			t.Errorf("ResolveRemove(%q) = %q, %v, want %q, %v", tt.path, real, err, tt.real, tt.err)
		}
	}
}

func TestResolvePair(t *testing.T) {
	view := newTestView(t)
	tests := []struct {
		old, new         string
		oldReal, newReal string
		err              error
	}{
		{"/docs/a", "/docs/b", "/home/notes/a", "/home/notes/b", nil},
		{"/docs/a", "/c/a", "", "", syscall.EXDEV},
		{"/docs/a", "/api/a", "", "", syscall.EXDEV},
		{"/api/a", "/docs/a", "", "", syscall.EXDEV},
		{"/api/a", "/api/b", "", "", syscall.EROFS},
		{"/docs", "/docs/x", "", "", syscall.EBUSY},
		{"/docs/a", "/docs", "", "", syscall.EBUSY},
		{"/docs", "/c/x", "", "", syscall.EBUSY},
		{"/docs/a", "/newtop", "", "", syscall.EACCES},
		{"/nope/x", "/docs/a", "", "", syscall.ENOENT},
	}
	for _, tt := range tests {
		oldReal, newReal, err := view.ResolvePair(tt.old, tt.new)
		if tt.oldReal != oldReal || tt.newReal != newReal || !errors.Is(err, tt.err) {
			t.Errorf("ResolvePair(%q, %q) = %q, %q, %v, want %q, %q, %v",
				tt.old, tt.new, oldReal, newReal, err, tt.oldReal, tt.newReal, tt.err)
		}
	}
}

func newScratchView(t *testing.T) *View {
	t.Helper()
	view, err := New([]Member{
		{Name: "docs", Path: "/home/notes"},
		{Name: "api", Path: "/home/b", ReadOnly: true},
	}, WithScratch("/scratch"))
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func TestResolveScratch(t *testing.T) {
	view := newScratchView(t)
	tests := []struct {
		path string
		want Resolved
	}{
		{"/", Resolved{Kind: Root}},
		{"/AGENTS.md", Resolved{Kind: Scratch, Real: "/scratch/AGENTS.md"}},
		{"/notes/todo.md", Resolved{Kind: Scratch, Real: "/scratch/notes/todo.md"}},
		{"/../AGENTS.md", Resolved{Kind: Scratch, Real: "/scratch/AGENTS.md"}},
		{"/docs/a.txt", Resolved{Kind: InMember, Real: "/home/notes/a.txt"}},
		{"/api", Resolved{Kind: InMember, Real: "/home/b", ReadOnly: true, MemberRoot: true}},
	}
	for _, tt := range tests {
		if got := view.Resolve(tt.path); tt.want != got {
			t.Errorf("Resolve(%q) = %+v, want %+v", tt.path, got, tt.want)
		}
	}
}

func TestResolveWriteScratch(t *testing.T) {
	view := newScratchView(t)
	tests := []struct {
		path string
		real string
		err  error
	}{
		{"/AGENTS.md", "/scratch/AGENTS.md", nil},
		{"/sub/x", "/scratch/sub/x", nil},
		{"/docs/a.txt", "/home/notes/a.txt", nil},
		{"/api/a.txt", "", syscall.EROFS},
		{"/", "", syscall.EACCES},
	}
	for _, tt := range tests {
		real, err := view.ResolveWrite(tt.path)
		if tt.real != real || !errors.Is(err, tt.err) {
			t.Errorf("ResolveWrite(%q) = %q, %v, want %q, %v", tt.path, real, err, tt.real, tt.err)
		}
	}
	if real, err := view.ResolveRemove("/AGENTS.md"); real != "/scratch/AGENTS.md" || err != nil {
		t.Errorf("ResolveRemove(/AGENTS.md) = %q, %v, want /scratch/AGENTS.md, nil", real, err)
	}
}

func TestResolvePairScratch(t *testing.T) {
	view := newScratchView(t)
	tests := []struct {
		old, new         string
		oldReal, newReal string
		err              error
	}{
		{"/a", "/b", "/scratch/a", "/scratch/b", nil},
		{"/a", "/sub/b", "/scratch/a", "/scratch/sub/b", nil},
		{"/a", "/docs/a", "", "", syscall.EXDEV},
		{"/docs/a", "/a", "", "", syscall.EXDEV},
		{"/docs", "/x", "", "", syscall.EBUSY},
		{"/a", "/docs", "", "", syscall.EBUSY},
	}
	for _, tt := range tests {
		oldReal, newReal, err := view.ResolvePair(tt.old, tt.new)
		if tt.oldReal != oldReal || tt.newReal != newReal || !errors.Is(err, tt.err) {
			t.Errorf("ResolvePair(%q, %q) = %q, %q, %v, want %q, %q, %v",
				tt.old, tt.new, oldReal, newReal, err, tt.oldReal, tt.newReal, tt.err)
		}
	}
}

func TestRootEntries(t *testing.T) {
	want := []string{"AGENTS.md", "CLAUDE.md", "api", "c", "docs"}
	got := newTestView(t).RootEntries([]string{"CLAUDE.md", "docs", "AGENTS.md"})
	if !slices.Equal(want, got) {
		t.Errorf("RootEntries() = %v, want %v", got, want)
	}
}

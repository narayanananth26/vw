package core

import (
	"slices"
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
		if nil != err {
			t.Errorf("ParseMember(%q) error: %v", tt.spec, err)
			continue
		}
		if tt.want != got {
			t.Errorf("ParseMember(%q) = %+v, want %+v", tt.spec, got, tt.want)
		}
	}
}

func TestParseMemberErrors(t *testing.T) {
	for _, spec := range []string{"", ":api", "/home/a:x:rw", "/home/a:x:ro:extra"} {
		if _, err := ParseMember(spec); nil == err {
			t.Errorf("ParseMember(%q) succeeded, want an error", spec)
		}
	}
}

func TestNew(t *testing.T) {
	view, err := New([]Member{
		{Name: "a", Path: "/home/a"},
		{Name: "api", Path: "/home/b", ReadOnly: true},
	})
	if nil != err {
		t.Fatal(err)
	}
	if 2 != len(view.members) {
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
	if nil == err {
		t.Error("New accepted two members named api")
	}
}

func TestNewRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b"} {
		if _, err := New([]Member{{Name: name, Path: "/home/a"}}); nil == err {
			t.Errorf("New accepted member name %q", name)
		}
	}
}

func TestNewRejectsDotPathWithoutName(t *testing.T) {
	m, err := ParseMember(".")
	if nil != err {
		t.Fatal(err)
	}
	if _, err := New([]Member{m}); nil == err {
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
	if nil != err {
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

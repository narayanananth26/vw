// Package core decides what each path in a view means. It never touches FUSE or the disk.
package core

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

type Member struct {
	Name     string
	Path     string
	File     bool
	ReadOnly bool
	Include  []string
	Exclude  []string
}

type Kind int

const (
	NotFound Kind = iota
	Root
	InMember
	Scratch
)

type Resolved struct {
	Kind       Kind
	Real       string // real path, set when Kind is InMember or Scratch
	ReadOnly   bool
	MemberRoot bool
}

type View struct {
	members       map[string]Member
	include       includeSet
	exclude       gitignore.Matcher
	memberInclude map[string]includeSet
	memberExclude map[string]gitignore.Matcher
	scratch       string
	sidecars      string
}

type Option func(*View)

// WithScratch resolves root-level names that are not members to paths under dir.
func WithScratch(dir string) Option {
	return func(view *View) {
		view.scratch = dir
	}
}

// WithSidecars resolves member paths named ._* to paths under dir, one subdirectory per member.
func WithSidecars(dir string) Option {
	return func(view *View) {
		view.sidecars = dir
	}
}

// WithInclude takes gitignore-style patterns matched against the whole view path, member name first.
func WithInclude(patterns ...string) Option {
	return func(view *View) {
		view.include = newIncludeSet(patterns)
	}
}

// WithExclude takes gitignore-style patterns matched against the whole view path, member name first.
func WithExclude(patterns ...string) Option {
	return func(view *View) {
		view.exclude = compile(patterns)
	}
}

// New builds a view, rejecting member names that are unusable or used twice.
func New(members []Member, opts ...Option) (*View, error) {
	view := &View{
		members:       make(map[string]Member, len(members)),
		memberInclude: make(map[string]includeSet, len(members)),
		memberExclude: make(map[string]gitignore.Matcher, len(members)),
	}
	for _, opt := range opts {
		opt(view)
	}
	for _, m := range members {
		if !validName(m.Name) {
			return nil, fmt.Errorf("invalid member name %q for %s, name it with path:name", m.Name, m.Path)
		}
		if _, dup := view.members[m.Name]; dup {
			return nil, fmt.Errorf("duplicate member name %q, name one with path:name", m.Name)
		}
		if m.File && (len(m.Include) > 0 || len(m.Exclude) > 0) {
			return nil, fmt.Errorf("file member %q cannot have include or exclude patterns", m.Name)
		}
		view.members[m.Name] = m
		view.memberInclude[m.Name] = newIncludeSet(m.Include)
		view.memberExclude[m.Name] = compile(m.Exclude)
	}
	return view, nil
}

func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.Contains(name, "/")
}

func split(viewPath string) (name, rest string) {
	clean := path.Clean("/" + viewPath)
	name, rest, _ = strings.Cut(clean[1:], "/")
	return name, rest
}

// Resolve cleans viewPath first, so .. cannot climb out of a member.
func (self *View) Resolve(viewPath string) Resolved {
	name, rest := split(viewPath)
	if name == "" {
		return Resolved{Kind: Root}
	}
	m, ok := self.members[name]
	if !ok {
		if self.scratch == "" {
			return Resolved{Kind: NotFound}
		}
		return Resolved{Kind: Scratch, Real: filepath.Join(self.scratch, name, rest)}
	}
	real := filepath.Join(m.Path, rest)
	if self.IsSidecar(viewPath) {
		real = filepath.Join(self.sidecars, name, rest)
	}
	return Resolved{
		Kind:       InMember,
		Real:       real,
		ReadOnly:   m.ReadOnly,
		MemberRoot: rest == "",
	}
}

// IsSidecar reports whether viewPath names a ._* entry inside a member while sidecars are on. Resolve
// maps such a path under the sidecar directory.
func (self *View) IsSidecar(viewPath string) bool {
	name, rest := split(viewPath)
	_, isMember := self.members[name]
	return isMember && self.sidecars != "" && rest != "" && strings.HasPrefix(path.Base(rest), "._")
}

// SidecarTree is the sidecar directory mirroring the member directory at viewPath. ok is false when
// sidecars are off or viewPath is a member root, outside a member, or a sidecar.
func (self *View) SidecarTree(viewPath string) (string, bool) {
	name, rest := split(viewPath)
	_, isMember := self.members[name]
	if !isMember || self.sidecars == "" || rest == "" || self.IsSidecar(viewPath) {
		return "", false
	}
	return filepath.Join(self.sidecars, name, rest), true
}

// RootEntries merges scratchNames into the member names, sorted. A member hides a scratch entry
// of the same name.
func (self *View) RootEntries(scratchNames []string) []string {
	entries := self.Names()
	for _, name := range scratchNames {
		if _, isMember := self.members[name]; !isMember {
			entries = append(entries, name)
		}
	}
	slices.Sort(entries)
	return entries
}

// Names returns the member names sorted.
func (self *View) Names() []string {
	names := make([]string, 0, len(self.members))
	for name := range self.members {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func writeErr(viewPath string, r Resolved) error {
	switch r.Kind {
	case Root:
		return syscall.EACCES
	case NotFound:
		if _, rest := split(viewPath); rest == "" {
			return syscall.EACCES
		}
		return syscall.ENOENT
	}
	if r.ReadOnly {
		return syscall.EROFS
	}
	return nil
}

// ResolveWrite returns EROFS for a read-only member, EACCES at the view root and ENOENT under an
// unknown member.
func (self *View) ResolveWrite(viewPath string) (string, error) {
	r := self.Resolve(viewPath)
	if err := writeErr(viewPath, r); err != nil {
		return "", err
	}
	return r.Real, nil
}

// ResolveRemove fails like ResolveWrite, and with EBUSY for a member's own directory.
func (self *View) ResolveRemove(viewPath string) (string, error) {
	r := self.Resolve(viewPath)
	if err := writeErr(viewPath, r); err != nil {
		return "", err
	}
	if r.MemberRoot {
		return "", syscall.EBUSY
	}
	return r.Real, nil
}

// ResolvePair returns EXDEV between members, or between a member and the scratch directory, so mv
// falls back to a copy. Otherwise it fails like ResolveRemove.
func (self *View) ResolvePair(oldPath, newPath string) (string, string, error) {
	oldR, newR := self.Resolve(oldPath), self.Resolve(newPath)
	if oldR.MemberRoot || newR.MemberRoot {
		return "", "", syscall.EBUSY
	}
	if oldR.Real != "" && newR.Real != "" && volume(oldPath, oldR) != volume(newPath, newR) {
		return "", "", syscall.EXDEV
	}
	if err := writeErr(oldPath, oldR); err != nil {
		return "", "", err
	}
	if err := writeErr(newPath, newR); err != nil {
		return "", "", err
	}
	return oldR.Real, newR.Real, nil
}

func volume(viewPath string, r Resolved) string {
	if r.Kind == Scratch {
		return ""
	}
	name, _ := split(viewPath)
	return name
}

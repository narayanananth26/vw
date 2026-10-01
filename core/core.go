// Package core decides what each path in a view means. It never touches FUSE or the disk.
package core

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// Member is a real directory shown in the view under Name.
type Member struct {
	Name     string
	Path     string
	ReadOnly bool
}

// Kind says what a view path points at.
type Kind int

const (
	NotFound Kind = iota
	Root
	InMember
)

// Resolved is the answer to what a view path means.
type Resolved struct {
	Kind       Kind
	Real       string // real path, set when Kind is InMember
	ReadOnly   bool   // the member holding the path is read-only
	MemberRoot bool   // the path is the member's own directory
}

// View is the set of members behind one mount.
type View struct {
	members map[string]Member
}

// ParseMember reads path[:name[:ro]]. An empty name defaults to the last element of path.
func ParseMember(spec string) (Member, error) {
	fields := strings.Split(spec, ":")
	if len(fields) > 3 {
		return Member{}, fmt.Errorf("member %q has too many fields, want path[:name[:ro]]", spec)
	}
	m := Member{Path: fields[0]}
	if "" == m.Path {
		return Member{}, fmt.Errorf("member %q has an empty path", spec)
	}
	if len(fields) > 1 {
		m.Name = fields[1]
	}
	if "" == m.Name {
		m.Name = filepath.Base(m.Path)
	}
	if len(fields) > 2 {
		if "ro" != fields[2] {
			return Member{}, fmt.Errorf("member %q has unknown option %q, want ro", spec, fields[2])
		}
		m.ReadOnly = true
	}
	return m, nil
}

// New builds a view, rejecting member names that are unusable or used twice.
func New(members []Member) (*View, error) {
	view := &View{members: make(map[string]Member, len(members))}
	for _, m := range members {
		if !validName(m.Name) {
			return nil, fmt.Errorf("invalid member name %q for %s, name it with path:name", m.Name, m.Path)
		}
		if _, dup := view.members[m.Name]; dup {
			return nil, fmt.Errorf("duplicate member name %q, name one with path:name", m.Name)
		}
		view.members[m.Name] = m
	}
	return view, nil
}

func validName(name string) bool {
	return "" != name && "." != name && ".." != name && !strings.Contains(name, "/")
}

// split cleans a view path and cuts it into the member name and the rest.
func split(viewPath string) (name, rest string) {
	clean := path.Clean("/" + viewPath)
	name, rest, _ = strings.Cut(clean[1:], "/")
	return name, rest
}

// Resolve maps a view path to the real path it stands for.
func (self *View) Resolve(viewPath string) Resolved {
	name, rest := split(viewPath)
	if "" == name {
		return Resolved{Kind: Root}
	}
	m, ok := self.members[name]
	if !ok {
		return Resolved{Kind: NotFound}
	}
	return Resolved{
		Kind:       InMember,
		Real:       filepath.Join(m.Path, rest),
		ReadOnly:   m.ReadOnly,
		MemberRoot: "" == rest,
	}
}

// Names lists the member names in order, which is what the view root contains.
func (self *View) Names() []string {
	names := make([]string, 0, len(self.members))
	for name := range self.members {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// writeErr says why r cannot be written, or nil. The root takes no files until it has a scratch directory.
func writeErr(viewPath string, r Resolved) error {
	switch r.Kind {
	case Root:
		return syscall.EACCES
	case NotFound:
		if _, rest := split(viewPath); "" == rest {
			return syscall.EACCES
		}
		return syscall.ENOENT
	}
	if r.ReadOnly {
		return syscall.EROFS
	}
	return nil
}

// ResolveWrite resolves a path that is about to be created or changed.
func (self *View) ResolveWrite(viewPath string) (string, error) {
	r := self.Resolve(viewPath)
	if err := writeErr(viewPath, r); nil != err {
		return "", err
	}
	return r.Real, nil
}

// ResolveRemove resolves a path that is about to be deleted. A member's own directory is refused,
// so removing it can never delete the real folder.
func (self *View) ResolveRemove(viewPath string) (string, error) {
	r := self.Resolve(viewPath)
	if err := writeErr(viewPath, r); nil != err {
		return "", err
	}
	if r.MemberRoot {
		return "", syscall.EBUSY
	}
	return r.Real, nil
}

// ResolvePair resolves the two paths of a rename or link. Paths in different members are on
// different filesystems as far as tools are concerned, so they get EXDEV and mv falls back to a copy.
func (self *View) ResolvePair(oldPath, newPath string) (string, string, error) {
	oldR, newR := self.Resolve(oldPath), self.Resolve(newPath)
	oldName, _ := split(oldPath)
	newName, _ := split(newPath)
	if InMember == oldR.Kind && InMember == newR.Kind && oldName != newName {
		return "", "", syscall.EXDEV
	}
	if err := writeErr(oldPath, oldR); nil != err {
		return "", "", err
	}
	if err := writeErr(newPath, newR); nil != err {
		return "", "", err
	}
	if oldR.MemberRoot || newR.MemberRoot {
		return "", "", syscall.EBUSY
	}
	return oldR.Real, newR.Real, nil
}

// Package core decides what each path in a view means. It never touches FUSE or the disk.
package core

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Member is a real directory shown in the view under Name.
type Member struct {
	Name     string
	Path     string
	ReadOnly bool
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

package core

import (
	"path"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

func compile(patterns []string) gitignore.Matcher {
	if 0 == len(patterns) {
		return nil
	}
	parsed := make([]gitignore.Pattern, len(patterns))
	for i, p := range patterns {
		parsed[i] = gitignore.ParsePattern(p, nil)
	}
	return gitignore.NewMatcher(parsed)
}

// includeSet is the include patterns of a view or a member. With no patterns everything shows.
type includeSet struct {
	patterns []string
	matcher  gitignore.Matcher
}

func newIncludeSet(patterns []string) includeSet {
	return includeSet{patterns: patterns, matcher: compile(patterns)}
}

// shows reports whether parts is inside an included path, or is a directory an include could
// still match below. Without that second case an include of /src/pkg/x.go would hide /src.
func (self includeSet) shows(parts []string, isDir bool) bool {
	if 0 == len(self.patterns) || 0 == len(parts) {
		return true
	}
	for i := 1; i <= len(parts); i++ {
		if self.matcher.Match(parts[:i], i < len(parts) || isDir) {
			return true
		}
	}
	return isDir && self.matchesBelow(parts)
}

// matchesBelow reports whether some include pattern can match a path under the directory dir.
// A pattern without a slash matches at any depth. Otherwise it is anchored, so dir must agree
// with the pattern's leading elements.
func (self includeSet) matchesBelow(dir []string) bool {
	for _, p := range self.patterns {
		if strings.HasPrefix(p, "!") {
			continue
		}
		elems := strings.Split(strings.TrimPrefix(strings.TrimSuffix(p, "/"), "/"), "/")
		if 1 == len(elems) && !strings.HasPrefix(p, "/") || anchoredAbove(elems, dir) {
			return true
		}
	}
	return false
}

// anchoredAbove reports whether dir is a leading part of the anchored pattern elems.
func anchoredAbove(elems, dir []string) bool {
	for i, part := range dir {
		if i >= len(elems) {
			return false
		}
		if "**" == elems[i] {
			return true
		}
		if ok, _ := path.Match(elems[i], part); !ok {
			return false
		}
	}
	return true
}

// A file under an excluded directory stays hidden whatever the later patterns say, so every
// ancestor is tried as a directory first.
func excludedBy(m gitignore.Matcher, parts []string, isDir bool) bool {
	if nil == m {
		return false
	}
	for i := 1; i <= len(parts); i++ {
		if m.Match(parts[:i], i < len(parts) || isDir) {
			return true
		}
	}
	return false
}

// Visible reports whether a view path shows up once the filters apply. isDir says whether its
// last element is a directory. A path with a .git element, or outside every member, is always
// visible.
func (self *View) Visible(viewPath string, isDir bool) bool {
	clean := path.Clean("/" + viewPath)
	if "/" == clean {
		return true
	}
	parts := strings.Split(clean[1:], "/")
	if slices.Contains(parts, ".git") {
		return true
	}
	name, rest := parts[0], parts[1:]
	_, isMember := self.members[name]
	if !isMember {
		return true
	}
	if !self.include.shows(parts, isDir) || excludedBy(self.exclude, parts, isDir) {
		return false
	}
	return self.memberInclude[name].shows(rest, isDir) && !excludedBy(self.memberExclude[name], rest, isDir)
}

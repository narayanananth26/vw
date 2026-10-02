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

// Visible reports whether a view path shows up in the view. isDir says whether its last
// element is a directory. View-level patterns match against the whole view path, member name
// first, and member-level patterns against the path inside the member.
func (self *View) Visible(viewPath string, isDir bool) bool {
	clean := path.Clean("/" + viewPath)
	if "/" == clean {
		return true
	}
	parts := strings.Split(clean[1:], "/")
	if slices.Contains(parts, ".git") {
		return true
	}
	if excludedBy(self.exclude, parts, isDir) {
		return false
	}
	return !excludedBy(self.memberExclude[parts[0]], parts[1:], isDir)
}

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"vw/core"
)

type viewMemberFile struct {
	Path    string   `toml:"path"`
	As      string   `toml:"as"`
	Ro      bool     `toml:"ro"`
	Include []string `toml:"include"`
	Exclude []string `toml:"exclude"`
}

type viewFile struct {
	Mount   string           `toml:"mount"`
	Include []string         `toml:"include"`
	Exclude []string         `toml:"exclude"`
	Member  []viewMemberFile `toml:"member"`
}

// view is a view file with every path made absolute.
type view struct {
	Name    string
	Path    string
	Mount   string
	Include []string
	Exclude []string
	Members []core.Member
}

// expandHome replaces a leading ~ with the home directory.
func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(home, path[1:]), nil
}

// resolveAgainst makes path absolute, taking a relative one as relative to dir.
func resolveAgainst(dir, path string) (string, error) {
	path, e := expandHome(path)
	if e != nil {
		return "", e
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return filepath.Clean(path), nil
}

func decodeViewFile(path string, data []byte) (viewFile, error) {
	var v viewFile
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	e := dec.Decode(&v)
	if e == nil {
		return v, nil
	}
	if strict, ok := errors.AsType[*toml.StrictMissingError](e); ok {
		return v, fmt.Errorf("%s: %s", path, strict.String())
	}
	if decode, ok := errors.AsType[*toml.DecodeError](e); ok {
		return v, fmt.Errorf("%s: %s", path, decode.String())
	}
	return v, fmt.Errorf("%s: %w", path, e)
}

// loadViewFile reads a TOML view file. Relative paths resolve against the file's directory.
func loadViewFile(path string) (*view, error) {
	abs, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	data, e := os.ReadFile(abs)
	if e != nil {
		return nil, e
	}
	f, e := decodeViewFile(path, data)
	if e != nil {
		return nil, e
	}
	dir := filepath.Dir(abs)
	name := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	v := &view{Name: name, Path: abs, Include: f.Include, Exclude: f.Exclude}
	mount := f.Mount
	if mount == "" {
		mount = filepath.Join("~", "views", name)
	}
	if v.Mount, e = resolveAgainst(dir, mount); e != nil {
		return nil, e
	}
	for i, m := range f.Member {
		if m.Path == "" {
			return nil, fmt.Errorf("%s: member %d has no path", path, i+1)
		}
		real, e := resolveAgainst(dir, m.Path)
		if e != nil {
			return nil, e
		}
		memberName := m.As
		if memberName == "" {
			memberName = filepath.Base(real)
		}
		v.Members = append(v.Members, core.Member{Name: memberName, Path: real, ReadOnly: m.Ro, Include: m.Include, Exclude: m.Exclude})
	}
	return v, nil
}

// viewsDir is where central views live, ~/.config/vw/views unless XDG_CONFIG_HOME says otherwise.
func viewsDir() (string, error) {
	if config := os.Getenv("XDG_CONFIG_HOME"); config != "" {
		return filepath.Join(config, "vw", "views"), nil
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(home, ".config", "vw", "views"), nil
}

// resolveView turns a view argument into a view file path. An argument with a path separator or
// a .view or .toml suffix is a file, and anything else is the name of a central view.
func resolveView(arg string) (string, error) {
	if strings.ContainsRune(arg, filepath.Separator) || strings.HasSuffix(arg, ".view") || strings.HasSuffix(arg, ".toml") {
		return arg, nil
	}
	dir, e := viewsDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(dir, arg+".toml"), nil
}

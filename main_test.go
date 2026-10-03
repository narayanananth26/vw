package main

import (
	"flag"
	"io"
	"reflect"
	"testing"
)

func parseMountFlags(args []string) (*mountFlags, error) {
	var mount mountFlags
	set := flag.NewFlagSet("mount", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	mount.register(set)
	return &mount, set.Parse(args)
}

func TestMountFlagsBindFiltersToTheClosestMember(t *testing.T) {
	mount, err := parseMountFlags([]string{
		"--exclude", "node_modules/", "--include", "/api/",
		"--member", "/a", "--member-exclude", "*.snap", "--member-include", "/src/",
		"--member", "/b:web:ro", "--member-exclude", "dist/",
		"--member-exclude", "*.log",
	})
	if nil != err {
		t.Fatal(err)
	}
	want := memberFlag{
		{Name: "a", Path: "/a", Include: []string{"/src/"}, Exclude: []string{"*.snap"}},
		{Name: "web", Path: "/b", ReadOnly: true, Exclude: []string{"dist/", "*.log"}},
	}
	if !reflect.DeepEqual(want, mount.members) {
		t.Errorf("members = %+v, want %+v", mount.members, want)
	}
	if want := (listFlag{"node_modules/"}); !reflect.DeepEqual(want, mount.exclude) {
		t.Errorf("exclude = %v, want %v", mount.exclude, want)
	}
	if want := (listFlag{"/api/"}); !reflect.DeepEqual(want, mount.include) {
		t.Errorf("include = %v, want %v", mount.include, want)
	}
}

func TestMountFlagsRejectMemberFilterWithoutMember(t *testing.T) {
	for _, flagName := range []string{"--member-include", "--member-exclude"} {
		if _, err := parseMountFlags([]string{flagName, "x/", "--member", "/a"}); nil == err {
			t.Errorf("%s before any --member was accepted", flagName)
		}
	}
}

func TestMountFlagsRejectBadMemberSpec(t *testing.T) {
	if _, err := parseMountFlags([]string{"--member", "/a:b:rw"}); nil == err {
		t.Error("a member spec with an unknown option was accepted")
	}
}

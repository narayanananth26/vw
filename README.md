# vw

I'm building `vw` so I can work on a slice of several projects as if it were
one folder.

The idea is a _view_: a directory that contains only the files and folders I
pick, taken from anywhere on disk and left where they are. Open a terminal,
an editor or a coding agent inside the view, and `ls`, `rg`, `find`, vim and
git see only what I chose. Edits go straight to the originals.

A view will be a small TOML file (we'll see).

## Why a filesystem

- My work is usually spread across several projects on my disk
- Usually I only care about a slice of each
- I want that slice in one place, so my tools and agents see just that
- A folder of symlinks should do it, but on macOS `grep -r`, `rg` and `find`
  don't follow them.
- Organizing manually every time using `mkdir` and `cd` is a _pita_
- New filesystem!

It's written in Go on [cgofuse](https://github.com/winfsp/cgofuse) and
[FUSE-T](https://www.fuse-t.org/), so it runs in user space with no kernel
extension.

## Update
- [FUSE-T](https://www.fuse-t.org/) turned out to be very slow. Processes that do many i/o ops like tsc on a resonable large project, are very slow due to the loopback overhead on every call.
- I am checking out [FSKit](https://developer.apple.com/documentation/fskit), which is a more native solution. We'll still have the round trip between userspace and kernel but there won't be the extra overhead of an NFS mount. It also seems to have better support for fileattributes and cache control.

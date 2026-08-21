# zimfs

A read-only [FUSE](https://github.com/libfuse/libfuse) filesystem that mounts a
[ZIM](https://openzim.org/wiki/ZIM_file_format) archive — the offline format developed
by [Kiwix](https://kiwix.org/)

It exposes the archive's **content namespace** (`C`) as a tree of files, directories,
and symlinks, with no extraction and no up-front decompression of the whole
archive.

```text
$ zimfs wikipedia_fr_all_maxi.zim /mnt/wiki
$ ls /mnt/wiki
```

## Features

- **Read-only**: files are `0444`, directories `0555`, symlinks `0777`.
- **Zero-copy access**: the archive is `mmap`ed once and blobs are read directly
  from the mapping where possible (in case of uncompressed clusters).
- **All ZIM compression codecs**: `none`, `zlib`, `bzip2`, `lzma2`, and `zstd` are supported
- **Synthetic directories**: ZIM stores only sorted file paths. For example, we can have
  path `example.com/assets/index.html` but there is no corresponding `example.com/assets`
  directory. Thus, directories are synthesized by the filesystem.
- **Redirect entries become symlinks**: a ZIM redirect resolves to its target
  path via `readlink`.
- **Deprecated entries are hidden**: `deleted` (`0xfffd`) and `linktarget`
  (`0xfffe`) entries are kept for correct ordering but never shown.
- **Bounded memory**: decompressed clusters and inodes are held in bounded LRU
  caches
- **Hardened against corruption**: malformed/truncated data returns `EIO`
  instead of panicking while missing entries return `ENOENT`. While we do not check
  if the ZIM is a valid ZIM file, offsets are calculated based on the
  specificiation of the [ZIM file format](https://wiki.openzim.org/wiki/ZIM_file_format).
  As a consequence, you might be able to read partially correct ZIMs. To ensure your ZIM
  file is valid, run [zimcheck](https://github.com/openzim/zim-tools) against it.
- **Refuses to run as root**: the filesystem does no access checking of its
  own, so running as root would be a security hole.

## Building

Requires

- [Go 1.26+](https://go.dev/doc/install)
- libfuse2 (install with your package manager)

```sh
go build -o zimfs ./cmd
```

## Usage

```sh
zimfs <file.zim> <mountpoint>
```

Example:

```sh
mkdir -p /mnt/wiki
zimfs wikipedia_fr_all_maxi.zim /mnt/wiki
```

Unmount with:

```sh
fusermount -u /mnt/wiki
```

## Limitations

- Only the **`C` (content) namespace** is mounted at the root; other
  namespaces (`M`, `A`, `I`, `J`, …) are not yet exposed.
- **Whole-file `mmap`** is used. This is efficient for random access but requires
  enough virtual address space for the archive (fine on 64-bit).
- **No write support**: this is a viewer, not an editor.
- Listing a directory walks the archive's sorted entry span; a directory with a
  very large number of direct children is `O(children + descendants)` per page.

package fs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/elfkuzco/zimfs/internal/zim"
	"github.com/jacobsa/fuse"
	"github.com/jacobsa/fuse/fuseops"
	"github.com/jacobsa/fuse/fuseutil"
)

type ZimFS struct {
	fuseutil.NotImplementedFileSystem
	zf    *zim.ZimFile
	cache *InodeCache

	// The next inode ID to hand out. We assume that this will never overflow,
	// since even if we were handing out inode IDs at 4 GHz, it would still take
	// over a century to do so.
	nextInodeID atomic.Uint64
	// Uid and Gid of the user
	Uid uint32
	Gid uint32
}

func NewZimFS(data []byte) (fuse.Server, error) {
	zf, err := zim.NewZimFile(data)
	if err != nil {
		return nil, err
	}

	fs := &ZimFS{
		zf:    zf,
		cache: NewCache(),
		Uid:   uint32(os.Geteuid()),
		Gid:   uint32(os.Getgid()),
	}
	fs.nextInodeID.Store(uint64(fuseops.RootInodeID))

	// The root's first child is the first entry in the content namespace, which
	// is not necessarily index 0 if other namespaces sort before it.
	startIndex, err := zf.FirstIndexInNamespace('C')
	if err != nil {
		slog.Error("could not find any entries in C namesapce")
		return nil, err
	}

	rootEntry := zim.NewDirectoryEntry('C', "", startIndex)
	rootInode := newInode(fuseops.RootInodeID, rootEntry)
	rootInode.attributes = fs.createInodeAttributes(rootEntry)
	rootInode.materialized = true
	rootInode.parent = fuseops.RootInodeID
	fs.cache.addInode(rootInode)
	slog.Debug("added root entry to cache", "inodeId", rootInode.id)

	server := fuseutil.NewFileSystemServer(fs)
	return server, nil
}

func (fs *ZimFS) allocateInodeId() fuseops.InodeID {
	return fuseops.InodeID(fs.nextInodeID.Add(1))
}

func (fs *ZimFS) getOrCreateInode(child zim.ZimEntry, parent fuseops.InodeID, lookup bool) *inode {
	entry := child.Get()

	if inode, ok := fs.cache.getInodeByNsPath(entry.Namespace, entry.Path, lookup); ok {
		return inode
	}

	return fs.cache.getOrAddInode(entry.Namespace, entry.Path, child, parent, fs.allocateInodeId, lookup)
}

// materializeInode returns the inode's attributes, computing them lazily (and
// possibly decompressing cluster data) on first access.
func (fs *ZimFS) materializeInode(id fuseops.InodeID) (fuseops.InodeAttributes, bool) {
	attrs, matrerlized := fs.cache.materialize(id, func(inode *inode) fuseops.InodeAttributes {
		return fs.createInodeAttributes(inode.entry)
	})
	if !matrerlized {
		slog.Debug("could not materialize attributes for missing inode", "inodeId", id)
	}
	return attrs, matrerlized
}

// mapError translates zim-layer errors into the appropriate FUSE errno.
// Missing entries map to ENOENT; corrupt/truncated data and everything else map
// to EIO so a malformed archive never panics the mount process.
func (fs *ZimFS) mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, zim.EntryDoesNotExist) {
		return fuse.ENOENT
	}
	return fuse.EIO
}

// Get the mode of the ZIM entry
func (fs *ZimFS) getMode(entry *zim.Entry) os.FileMode {
	if entry.IsDirectoryEntry() {
		return 0555 | os.ModeDir
	} else if entry.IsRedirectEntry() {
		return 0777 | os.ModeSymlink
	} else {
		return 0444
	}
}

// create the inode attributes for a ZIM entry. May decompress cluster data to get
// additional information like size
func (fs *ZimFS) createInodeAttributes(zimEntry zim.ZimEntry) fuseops.InodeAttributes {
	now := time.Now()
	entry := zimEntry.Get()
	attrs := fuseops.InodeAttributes{
		Mode:   fs.getMode(entry),
		Atime:  now,
		Mtime:  now,
		Ctime:  now,
		Crtime: now,
		Uid:    fs.Uid,
		Gid:    fs.Gid,
	}
	switch entry := zimEntry.(type) {
	case *zim.DirectoryEntry:
		attrs.Nlink = 2
		attrs.Size = 4096
	case *zim.ContentEntry:
		attrs.Nlink = 1
		cluster, err := fs.zf.GetOrCreateCluster(entry)
		if err != nil {
			slog.Error("unable to get cluster for entry", "path", entry.Path, "error", err)
			return attrs
		}
		size, err := cluster.GetBlobSize(entry.BlobNumber)
		if err != nil {
			slog.Error("unable to retrieve size for entry from cluster", "path", entry.Path, "error", err)
		}
		attrs.Size = size
	case *zim.RedirectEntry:
		attrs.Nlink = 1
		target, err := fs.zf.ResolveRedirect(entry)
		if err != nil {
			slog.Error("unable to resolve redirect for entry", "path", entry.Path, "error", err)
			return attrs
		}
		attrs.Size = uint64(len(target.Get().Path))
	default:
		attrs.Nlink = 1
		attrs.Size = 4096
	}
	return attrs
}

// Find an entry using it's path name. ZIMs pathlist only contains filenames and not
// directory details. Thus, we can see entries like example.com/index.html even though
// there's no entry named example.com which would be the directory where the
// index.html file is located.
func (fs *ZimFS) LookUpInode(ctx context.Context, op *fuseops.LookUpInodeOp) error {
	// Grab the parent directory.
	parent, found := fs.cache.getInodeById(op.Parent)
	if !found {
		return fuse.ENOENT
	}
	parentEntry, ok := parent.entry.(*zim.DirectoryEntry)
	if !ok {
		slog.Error("parent entry is not a directory entry", "parent", op.Parent, "name", op.Name)
		return fuse.EINVAL
	}

	path := parent.buildPathName(op.Name)

	var child *inode
	if child, ok = fs.cache.getInodeByNsPath(parentEntry.Namespace, path, true); !ok {
		childEntry, err := fs.zf.GetZimEntryFromStart(parentEntry.Number, parentEntry.Namespace, path)
		if err != nil {
			return fs.mapError(err)
		}
		child = fs.getOrCreateInode(childEntry, op.Parent, true)
	}

	attrs, ok := fs.materializeInode(child.id)
	if !ok {
		return fuse.ENOENT
	}

	fs.setLookupEntry(op, child.id, attrs)
	return nil
}

func (fs *ZimFS) setLookupEntry(op *fuseops.LookUpInodeOp, childID fuseops.InodeID, attrs fuseops.InodeAttributes) {
	op.Entry.Child = childID
	op.Entry.Attributes = attrs

	// We don't spontaneously mutate, so the kernel can cache as long as it wants
	// (since it also handles invalidation).
	op.Entry.AttributesExpiration = time.Now().Add(365 * 24 * time.Hour)
	op.Entry.EntryExpiration = op.Entry.AttributesExpiration
}

// Decrement the reference count and evict the inode once it is unreferenced.
func (fs *ZimFS) ForgetInode(ctx context.Context, op *fuseops.ForgetInodeOp) error {
	fs.cache.forgetInode(op.Inode, op.N)
	return nil
}

// Return the attributes for an already-known inode, computing them lazily if the
// inode was only created during ReadDir.
func (fs *ZimFS) GetInodeAttributes(ctx context.Context, op *fuseops.GetInodeAttributesOp) error {
	attrs, ok := fs.materializeInode(op.Inode)
	if !ok {
		return fuse.ENOENT
	}

	op.Attributes = attrs
	op.AttributesExpiration = time.Now().Add(365 * 24 * time.Hour)
	return nil
}

// Open a directory
func (fs *ZimFS) OpenDir(ctx context.Context, op *fuseops.OpenDirOp) error {
	node, ok := fs.cache.getInodeById(op.Inode)
	if !ok {
		return fuse.ENOENT
	}

	if !node.entry.Get().IsDirectoryEntry() {
		slog.Error("inode is not a directory", "inode", op.Inode, "path", node.entry.Get().Path)
		return fuse.EINVAL
	}

	return nil
}

func (fs *ZimFS) makeDirEntry(id fuseops.InodeID, name string, dirType fuseutil.DirentType, offset uint32) fuseutil.Dirent {
	return fuseutil.Dirent{
		Name:   name,
		Type:   dirType,
		Inode:  id,
		Offset: fuseops.DirOffset(offset),
	}
}

// Read the entries of a directory previously opened with OpenDir
func (fs *ZimFS) ReadDir(ctx context.Context, op *fuseops.ReadDirOp) error {
	node, ok := fs.cache.getInodeById(op.Inode)
	if !ok {
		return fuse.ENOENT
	}

	if !node.entry.Get().IsDirectoryEntry() {
		slog.Error("inode is not a directory", "inode", op.Inode, "path", node.entry.Get().Path)
		return fuse.EINVAL
	}

	dirent := node.entry.(*zim.DirectoryEntry)

	// Directory offsets are synthetic: 0 starts at ".", 1 starts at "..", and
	// offsets >= 2 resume into the sorted entry span (index = offset - 2).
	cursor := uint32(op.Offset)

	// ZIM has no on-disk directories, so "." and ".." are synthesized here
	if cursor == 0 {
		entry := fs.makeDirEntry(node.id, ".", fuseutil.DT_Directory, 1)
		if n := fuseutil.WriteDirent(op.Dst[op.BytesRead:], entry); n == 0 {
			return nil
		} else {
			op.BytesRead += n
		}
		cursor = 1
	}
	if cursor == 1 {
		entry := fs.makeDirEntry(node.parent, "..", fuseutil.DT_Directory, 2)
		if n := fuseutil.WriteDirent(op.Dst[op.BytesRead:], entry); n == 0 {
			return nil
		} else {
			op.BytesRead += n
		}
		cursor = 2
	}

	startIndex := dirent.Number + (cursor - 2)
	slog.Debug("reading directory entries", "directory", dirent.Path, "offset", op.Offset)

	for {
		child, nextIndex, found, err := fs.zf.NextChild(dirent, startIndex)
		if err != nil {
			return fs.mapError(err)
		}
		if !found {
			slog.Debug("reached end of directory")
			break
		}

		childEntry := child.Get()
		var dirType fuseutil.DirentType
		if zim.IsDirectoryEntry(childEntry.MimeType) {
			dirType = fuseutil.DT_Directory
		} else if zim.IsRedirectEntry(childEntry.MimeType) {
			dirType = fuseutil.DT_Link
		} else {
			dirType = fuseutil.DT_File
		}

		name, _ := strings.CutPrefix(childEntry.Path, dirent.Path+"/")
		offset := (nextIndex - dirent.Number) + 2

		// create children inodes but don't materialize their attributes as
		// that could lead to decompressing cluster data. attributes will be
		// materialzed in GetInodeAttributes or LookupInode
		inode := fs.getOrCreateInode(child, node.id, false)
		entry := fs.makeDirEntry(inode.id, name, dirType, offset)

		n := fuseutil.WriteDirent(op.Dst[op.BytesRead:], entry)
		if n == 0 {
			break
		}
		op.BytesRead += n
		startIndex = nextIndex
	}
	return nil
}

// Open a file within the filesystem
func (fs *ZimFS) OpenFile(ctx context.Context, op *fuseops.OpenFileOp) error {
	if (op.OpenFlags & syscall.O_ACCMODE) != syscall.O_RDONLY {
		return syscall.EACCES
	}
	node, ok := fs.cache.getInodeById(op.Inode)
	if !ok {
		return fuse.ENOENT
	}
	if node.entry.Get().IsDirectoryEntry() {
		slog.Error("inode is not a directory", "inode", op.Inode, "path", node.entry.Get().Path)
		return syscall.EINVAL
	}
	return nil
}

// Read data from an open file
func (fs *ZimFS) ReadFile(ctx context.Context, op *fuseops.ReadFileOp) error {
	node, ok := fs.cache.getInodeById(op.Inode)
	if !ok {
		return fuse.ENOENT
	}

	var err error
	op.BytesRead, err = fs.zf.Read(node.entry, op.Offset, op.Dst)
	// Don't return EOF errors; we just indicate EOF to fuse using a short read.
	if err == io.EOF {
		return nil
	}
	return fs.mapError(err)
}

// Build the relative path to target from source. Assumes both are absolute path.
func (fs *ZimFS) buildRelativePath(source, target string) string {
	// Since we always get the full paths, the source tells how far we are away from the
	// root. Calculate the depth of the source directory and append the rest of target as
	// it is
	if source == "" {
		return target
	}
	sourceParts := strings.Split(source, "/")
	if len(sourceParts) == 1 {
		return target
	}
	depth := len(sourceParts) - 1
	var b strings.Builder
	for range depth {
		b.WriteString("..")
		b.WriteString("/")
	}
	return b.String() + target
}

// Read the target of a symlink inode
func (fs *ZimFS) ReadSymlink(ctx context.Context, op *fuseops.ReadSymlinkOp) error {
	node, ok := fs.cache.getInodeById(op.Inode)
	if !ok {
		return fuse.ENOENT
	}

	if !node.entry.Get().IsRedirectEntry() {
		slog.Error("inode is not a directory", "inode", op.Inode, "path", node.entry.Get().Path)
		return syscall.EINVAL
	}
	redirect := node.entry.(*zim.RedirectEntry)
	target, err := fs.zf.ResolveRedirect(redirect)
	if err != nil {
		return fs.mapError(err)
	}

	op.Target = fs.buildRelativePath(redirect.Path, target.Get().Path)

	return nil
}

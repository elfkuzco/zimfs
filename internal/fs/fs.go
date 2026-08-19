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
	zf     *zim.ZimFile
	logger *slog.Logger
	cache  *InodeCache

	// The next inode ID to hand out. We assume that this will never overflow,
	// since even if we were handing out inode IDs at 4 GHz, it would still take
	// over a century to do so.
	nextInodeID atomic.Uint64
}

func NewZimFS(f *os.File) (*fuse.Server, error) {
	zf, err := zim.NewZimFile(f)
	if err != nil {
		return nil, err
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	fs := &ZimFS{
		zf:     zf,
		logger: logger,
		cache:  NewCache(),
	}
	fs.nextInodeID.Store(uint64(fuseops.RootInodeID))

	// Set up the root inode.
	rootAttrs := fuseops.InodeAttributes{
		Mode: 0500 | os.ModeDir,
		Size: 4096,
	}

	rootEntry := zim.NewDirectoryEntry('C', "", 0)
	rootInode := newInode(fuseops.RootInodeID, rootAttrs, rootEntry)
	fs.cache.addInode(rootInode)

	server := fuseutil.NewFileSystemServer(fs)
	return &server, nil
}

func (fs *ZimFS) allocateInodeId() fuseops.InodeID {
	return fuseops.InodeID(fs.nextInodeID.Add(1))
}

func (fs *ZimFS) getOrCreateInode(child zim.ZimEntry) *inode {
	entry := child.Get()

	if id, ok := fs.cache.getInodeIdByNsPath(entry.Namespace, entry.Path); ok {
		if inode, ok := fs.cache.getInodeById(id); ok {
			return inode
		}
	}

	attrs := fs.createInodeAttributes(child)
	return fs.cache.getOrAddInode(entry.Namespace, entry.Path, attrs, child, fs.allocateInodeId)
}

func (fs *ZimFS) Destroy() {
	fs.zf.Close()
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
		return 0500 | os.ModeDir
	} else if entry.IsRedirectEntry() {
		return 0777 | os.ModeSymlink
	} else {
		return 0400
	}
}

func (fs *ZimFS) createInodeAttributes(zimEntry zim.ZimEntry) fuseops.InodeAttributes {
	now := time.Now()
	entry := zimEntry.Get()
	attrs := fuseops.InodeAttributes{
		Mode:   fs.getMode(entry),
		Atime:  now,
		Mtime:  now,
		Ctime:  now,
		Crtime: now,
	}
	switch entry := zimEntry.(type) {
	case *zim.DirectoryEntry:
		attrs.Nlink = 2
		attrs.Size = 4096
	case *zim.ContentEntry:
		attrs.Nlink = 1
		cluster, err := fs.zf.GetOrCreateCluster(entry)
		if err != nil {
			fs.logger.Error("unable to get cluster for entry", "path", entry.Path, "error", err)
			return attrs
		}
		size, err := cluster.GetBlobSize(entry.BlobNumber)
		if err != nil {
			fs.logger.Error("unable to retrieve size for entry from cluster", "path", entry.Path, "error", err)
		}
		attrs.Size = size
	case *zim.RedirectEntry:
		attrs.Nlink = 1
		target, err := fs.zf.ResolveRedirect(entry)
		if err != nil {
			fs.logger.Error("unable to resolve redirect for entry", "path", entry.Path, "error", err)
			return attrs
		}
		attrs.Size = uint64(len(target.Get().Path))
	default:
		attrs.Nlink = 1
		attrs.Size = 4096
	}
	return attrs
}

// Find an entry using it's path name. Paths are only searched in the C namespace
// of the ZIM file. ZIMs pathlist only contains filenames and not directory details.
// Thus, we can see entries like example.com/index.html even though there's no
// entry named example.com which would be the directory where the index.html file is
// located.
func (fs *ZimFS) LookUpInode(ctx context.Context, op *fuseops.LookUpInodeOp) error {
	// Grab the parent directory.
	parent, found := fs.cache.getInodeById(op.Parent)
	if !found {
		return fuse.ENOENT
	}
	parentEntry, ok := parent.entry.(*zim.DirectoryEntry)
	if !ok {
		return fuse.EINVAL
	}

	path := parent.buildPathName(op.Name)

	var child *inode
	if id, ok := fs.cache.getInodeIdByNsPath(parentEntry.Namespace, path); ok {
		child, _ = fs.cache.getInodeById(id)
	}

	if child == nil {
		childEntry, err := fs.zf.GetZimEntryFromStart(parentEntry.Number, parentEntry.Namespace, path)
		if err != nil {
			return fs.mapError(err)
		}
		child = fs.getOrCreateInode(childEntry)
	}

	fs.cache.incrementLookup(child.id)
	fs.setLookupEntry(op, child)
	return nil
}

func (fs *ZimFS) setLookupEntry(op *fuseops.LookUpInodeOp, child *inode) {
	op.Entry.Child = child.id
	op.Entry.Attributes = child.attributes

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

// Return the cached attributes for an already-known inode.
func (fs *ZimFS) GetInodeAttributes(ctx context.Context, op *fuseops.GetInodeAttributesOp) error {
	node, ok := fs.cache.getInodeById(op.Inode)
	if !ok {
		return fuse.ENOENT
	}

	op.Attributes = node.attributes
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

func (fs *ZimFS) makeDirEntries(parent *inode, children []zim.ZimEntry) []fuseutil.Dirent {
	parentEntry := parent.entry.(*zim.DirectoryEntry)
	dirents := make([]fuseutil.Dirent, len(children))
	for i, child := range children {
		childEntry := child.Get()
		var dirType fuseutil.DirentType
		if zim.IsDirectoryEntry(childEntry.MimeType) {
			dirType = fuseutil.DT_Directory
		} else if zim.IsRedirectEntry(childEntry.MimeType) {
			dirType = fuseutil.DT_Link
		} else {
			dirType = fuseutil.DT_File
		}

		name, _ := strings.CutPrefix(childEntry.Path, parentEntry.Path+"/")
		// The offset is the next entry index to read: childEntry.Number points at
		// this child's first entry, so add one so a subsequent readdir resumes
		// after it instead of re-reading it.
		offset := childEntry.Number - parentEntry.Number + 1

		inode := fs.getOrCreateInode(child)
		dirents[i] = fs.makeDirEntry(inode.id, name, dirType, offset)
	}
	return dirents
}

// Read the entries of a directory previously opened with OpenDir
func (fs *ZimFS) ReadDir(ctx context.Context, op *fuseops.ReadDirOp) error {
	node, ok := fs.cache.getInodeById(op.Inode)
	if !ok {
		return fuse.ENOENT
	}

	if !node.entry.Get().IsDirectoryEntry() {
		return fuse.EINVAL
	}

	dirent := node.entry.(*zim.DirectoryEntry)
	children := fs.zf.GetChildren(dirent, uint32(op.Offset))
	entries := fs.makeDirEntries(node, children)
	for _, entry := range entries {
		n := fuseutil.WriteDirent(op.Dst[op.BytesRead:], entry)
		if n == 0 {
			break
		}
		op.BytesRead += n
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

// Read the target of a symlink inode
func (fs *ZimFS) ReadSymlink(ctx context.Context, op *fuseops.ReadSymlinkOp) error {
	node, ok := fs.cache.getInodeById(op.Inode)
	if !ok {
		return fuse.ENOENT
	}

	if !node.entry.Get().IsRedirectEntry() {
		return syscall.EINVAL
	}
	redirect := node.entry.(*zim.RedirectEntry)
	target, err := fs.zf.ResolveRedirect(redirect)
	if err != nil {
		return fs.mapError(err)
	}

	op.Target = target.Get().Path

	return nil
}

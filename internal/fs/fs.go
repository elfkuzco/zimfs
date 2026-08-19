package fs

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/elfkuzco/zimfs/internal/zim"
	"github.com/jacobsa/fuse"
	"github.com/jacobsa/fuse/fuseops"
	"github.com/jacobsa/fuse/fuseutil"
)

type ZimFS struct {
	fuseutil.NotImplementedFileSystem
	mu     sync.Mutex
	zf     *zim.ZimFile
	logger *slog.Logger

	// GUARDED_BY(mu)
	cache *InodeCache

	// The next inode ID to hand out. We assume that this will never overflow,
	// since even if we were handing out inode IDs at 4 GHz, it would still take
	// over a century to do so.
	// GUARDED_BY(mu)
	nextInodeID fuseops.InodeID
}

func NewZimFS(f *os.File) (*fuse.Server, error) {
	var err error
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
	// Set up the root inode.
	rootAttrs := fuseops.InodeAttributes{
		Mode: 0500 | os.ModeDir,
		Size: 4096,
	}

	rootEntry := zim.NewDirectoryEntry('C', "", 0)
	rootInode := newInode(fuseops.RootInodeID, rootAttrs, rootEntry)
	fs.cache.addInode(rootInode)
	fs.nextInodeID = fuseops.RootInodeID + 1

	server := fuseutil.NewFileSystemServer(fs)
	return &server, nil
}

// LOCK_REQUIRED(fs.mu)
func (fs *ZimFS) allocateInodeId() (id fuseops.InodeID) {
	id = fs.nextInodeID
	fs.nextInodeID++
	return
}

// LOCK_REQUIRED(fs.mu)
// Create a new inode for the entry and store it in the cache
func (fs *ZimFS) allocateNode(child zim.ZimEntry) *inode {
	newId := fs.allocateInodeId()
	newInode := newInode(newId, fs.createInodeAttributes(child), child)
	fs.cache.addInode(newInode)
	return newInode
}

func (fs *ZimFS) Destroy() {
	fs.zf.Close()
}

// Get the mode of the ZIM entry
func (fs *ZimFS) getMode(entry *zim.Entry) os.FileMode {
	if entry.IsDirectoryEntry() {
		return 0500 | os.ModeDir
	} else {
		return 0400
	}
}

// Guarded_by(fs.mu)
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
	fs.mu.Lock()
	defer fs.mu.Unlock()

	var child *inode
	// Grab the parent directory.
	parent := fs.cache.getInodeByIdOrDie(op.Parent)
	parentEntry := parent.entry.(*zim.DirectoryEntry)
	// See if we have allocated an inode ID for this namespace+path combination
	path := parent.buildPathName(op.Name)
	childId, ok := fs.cache.getInodeIdByNsPath(parentEntry.Namespace, path)
	if !ok {
		// First time seeing this path, find it in the zim file and allocate
		// a node for it
		childEntry, err := fs.zf.GetZimEntryFromStart(parentEntry.Number, parentEntry.Namespace, path)
		if err != nil {
			return fuse.ENOENT
		}
		child = fs.allocateNode(childEntry)
	} else {
		// If we have allocated an inode ID for this namepace+path combination
		// but child was deleted perhaps via a call to ForgetInode,
		// reuse the inodeID since this is a read only filesystem
		child, ok = fs.cache.getInodeById(childId)
		if !ok {
			childEntry, err := fs.zf.GetZimEntryFromStart(parentEntry.Number, parentEntry.Namespace, path)
			if err != nil {
				return fuse.ENOENT
			}
			child = newInode(childId, fs.createInodeAttributes(childEntry), childEntry)
			fs.cache.addInode(child)
		}
	}

	// Fill in the response.
	op.Entry.Child = child.id
	op.Entry.Attributes = child.attributes

	// We don't spontaneously mutate, so the kernel can cache as long as it wants
	// (since it also handles invalidation).
	op.Entry.AttributesExpiration = time.Now().Add(365 * 24 * time.Hour)
	op.Entry.EntryExpiration = op.Entry.AttributesExpiration

	return nil
}

// Open a directory
func (fs *ZimFS) OpenDir(ctx context.Context, op *fuseops.OpenDirOp) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	node, ok := fs.cache.getInodeById(op.Inode)
	if !ok {
		return fuse.ENOENT
	}

	// We don't mutate spontaneosuly, so if the VFS layer has asked for an
	// inode that doesn't exist, something screwed up earlier (a lookup, a
	// cache invalidation, etc.).
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

// LOCK_REQUIRED(fs.mu)
func (fs *ZimFS) makeDirEntries(parent *inode, children []zim.ZimEntry) []fuseutil.Dirent {
	_parent := parent.entry
	parentEntry := _parent.(*zim.DirectoryEntry)
	dirents := []fuseutil.Dirent{}
	var dirType fuseutil.DirentType
	var childEntry *zim.Entry
	for _, child := range children {
		childEntry = child.Get()
		if zim.IsDirectoryEntry(childEntry.MimeType) {
			dirType = fuseutil.DT_Directory
		} else if zim.IsRedirectEntry(childEntry.MimeType) {
			dirType = fuseutil.DT_Link
		} else {
			dirType = fuseutil.DT_File
		}
		name, _ := strings.CutPrefix(parentEntry.Path+"/", childEntry.Path)
		offset := childEntry.Number - parentEntry.Number
		if inodeId, ok := fs.cache.getInodeIdByNsPath(childEntry.Namespace, childEntry.Path); !ok {
			inode := fs.allocateNode(child)
			dirents = append(dirents,
				fs.makeDirEntry(inode.id, name, dirType, offset),
			)

		} else {
			dirents = append(dirents,
				fs.makeDirEntry(inodeId, name, dirType, offset),
			)
		}
	}
	return dirents
}

// Read the entries of a directory previously opened with OpenDir
func (fs *ZimFS) ReadDir(ctx context.Context, op *fuseops.ReadDirOp) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

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

package fs

import (
	"testing"

	"github.com/elfkuzco/zimfs/internal/zim"
	"github.com/jacobsa/fuse/fuseops"
)

func contentEntry(ns rune, path string) *zim.ContentEntry {
	return zim.NewContentEntry(0, 0, ns, 0, path, 0, 0, 0)
}

func TestInodeCacheAddAndGet(t *testing.T) {
	c := NewCache()
	in := newInode(10, contentEntry('C', "a/b"))
	c.addInode(in)

	if got, ok := c.getInodeById(10); !ok || got != in {
		t.Fatalf("getInodeById(10) = %v, %v; want %v, true", got, ok, in)
	}
	if got, ok := c.getInodeByNsPath('C', "a/b", false); !ok || got != in {
		t.Fatalf("getInodeByNsPath = %v, %v; want %v, true", got, ok, in)
	}
	if _, ok := c.getInodeByNsPath('M', "a/b", false); ok {
		t.Errorf("getInodeByNsPath with wrong namespace reported ok")
	}
	if _, ok := c.getInodeByNsPath('C', "a/c", false); ok {
		t.Errorf("getInodeByNsPath with wrong path reported ok")
	}
}

func TestGetOrAddInodeDedup(t *testing.T) {
	c := NewCache()
	var next fuseops.InodeID = 100
	alloc := func() fuseops.InodeID { next++; return next }

	e := contentEntry('C', "x/y")
	a := c.getOrAddInode('C', "x/y", e, 0, alloc, true)
	b := c.getOrAddInode('C', "x/y", e, 0, alloc, false)

	if a != b {
		t.Fatalf("getOrAddInode returned distinct inodes for the same path")
	}
	if a.lookupCount != 1 {
		t.Errorf("lookupCount = %d, want 1", a.lookupCount)
	}
	if !a.lookedUp {
		t.Errorf("lookedUp = false, want true")
	}
}

func TestInodeCacheEviction(t *testing.T) {
	c := NewCache()
	c.maxInodes = 2

	var next fuseops.InodeID = 100
	alloc := func() fuseops.InodeID { next++; return next }

	c.getOrAddInode('C', "a", contentEntry('C', "a"), 0, alloc, false) // 101
	c.getOrAddInode('C', "b", contentEntry('C', "b"), 0, alloc, false) // 102
	c.getOrAddInode('C', "c", contentEntry('C', "c"), 0, alloc, false) // 103, evicts 101

	if _, ok := c.getInodeById(101); ok {
		t.Errorf("inode 101 should have been evicted")
	}
	if _, ok := c.getInodeById(103); !ok {
		t.Errorf("inode 103 should be present")
	}
}

func TestForgetInode(t *testing.T) {
	c := NewCache()
	var next fuseops.InodeID = 100
	alloc := func() fuseops.InodeID { next++; return next }

	in := c.getOrAddInode('C', "a", contentEntry('C', "a"), 0, alloc, true)
	c.forgetInode(in.id, 1)
	if _, ok := c.getInodeById(in.id); ok {
		t.Errorf("inode should be removed after forget")
	}
}

func TestMaterialize(t *testing.T) {
	c := NewCache()
	in := newInode(50, contentEntry('C', "a"))
	c.addInode(in)

	computeCount := 0
	attrs, ok := c.materialize(50, func(i *inode) fuseops.InodeAttributes {
		computeCount++
		return fuseops.InodeAttributes{Size: 123}
	})
	if !ok {
		t.Fatal("materialize reported missing inode")
	}
	if attrs.Size != 123 {
		t.Errorf("Size = %d, want 123", attrs.Size)
	}
	if !in.materialized {
		t.Errorf("materialized = false, want true")
	}

	// A second call must not recompute.
	attrs2, ok := c.materialize(50, func(i *inode) fuseops.InodeAttributes {
		computeCount++
		return fuseops.InodeAttributes{Size: 999}
	})
	if !ok || attrs2.Size != 123 {
		t.Fatalf("second materialize = %+v, %v; want Size 123", attrs2, ok)
	}
	if computeCount != 1 {
		t.Errorf("compute ran %d times, want 1", computeCount)
	}

	if _, ok := c.materialize(9999, func(i *inode) fuseops.InodeAttributes { return fuseops.InodeAttributes{} }); ok {
		t.Errorf("materialize on missing inode reported ok")
	}
}

func TestBuildPathName(t *testing.T) {
	root := newInode(fuseops.RootInodeID, zim.NewDirectoryEntry('C', "", 0))
	if got := root.buildPathName("example.com"); got != "example.com" {
		t.Errorf("root buildPathName = %q, want %q", got, "example.com")
	}

	child := newInode(10, contentEntry('C', "example.com"))
	if got := child.buildPathName("index.html"); got != "example.com/index.html" {
		t.Errorf("child buildPathName = %q, want %q", got, "example.com/index.html")
	}
}

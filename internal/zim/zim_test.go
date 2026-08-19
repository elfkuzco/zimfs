package zim

import (
	"bytes"
	"encoding/binary"
	"sort"
	"testing"

	"github.com/edsrzf/mmap-go"
)

// testDirEntry describes a single directory entry used to build a synthetic
// ZIM file for testing. A mimeType of 0 selects a regular content entry.
type testDirEntry struct {
	namespace     rune
	path          string
	mimeType      uint16
	redirectIndex uint32
}

// appendDirent encodes a single directory entry into buf. Content entries have
// a 16-byte header before the path; redirect entries have a 12-byte header.
func appendDirent(buf *bytes.Buffer, e testDirEntry) {
	binary.Write(buf, binary.LittleEndian, e.mimeType)
	buf.WriteByte(0) // parameter length
	buf.WriteByte(byte(e.namespace))
	binary.Write(buf, binary.LittleEndian, uint32(0)) // revision
	if IsRedirectEntry(e.mimeType) {
		binary.Write(buf, binary.LittleEndian, e.redirectIndex)
	} else {
		binary.Write(buf, binary.LittleEndian, uint32(0)) // cluster number
		binary.Write(buf, binary.LittleEndian, uint32(0)) // blob number
	}
	buf.WriteString(e.path)
	buf.WriteByte(0) // zero-terminated path
}

// buildZimFile assembles an in-memory ZimFile laid out like a real ZIM archive:
// the directory entries first, followed by the path pointer list pointed to by
// PathPtrPos. Entries are sorted by (namespace, path)
func buildZimFile(t *testing.T, entries []testDirEntry) *ZimFile {
	t.Helper()

	var offsets []uint64
	var buf bytes.Buffer

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].namespace != entries[j].namespace {
			return entries[i].namespace < entries[j].namespace
		}

		return entries[i].path < entries[j].path
	})

	for _, e := range entries {
		offsets = append(offsets, uint64(buf.Len()))
		appendDirent(&buf, e)
	}

	pathPtrPos := uint64(buf.Len())
	for _, off := range offsets {
		binary.Write(&buf, binary.LittleEndian, off)
	}

	return &ZimFile{
		Header: &Header{
			EntryCount: uint32(len(entries)),
			PathPtrPos: pathPtrPos,
		},
		contents: mmap.MMap(buf.Bytes()),
	}
}

func TestGetEntryLowerBoundEmptyZimFile(t *testing.T) {
	zf := buildZimFile(t, nil)

	if got := zf.getEntryLowerBound(0, 'A', "foo"); got != 0 {
		t.Fatalf("GetEntryLowerBound(%q, %q) on empty archive = %d, want 0", 'A', "foo", got)
	}
}

func TestGetEntryLowerBound(t *testing.T) {
	zf := buildZimFile(t, []testDirEntry{
		{namespace: 'C', path: "example.com/about.html"},
		{namespace: 'C', path: "example.com/assets/style.css"},
		{namespace: 'C', path: "example.com/assets/image.png"},
		{namespace: 'C', path: "example.com/index.html"},
		{namespace: 'M', path: "Creator"},
		{namespace: 'M', path: "Publisher"},
	})

	tests := []struct {
		name      string
		namespace rune
		path      string
		want      uint32
	}{
		{"exact path match", 'C', "example.com/index.hml", 3},
		{"path exits but different namespace", 'M', "example.com/index.html", 5},
		{"inexistent directory starts at first matching file", 'C', "example.com/assets", 1},
		{"inexistent path", 'C', "about.html", 0},
		{"exact metadata", 'M', "Creator", 4},
		{"inexistent metadata", 'M', "Scraper", 5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := zf.getEntryLowerBound(0, tc.namespace, tc.path); got != tc.want {
				t.Errorf("GetEntryLowerBound(%q, %q) = %d, want %d", tc.namespace, tc.path, got, tc.want)
			}
		})
	}
}

func TestGetPathName(t *testing.T) {
	var buf bytes.Buffer
	first := uint64(buf.Len())
	buf.WriteString("example.com/index.html")
	buf.WriteByte(0)
	second := uint64(buf.Len())
	buf.WriteString("example.com/assets/style.css")
	buf.WriteByte(0)

	zf := &ZimFile{contents: mmap.MMap(buf.Bytes())}

	tests := []struct {
		name  string
		start uint64
		want  string
	}{
		{"zero-terminated path", first, "example.com/index.html"},
		{"second zero-terminated path", second, "example.com/assets/style.css"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := zf.getPathName(tc.start); got != tc.want {
				t.Errorf("getPathName(%d) = %q, want %q", tc.start, got, tc.want)
			}
		})
	}
}

func TestGetZimEntry(t *testing.T) {
	zf := buildZimFile(t, []testDirEntry{
		{namespace: 'C', path: "example.com/about.html"},
		{namespace: 'C', path: "example.com/assets/css/style.css"},
		{namespace: 'C', path: "example.com/assets/js/main.js"},
		{namespace: 'C', path: "example.com/index.html"},
		{namespace: 'C', path: "example.com/legacy.html", mimeType: REDIRECT_ENTRY_MIMETYPE, redirectIndex: 3},
		{namespace: 'M', path: "Creator"},
		{namespace: 'M', path: "Publisher"},
	})

	tests := []struct {
		name      string
		namespace rune
		path      string
		wantKind  string // content, directory, redirect, or notfound
		wantPath  string
	}{
		{"exact content entry", 'C', "example.com/index.html", "content", "example.com/index.html"},
		{"exact metadata entry", 'M', "Publisher", "content", "Publisher"},
		{"implicit directory", 'C', "example.com/assets", "directory", "example.com/assets"},
		{"redirect entry", 'C', "example.com/legacy.html", "redirect", "example.com/legacy.html"},
		{"unknown path", 'C', "example.com/unknown.html", "notfound", ""},
		{"wrong namespace", 'M', "example.com/index.html", "notfound", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry, err := zf.GetZimEntry(tc.namespace, tc.path)

			if tc.wantKind == "notfound" {
				if err != EntryDoesNotExist {
					t.Fatalf("GetZimEntry(%q, %q) error = %v, want %v", tc.namespace, tc.path, err, EntryDoesNotExist)
				}
				if entry != nil {
					t.Fatalf("GetZimEntry(%q, %q) = %v, want nil", tc.namespace, tc.path, entry)
				}
				return
			}

			if err != nil {
				t.Fatalf("GetZimEntry(%q, %q) error = %v, want nil", tc.namespace, tc.path, err)
			}

			if entry.Get().Namespace != tc.namespace || entry.Get().Path != tc.wantPath {
				t.Errorf("GetZimEntry(%q, %q) = %q/%q, want %q/%q",
					tc.namespace, tc.path, entry.Get().Namespace, entry.Get().Path, tc.namespace, tc.wantPath)
			}

			switch tc.wantKind {
			case "content":
				if _, ok := entry.(*ContentEntry); !ok {
					t.Errorf("type = %T, want *ContentEntry", entry)
				}
			case "directory":
				if _, ok := entry.(*DirectoryEntry); !ok {
					t.Errorf("type = %T, want *DirectoryEntry", entry)
				}
			case "redirect":
				if _, ok := entry.(*RedirectEntry); !ok {
					t.Errorf("type = %T, want *RedirectEntry", entry)
				}
			}
		})
	}
}

func TestGetZimEntryFromStart(t *testing.T) {
	zf := buildZimFile(t, []testDirEntry{
		{namespace: 'C', path: "example.com/about.html"},
		{namespace: 'C', path: "example.com/index.html"},
	})

	t.Run("finds entry at start", func(t *testing.T) {
		entry, err := zf.GetZimEntryFromStart(0, 'C', "example.com/about.html")
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		if entry.Get().Path != "example.com/about.html" {
			t.Errorf("Path = %q, want %q", entry.Get().Path, "example.com/about.html")
		}
	})

	t.Run("start past entry skips it", func(t *testing.T) {
		entry, err := zf.GetZimEntryFromStart(1, 'C', "example.com/about.html")
		if err != EntryDoesNotExist {
			t.Errorf("error = %v, want %v", err, EntryDoesNotExist)
		}
		if entry != nil {
			t.Errorf("entry = %v, want nil", entry)
		}
	})
}

// childExpectation describes an expected entry returned by GetChildren.
type childExpectation struct {
	kind      string // "content", "directory", or "redirect"
	namespace rune
	path      string
	offset    uint32 // only checked for directories
}

// assertChildren verifies that GetChildren returned the expected entries in order.
func assertChildren(t *testing.T, got []ZimEntry, want []childExpectation) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("GetChildren() returned %d entries, want %d", len(got), len(want))
	}

	for i, w := range want {
		g := got[i]
		entry := g.Get()

		if entry.Namespace != w.namespace {
			t.Errorf("child[%d].Namespace = %q, want %q", i, entry.Namespace, w.namespace)
		}
		if entry.Path != w.path {
			t.Errorf("child[%d].Path = %q, want %q", i, entry.Path, w.path)
		}

		switch w.kind {
		case "content":
			if _, ok := g.(*ContentEntry); !ok {
				t.Errorf("child[%d] type = %T, want *ContentEntry", i, g)
			}
		case "redirect":
			if _, ok := g.(*RedirectEntry); !ok {
				t.Errorf("child[%d] type = %T, want *RedirectEntry", i, g)
			}
		case "directory":
			d, ok := g.(*DirectoryEntry)
			if !ok {
				t.Errorf("child[%d] type = %T, want *DirectoryEntry", i, g)
				continue
			}
			if d.Number != w.offset {
				t.Errorf("child[%d].Number = %d, want %d", i, d.Number, w.offset)
			}
		}
	}
}

// directoryEntry resolves path to an implicit directory entry using GetZimEntry.
func directoryEntry(t *testing.T, zf *ZimFile, namespace rune, path string) *DirectoryEntry {
	t.Helper()

	entry, err := zf.GetZimEntry(namespace, path)
	if err != nil {
		t.Fatalf("GetZimEntry(%q, %q) error = %v", namespace, path, err)
	}
	dir, ok := entry.(*DirectoryEntry)
	if !ok {
		t.Fatalf("GetZimEntry(%q, %q) type = %T, want *DirectoryEntry", namespace, path, entry)
	}
	return dir
}

func TestGetChildren(t *testing.T) {
	t.Run("returns top-level files and directories", func(t *testing.T) {
		zf := buildZimFile(t, []testDirEntry{
			{namespace: 'C', path: "example.com/about.html"},
			{namespace: 'C', path: "example.com/assets/css/style.css"},
			{namespace: 'C', path: "example.com/assets/js/main.js"},
			{namespace: 'C', path: "example.com/index.html"},
			{namespace: 'C', path: "example.com/redirect.html", mimeType: REDIRECT_ENTRY_MIMETYPE, redirectIndex: 3},
			{namespace: 'M', path: "Creator"},
		})

		dir := directoryEntry(t, zf, 'C', "example.com")

		assertChildren(t, zf.GetChildren(dir, 0), []childExpectation{
			{"content", 'C', "example.com/about.html", 0},
			{"directory", 'C', "example.com/assets", 1},
			{"content", 'C', "example.com/index.html", 0},
			{"redirect", 'C', "example.com/redirect.html", 0},
		})
	})

	t.Run("deduplicates sibling files under a directory", func(t *testing.T) {
		zf := buildZimFile(t, []testDirEntry{
			{namespace: 'C', path: "example.com/index.html"},
			{namespace: 'C', path: "example.com/js/index.js"},
			{namespace: 'C', path: "example.com/js/neuron.js"},
		})

		dir := directoryEntry(t, zf, 'C', "example.com")

		assertChildren(t, zf.GetChildren(dir, 0), []childExpectation{
			{"content", 'C', "example.com/index.html", 0},
			{"directory", 'C', "example.com/js", 1},
		})
	})

	t.Run("skips deleted and link target entries", func(t *testing.T) {
		zf := buildZimFile(t, []testDirEntry{
			{namespace: 'C', path: "example.com/about.html"},
			{namespace: 'C', path: "example.com/deleted.html", mimeType: DELETED_ENTRY_MIMETYPE},
			{namespace: 'C', path: "example.com/index.html"},
			{namespace: 'C', path: "example.com/link.html", mimeType: LINK_TARGET_MIMETYPE},
		})

		dir := NewDirectoryEntry('C', "example.com", 0)

		assertChildren(t, zf.GetChildren(dir, 0), []childExpectation{
			{"content", 'C', "example.com/about.html", 0},
			{"content", 'C', "example.com/index.html", 0},
		})
	})

	t.Run("requires a trailing slash for prefix match", func(t *testing.T) {
		zf := buildZimFile(t, []testDirEntry{
			{namespace: 'C', path: "example.com/assets/style.css"},
			{namespace: 'C', path: "example.com/assets2/other.html"},
		})

		dir := directoryEntry(t, zf, 'C', "example.com/assets")

		assertChildren(t, zf.GetChildren(dir, 0), []childExpectation{
			{"content", 'C', "example.com/assets/style.css", 0},
		})
	})

	t.Run("offset skips earlier children", func(t *testing.T) {
		zf := buildZimFile(t, []testDirEntry{
			{namespace: 'C', path: "example.com/a.html"},
			{namespace: 'C', path: "example.com/b.html"},
			{namespace: 'C', path: "example.com/c.html"},
		})

		dir := directoryEntry(t, zf, 'C', "example.com")

		assertChildren(t, zf.GetChildren(dir, 1), []childExpectation{
			{"content", 'C', "example.com/b.html", 0},
			{"content", 'C', "example.com/c.html", 0},
		})
	})

	t.Run("empty directory returns no children", func(t *testing.T) {
		zf := buildZimFile(t, []testDirEntry{
			{namespace: 'C', path: "example.com/about.html"},
		})

		dir := NewDirectoryEntry('C', "example.com/empty", zf.EntryCount)

		assertChildren(t, zf.GetChildren(dir, 0), nil)
	})
}

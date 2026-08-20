package fs

import (
	"errors"
	"os"
	"testing"

	"github.com/elfkuzco/zimfs/internal/zim"
	"github.com/jacobsa/fuse"
)

func TestMapError(t *testing.T) {
	f := &ZimFS{}

	tests := []struct {
		name string
		in   error
		want error
	}{
		{"nil", nil, nil},
		{"entry does not exist", zim.EntryDoesNotExist, fuse.ENOENT},
		{"corrupt data", zim.CorruptData, fuse.EIO},
		{"other error", errors.New("boom"), fuse.EIO},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := f.mapError(tc.in); got != tc.want {
				t.Errorf("mapError(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestGetMode(t *testing.T) {
	f := &ZimFS{}

	tests := []struct {
		name     string
		mimeType uint16
		want     os.FileMode
	}{
		{"directory", zim.DIRECTORY_ENTRY_MIMETYPE, 0555 | os.ModeDir},
		{"redirect", zim.REDIRECT_ENTRY_MIMETYPE, 0777 | os.ModeSymlink},
		{"content", 0, 0444},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := f.getMode(&zim.Entry{MimeType: tc.mimeType}); got != tc.want {
				t.Errorf("getMode(%#x) = %v, want %v", tc.mimeType, got, tc.want)
			}
		})
	}
}

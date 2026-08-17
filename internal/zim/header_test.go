package zim

import (
	"encoding/binary"
	"os"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

// writeHeaderBytes serializes a Header into the 80-byte on-disk ZIM header.
func writeHeaderBytes(t *testing.T, h *Header) []byte {
	t.Helper()

	buf := make([]byte, ZIM_HEADER_LENGTH)
	binary.LittleEndian.PutUint32(buf[0:4], h.MagicNumber)
	binary.LittleEndian.PutUint16(buf[4:6], h.MajorVersion)
	binary.LittleEndian.PutUint16(buf[6:8], h.MinorVersion)
	copy(buf[8:24], h.Id[:])
	binary.LittleEndian.PutUint32(buf[24:28], h.EntryCount)
	binary.LittleEndian.PutUint32(buf[28:32], h.ClusterCount)
	binary.LittleEndian.PutUint64(buf[32:40], h.PathPtrPos)
	binary.LittleEndian.PutUint64(buf[40:48], h.TitlePtrPos)
	binary.LittleEndian.PutUint64(buf[48:56], h.ClusterPtrPos)
	binary.LittleEndian.PutUint64(buf[56:64], h.MimeListPos)
	binary.LittleEndian.PutUint32(buf[64:68], h.MainPage)
	binary.LittleEndian.PutUint32(buf[68:72], h.LayoutPage)
	binary.LittleEndian.PutUint64(buf[72:80], h.ChecksumPos)
	return buf
}

// tempFile writes data to a temporary file and returns it still open
func tempFile(t *testing.T, data []byte) *os.File {
	t.Helper()

	f, err := os.CreateTemp(t.TempDir(), "zim-header-*")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	t.Cleanup(func() { f.Close() })

	if _, err := f.Write(data); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}
	return f
}

func TestReadHeader(t *testing.T) {
	id := uuid.New()
	want := &Header{
		MagicNumber:   ZIM_MAGIC_NUMBER,
		MajorVersion:  6,
		MinorVersion:  1,
		Id:            id,
		EntryCount:    1234,
		ClusterCount:  56,
		PathPtrPos:    80,
		TitlePtrPos:   200,
		ClusterPtrPos: 300,
		MimeListPos:   400,
		MainPage:      1,
		LayoutPage:    0xffffffff,
		ChecksumPos:   500,
	}

	got, err := ReadHeader(tempFile(t, writeHeaderBytes(t, want)))
	if err != nil {
		t.Fatalf("ReadHeader() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadHeader() = %+v, want %+v", got, want)
	}
}

func TestReadHeaderInvalidMagic(t *testing.T) {
	h := &Header{MagicNumber: 0xdeadbeef}

	_, err := ReadHeader(tempFile(t, writeHeaderBytes(t, h)))
	if err != InvalidZimHeader {
		t.Errorf("ReadHeader() error = %v, want %v", err, InvalidZimHeader)
	}
}

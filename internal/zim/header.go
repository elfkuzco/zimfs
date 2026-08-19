package zim

import (
	"errors"
	"io"
	"os"

	"github.com/google/uuid"
)

var ZIM_HEADER_LENGTH = 80

var ZIM_MAGIC_NUMBER uint32 = 72173914

var InvalidZimHeader = errors.New("invalid ZIM header")

// Header for a ZIM archive populated from the first 80 bytes of the ZIM file
type Header struct {
	// magic number to recognize the file format, must be 72173914 (0x44D495A)
	MagicNumber uint32
	// major version of the ZIM archive format
	MajorVersion uint16
	// minor version of the ZIM archive format
	MinorVersion uint16
	// unique if of this ZIM archive
	Id uuid.UUID
	// total number of entries
	EntryCount uint32
	// total number of clusters
	ClusterCount uint32
	// position of the directory pointer list ordered by Path
	PathPtrPos uint64
	// position of the directory pointer list ordered by Title
	TitlePtrPos uint64
	// position of the cluster pointer list
	ClusterPtrPos uint64
	// position of the MIME type list
	MimeListPos uint64
	// main page or 0xffffffff if no main page
	MainPage uint32
	// layout page or 0xffffffffff if no layout page (deprecated, always 0xffffffffff)
	LayoutPage uint32
	// pointer to the md5checksum of this archive without the checksum itself
	ChecksumPos uint64
}

// Read and validate header information from ZIM file
func ReadHeader(file *os.File) (*Header, error) {
	buffer := make([]byte, ZIM_HEADER_LENGTH)
	n, err := file.ReadAt(buffer, 0)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if n != ZIM_HEADER_LENGTH {
		return nil, InvalidZimHeader
	}

	magicNumber := readUint32(buffer, 0)
	if magicNumber != ZIM_MAGIC_NUMBER {
		return nil, InvalidZimHeader
	}

	id, err := uuid.FromBytes(buffer[8:24])
	if err != nil {
		return nil, InvalidZimHeader
	}

	header := &Header{
		MagicNumber:   magicNumber,
		MajorVersion:  readUint16(buffer, 4),
		MinorVersion:  readUint16(buffer, 6),
		Id:            id,
		EntryCount:    readUint32(buffer, 24),
		ClusterCount:  readUint32(buffer, 28),
		PathPtrPos:    readUint64(buffer, 32),
		TitlePtrPos:   readUint64(buffer, 40),
		ClusterPtrPos: readUint64(buffer, 48),
		MimeListPos:   readUint64(buffer, 56),
		MainPage:      readUint32(buffer, 64),
		LayoutPage:    readUint32(buffer, 68),
		ChecksumPos:   readUint64(buffer, 72),
	}
	return header, nil
}

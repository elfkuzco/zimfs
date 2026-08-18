package zim

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

type Compression uint8
type OffsetSize uint64

var (
	UnregisteredCompression = errors.New("cluster for compression is not registered")
)

const (
	COMPRESSION_MASK = 0x0f
	EXTENDED_MASK    = 0x10
)

const (
	None Compression = iota + 1
	Zlib
	Bzip2
	Lzma2
	Zstd
)

const (
	Normal   OffsetSize = 4
	Extended OffsetSize = 8
)

type Cluster struct {
	Contents []byte
}

func (c *Cluster) GetCompression() Compression {
	return Compression(c.Contents[0] & COMPRESSION_MASK)
}

func (c *Cluster) IsExtended() bool {
	return (c.Contents[0] & EXTENDED_MASK) != 0
}

func (c *Cluster) GetOffsetSize() OffsetSize {
	if c.IsExtended() {
		return Extended
	}
	return Normal
}

func (c *Cluster) ReadOffset(data []byte, size OffsetSize) uint64 {
	if size == Extended {
		return binary.LittleEndian.Uint64(data)
	}
	return uint64(binary.LittleEndian.Uint32(data))
}

func (c *Cluster) ReadBlobOffset(n uint32, size OffsetSize) uint64 {
	data := c.Contents[uint64(n-1)*uint64(size)+1:]
	return c.ReadOffset(data, size)
}

type ClusterReader interface {
	IsExtended() bool
	GetCompression() Compression
	GetOffsetSize() OffsetSize
	// Get blob N in the cluster
	GetBlob(n uint32) ([]byte, error)
	// Get the size blob N in the cluster
	GetBlobSize(n uint32) (uint64, error)
}

type UncompressedCluster struct {
	*Cluster
}

// blobRange returns the start and end offsets of blob n in the cluster's
// offset table.
func (ucc *UncompressedCluster) blobRange(n uint32) (start, end uint64) {
	offsetSize := ucc.GetOffsetSize()
	return ucc.ReadBlobOffset(n, offsetSize), ucc.ReadBlobOffset(n+1, offsetSize)
}

func (ucc *UncompressedCluster) GetBlobSize(n uint32) (uint64, error) {
	// According to the docs, the size of one blob is calculated by the difference
	// of two consecutive offsets.
	start, end := ucc.blobRange(n)
	return end - start, nil
}

func (ucc *UncompressedCluster) GetBlob(n uint32) ([]byte, error) {
	start, end := ucc.blobRange(n)
	return ucc.Contents[start:end], nil
}

type CompressedCluster struct {
	*Cluster
	compression Compression
}

func (cc *CompressedCluster) newReader() (io.ReadCloser, error) {
	switch cc.compression {
	case Zstd:
		decoder, err := zstd.NewReader(bytes.NewReader(cc.Contents[1:]))
		if err != nil {
			return nil, err
		}
		return decoder.IOReadCloser(), nil
	default:
		return nil, UnregisteredCompression
	}
}

// readOffsets decompresses the cluster's offset table and returns it along with
// the decompressed reader positioned at the start of the blob data. The caller
// is responsible for closing the reader.
func (cc *CompressedCluster) readOffsets() ([]uint64, io.ReadCloser, error) {
	r, err := cc.newReader()
	if err != nil {
		return nil, nil, err
	}

	offsetSize := cc.GetOffsetSize()

	// The first offset points to the start of the first blob's data, so the
	// number of offsets is that value divided by the offset size.
	firstBuf := make([]byte, offsetSize)
	if _, err = io.ReadFull(r, firstBuf); err != nil {
		r.Close()
		return nil, nil, err
	}
	firstOffset := cc.ReadOffset(firstBuf, offsetSize)
	nOffsets := firstOffset / uint64(offsetSize)
	if nOffsets < 2 {
		r.Close()
		return nil, nil, fmt.Errorf("invalid cluster: too few offsets")
	}

	// Read the rest of the offset table.
	table := make([]byte, firstOffset-uint64(offsetSize))
	if _, err = io.ReadFull(r, table); err != nil {
		r.Close()
		return nil, nil, err
	}

	offsets := make([]uint64, nOffsets)
	offsets[0] = firstOffset
	for i := uint64(1); i < nOffsets; i++ {
		offsets[i] = cc.ReadOffset(table[(i-1)*uint64(offsetSize):], offsetSize)
	}

	return offsets, r, nil
}

func (cc *CompressedCluster) GetBlobSize(n uint32) (uint64, error) {
	offsets, r, err := cc.readOffsets()
	if err != nil {
		return 0, err
	}
	defer r.Close()

	if uint64(n)+1 >= uint64(len(offsets)) {
		return 0, fmt.Errorf("blob number %d out of range", n)
	}
	return offsets[n+1] - offsets[n], nil
}

func (cc *CompressedCluster) GetBlob(n uint32) ([]byte, error) {
	offsets, r, err := cc.readOffsets()
	if err != nil {
		return nil, err
	}
	defer r.Close()

	if uint64(n)+1 >= uint64(len(offsets)) {
		return nil, fmt.Errorf("blob number %d out of range", n)
	}

	start := offsets[n]
	size := offsets[n+1] - start

	// Skip blobs 0..n-1.
	if _, err := io.CopyN(io.Discard, r, int64(start-offsets[0])); err != nil {
		return nil, err
	}

	out := make([]byte, size)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, err
	}

	return out, nil
}

func NewCluster(contents []byte) (ClusterReader, error) {
	cluster := Cluster{contents}
	switch compression := cluster.GetCompression(); compression {
	case None:
		return &UncompressedCluster{
			Cluster: &cluster,
		}, nil
	case Zstd:
		return &CompressedCluster{
			Cluster:     &cluster,
			compression: compression,
		}, nil
	default:
		return nil, UnregisteredCompression
	}
}

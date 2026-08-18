package zim

import (
	"encoding/binary"
	"errors"
)

type Compression uint8
type OffsetSize uint64

var (
	ClusterNotFound = errors.New("cluster for compression is not registered")
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

func (c *Cluster) ReadBlobOffset(n uint32, size OffsetSize) uint64 {
	data := c.Contents[uint64(n-1)*uint64(size)+1:]
	if size == Extended {
		return binary.LittleEndian.Uint64(data)
	}
	return uint64(binary.LittleEndian.Uint32(data))
}

type ClusterReader interface {
	IsExtended() bool
	GetCompression() Compression
	GetOffsetSize() OffsetSize
	// Get blob N in the cluster
	GetBlob(n uint32) []byte
	// Get the size blob N in the cluster
	GetBlobSize(n uint32) uint64
}

type UncompressedCluster struct {
	*Cluster
}

func (ucc *UncompressedCluster) GetBlobSize(n uint32) uint64 {
	// According to the docs, the size of one blob is calculated by the difference
	// of two consecutive offsets
	offsetSize := ucc.GetOffsetSize()
	start := ucc.ReadBlobOffset(n, offsetSize)
	end := ucc.ReadBlobOffset(n+1, offsetSize)
	return end - start
}

func (ucc *UncompressedCluster) GetBlob(n uint32) []byte {
	offsetSize := ucc.GetOffsetSize()
	start := ucc.ReadBlobOffset(n, offsetSize)
	end := ucc.ReadBlobOffset(n+1, offsetSize)
	return ucc.Contents[start : start+(end-start)]
}

func NewCluster(contents []byte) (ClusterReader, error) {
	cluster := Cluster{contents}
	switch cluster.GetCompression() {
	case None:
		return &UncompressedCluster{
			Cluster: &cluster,
		}, nil
	default:
		return nil, ClusterNotFound
	}
}

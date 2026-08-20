package zim

import (
	"bytes"
	"compress/bzip2"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
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

// maxOffsetTableSize caps the size of a cluster offset table. Real ZIM clusters
// hold at most a few thousand blobs, so even a 64 MiB table is orders of
// magnitude larger than any legitimate archive; the cap only guards against a
// corrupt stream causing a multi-gigabyte allocation.
const maxOffsetTableSize = 1 << 26

// uncompressed cluster data without leading byte for cluster information
type clusterData struct {
	offsets []uint64
	blobs   []byte
}

// size of the cluster data
func (c *clusterData) Size() uint64 {
	return uint64(len(c.blobs)) + uint64(len(c.offsets))*8
}

// size of the blob at n
func (c *clusterData) BlobSize(n uint32) (uint64, error) {
	if uint64(n)+1 >= uint64(len(c.offsets)) {
		return 0, fmt.Errorf("blob number %d out of range", n)
	}
	if c.offsets[n+1] < c.offsets[n] {
		return 0, CorruptData
	}
	return c.offsets[n+1] - c.offsets[n], nil
}

func (c *clusterData) Blob(n uint32) ([]byte, error) {
	if uint64(n)+1 >= uint64(len(c.offsets)) {
		return nil, fmt.Errorf("blob number %d out of range", n)
	}

	base := c.offsets[0]
	start := c.offsets[n]
	end := c.offsets[n+1]
	if start < base || end < start || end-base > uint64(len(c.blobs)) {
		return nil, CorruptData
	}
	return c.blobs[start-base : end-base], nil
}

type Cluster struct {
	Contents []byte // full data including info byte
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

// readOffset reads a 4- or 8-byte offset from data without bounds checking.
// Callers must have already validated len(data) >= size.
func (c *Cluster) readOffset(data []byte, size OffsetSize) uint64 {
	if size == Extended {
		return binary.LittleEndian.Uint64(data)
	}
	return uint64(binary.LittleEndian.Uint32(data))
}

func (c *Cluster) readBlobOffset(n uint32, size OffsetSize) (uint64, error) {
	start := uint64(n)*uint64(size) + 1
	end := start + uint64(size)
	if end > uint64(len(c.Contents)) {
		return 0, CorruptData
	}
	return c.readOffset(c.Contents[start:end], size), nil
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
	compression Compression
}

func (ucc *UncompressedCluster) getData() (clusterData, error) {
	offsetSize := ucc.GetOffsetSize()

	firstOffset := ucc.readOffset(ucc.Contents[1:], offsetSize)
	nOffsets := firstOffset / uint64(offsetSize)
	if nOffsets < 2 {
		logger.Error("too few offsets in cluster")
		return clusterData{}, CorruptData
	}
	if firstOffset%uint64(offsetSize) != 0 {
		return clusterData{}, CorruptData
	}
	offsets := make([]uint64, nOffsets)
	for i := range offsets {
		offsets[i] = ucc.readOffset(ucc.Contents[1+uint64(i)*uint64(offsetSize):],
			offsetSize,
		)
	}
	base := offsets[0]
	return clusterData{offsets: offsets, blobs: ucc.Contents[1+base:]}, nil
}

func (ucc *UncompressedCluster) GetBlobSize(n uint32) (uint64, error) {
	data, err := ucc.getData()
	if err != nil {
		return 0, err
	}
	return data.BlobSize(n)
}

func (ucc *UncompressedCluster) GetBlob(n uint32) ([]byte, error) {
	data, err := ucc.getData()
	if err != nil {
		return nil, err
	}
	return data.Blob(n)
}

type CompressedCluster struct {
	*Cluster

	clusterNumber uint32
	cache         *clusterCache

	mu sync.Mutex // serializes this cluster's initial decompression
}

func (cc *CompressedCluster) newReader() (io.ReadCloser, error) {
	// the contents start from 1. Data at 0 just stores the cluster information
	contents := bytes.NewReader(cc.Contents[1:])
	switch compression := cc.GetCompression(); compression {
	case Zstd:
		decoder, err := zstd.NewReader(contents)
		if err != nil {
			return nil, err
		}
		return decoder.IOReadCloser(), nil
	case Lzma2:
		decoder, err := xz.NewReader(contents)
		if err != nil {
			return nil, err
		}
		return io.NopCloser(decoder), nil
	case Bzip2:
		decoder := bzip2.NewReader(contents)
		return io.NopCloser(decoder), nil
	case Zlib:
		decoder, err := zlib.NewReader(contents)
		if err != nil {
			return nil, err
		}
		return decoder, nil
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
		return nil, nil, err
	}
	firstOffset := cc.readOffset(firstBuf, offsetSize)
	if firstOffset > maxOffsetTableSize {
		logger.Error("offset table too large")
		return nil, nil, CorruptData
	}
	nOffsets := firstOffset / uint64(offsetSize)
	if nOffsets < 2 {
		logger.Error("too few offsets in cluster")
		return nil, nil, CorruptData
	}

	if firstOffset%uint64(offsetSize) != 0 {
		return nil, nil, CorruptData
	}

	// Read the rest of the offset table.
	table := make([]byte, firstOffset-uint64(offsetSize))
	if _, err = io.ReadFull(r, table); err != nil {
		return nil, nil, err
	}

	offsets := make([]uint64, nOffsets)
	offsets[0] = firstOffset
	for i := uint64(1); i < nOffsets; i++ {
		offsets[i] = cc.readOffset(table[(i-1)*uint64(offsetSize):], offsetSize)
	}

	return offsets, r, nil
}

// decompress reads and decodes the whole cluster, returning its offset table
// and blob data.
func (cc *CompressedCluster) decompress() (clusterData, error) {
	offsets, r, err := cc.readOffsets()
	if err != nil {
		return clusterData{}, err
	}
	defer r.Close()

	base := offsets[0]
	total := offsets[len(offsets)-1]
	if total < base {
		return clusterData{}, CorruptData
	}
	size := total - base
	if size > uint64(^uint(0)>>1) {
		return clusterData{}, CorruptData
	}

	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return clusterData{}, err
	}
	return clusterData{offsets: offsets, blobs: data}, nil
}

// return the cluster's decompressed data, decompressing it at
// most once per cluster and storing the result in the shared bounded cache.
func (cc *CompressedCluster) getData() (clusterData, error) {
	if data, ok := cc.cache.get(cc.clusterNumber); ok {
		return data, nil
	}

	cc.mu.Lock()
	defer cc.mu.Unlock()

	// Another goroutine may have decompressed while we waited for cc.mu.
	if data, ok := cc.cache.get(cc.clusterNumber); ok {
		return data, nil
	}

	data, err := cc.decompress()
	if err != nil {
		return data, err
	}
	cc.cache.put(cc.clusterNumber, data)
	return data, nil
}

func (cc *CompressedCluster) GetBlobSize(n uint32) (uint64, error) {
	data, err := cc.getData()
	if err != nil {
		return 0, err
	}
	return data.BlobSize(n)
}

func (cc *CompressedCluster) GetBlob(n uint32) ([]byte, error) {
	data, err := cc.getData()
	if err != nil {
		return nil, err
	}
	return data.Blob(n)
}

func NewCluster(contents []byte) (ClusterReader, error) {
	if len(contents) == 0 {
		return nil, CorruptData
	}
	cluster := Cluster{contents}
	switch compression := cluster.GetCompression(); compression {
	case None:
		return &UncompressedCluster{
			Cluster: &cluster,
		}, nil
	case Zlib, Bzip2, Lzma2, Zstd:
		return &CompressedCluster{
			Cluster: &cluster,
		}, nil
	default:
		logger.Error("no cluster registered", "compression", compression)
		return nil, UnregisteredCompression
	}
}

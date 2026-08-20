package zim

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

func TestClusterReadBlobOffset(t *testing.T) {
	c := Cluster{Contents: make([]byte, 1+3*4)}
	c.Contents[0] = byte(None)
	binary.LittleEndian.PutUint32(c.Contents[1:5], 20)
	binary.LittleEndian.PutUint32(c.Contents[5:9], 15)
	binary.LittleEndian.PutUint32(c.Contents[9:13], 10)

	got, err := c.readBlobOffset(1, Normal)
	if err != nil {
		t.Fatalf("readBlobOffset(1) error = %v", err)
	}
	if got != 15 {
		t.Errorf("readBlobOffset(1) = %d, want 15", got)
	}

	if _, err := c.readBlobOffset(10, Normal); err != CorruptData {
		t.Errorf("readBlobOffset(10) error = %v, want CorruptData", err)
	}
}

func buildUncompressedCluster(extended bool, blobs [][]byte) []byte {
	offsetSize := uint64(Normal)
	if extended {
		offsetSize = uint64(Extended)
	}

	nOffsets := len(blobs) + 1
	base := uint64(nOffsets) * offsetSize
	offsets := make([]uint64, nOffsets)
	offsets[0] = base
	for i, b := range blobs {
		offsets[i+1] = offsets[i] + uint64(len(b))
	}

	info := byte(None)
	if extended {
		info |= EXTENDED_MASK
	}

	var buf bytes.Buffer
	buf.WriteByte(info)
	for _, o := range offsets {
		if extended {
			_ = binary.Write(&buf, binary.LittleEndian, o)
		} else {
			_ = binary.Write(&buf, binary.LittleEndian, uint32(o))
		}
	}
	for _, b := range blobs {
		buf.Write(b)
	}
	return buf.Bytes()
}

func TestUncompressedCluster(t *testing.T) {
	for _, extended := range []bool{false, true} {
		name := "normal"
		if extended {
			name = "extended"
		}
		t.Run(name, func(t *testing.T) {
			blobs := [][]byte{[]byte("hello"), []byte("world!!")}
			cr, err := NewCluster(buildUncompressedCluster(extended, blobs))
			if err != nil {
				t.Fatalf("NewCluster error = %v", err)
			}
			ucc, ok := cr.(*UncompressedCluster)
			if !ok {
				t.Fatalf("type = %T, want *UncompressedCluster", cr)
			}

			if got, err := ucc.GetBlob(0); err != nil || string(got) != "hello" {
				t.Errorf("GetBlob(0) = %q, %v; want %q", got, err, "hello")
			}
			if got, err := ucc.GetBlob(1); err != nil || string(got) != "world!!" {
				t.Errorf("GetBlob(1) = %q, %v; want %q", got, err, "world!!")
			}
			if got, err := ucc.GetBlobSize(0); err != nil || got != 5 {
				t.Errorf("GetBlobSize(0) = %d, %v; want 5", got, err)
			}
			if _, err := ucc.GetBlob(2); err == nil {
				t.Errorf("GetBlob(2) error = nil, want non-nil")
			}
		})
	}
}

func TestUncompressedClusterCorruption(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if _, err := NewCluster(nil); err != CorruptData {
			t.Fatalf("NewCluster(nil) error = %v, want CorruptData", err)
		}
	})

	t.Run("info byte only", func(t *testing.T) {
		cr, err := NewCluster([]byte{byte(None)})
		if err != nil {
			t.Fatalf("NewCluster error = %v", err)
		}
		if _, err := cr.GetBlob(0); err != CorruptData {
			t.Fatalf("GetBlob(0) error = %v, want CorruptData", err)
		}
	})

	t.Run("offset table exceeds cluster size", func(t *testing.T) {
		// firstOffset claims 4 offsets (16 bytes) but only 2 are present.
		contents := []byte{byte(None), 16, 0, 0, 0}
		cr, err := NewCluster(contents)
		if err != nil {
			t.Fatalf("NewCluster error = %v", err)
		}
		if _, err := cr.GetBlob(0); err != CorruptData {
			t.Fatalf("GetBlob(0) error = %v, want CorruptData", err)
		}
	})
}

func TestNewClusterDispatch(t *testing.T) {
	if _, err := NewCluster([]byte{0x0f}); err != UnregisteredCompression {
		t.Errorf("NewCluster(0x0f) error = %v, want UnregisteredCompression", err)
	}
}

func compressWith(t *testing.T, codec Compression, raw []byte) []byte {
	t.Helper()

	var out bytes.Buffer
	switch codec {
	case Zlib:
		w := zlib.NewWriter(&out)
		if _, err := w.Write(raw); err != nil {
			t.Fatalf("zlib write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("zlib close: %v", err)
		}
	case Zstd:
		w, err := zstd.NewWriter(&out)
		if err != nil {
			t.Fatalf("zstd writer: %v", err)
		}
		if _, err := w.Write(raw); err != nil {
			t.Fatalf("zstd write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("zstd close: %v", err)
		}
	case Lzma2:
		w, err := xz.NewWriter(&out)
		if err != nil {
			t.Fatalf("xz writer: %v", err)
		}
		if _, err := w.Write(raw); err != nil {
			t.Fatalf("xz write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("xz close: %v", err)
		}
	default:
		t.Fatalf("unsupported codec %v", codec)
	}
	return out.Bytes()
}

func buildCompressedCluster(t *testing.T, codec Compression, blobs [][]byte) []byte {
	t.Helper()

	offsetSize := uint64(Normal)
	nOffsets := len(blobs) + 1
	base := uint64(nOffsets) * offsetSize
	offsets := make([]uint64, nOffsets)
	offsets[0] = base
	for i, b := range blobs {
		offsets[i+1] = offsets[i] + uint64(len(b))
	}

	var raw bytes.Buffer
	for _, o := range offsets {
		_ = binary.Write(&raw, binary.LittleEndian, uint32(o))
	}
	for _, b := range blobs {
		raw.Write(b)
	}

	compressed := compressWith(t, codec, raw.Bytes())
	out := make([]byte, 0, 1+len(compressed))
	out = append(out, byte(codec))
	out = append(out, compressed...)
	return out
}

func TestCompressedCluster(t *testing.T) {
	for _, codec := range []Compression{Zlib, Zstd, Lzma2} {
		t.Run(fmt.Sprintf("%d", codec), func(t *testing.T) {
			blobs := [][]byte{[]byte("hello"), []byte("world!!")}
			cr, err := NewCluster(buildCompressedCluster(t, codec, blobs))
			if err != nil {
				t.Fatalf("NewCluster error = %v", err)
			}
			cc, ok := cr.(*CompressedCluster)
			if !ok {
				t.Fatalf("type = %T, want *CompressedCluster", cr)
			}
			cc.cache = newClusterCache(1 << 20)

			if got, err := cc.GetBlob(0); err != nil || string(got) != "hello" {
				t.Errorf("GetBlob(0) = %q, %v; want %q", got, err, "hello")
			}
			if got, err := cc.GetBlob(1); err != nil || string(got) != "world!!" {
				t.Errorf("GetBlob(1) = %q, %v; want %q", got, err, "world!!")
			}
			if got, err := cc.GetBlobSize(0); err != nil || got != 5 {
				t.Errorf("GetBlobSize(0) = %d, %v; want 5", got, err)
			}
			if got, err := cc.GetBlobSize(1); err != nil || got != 7 {
				t.Errorf("GetBlobSize(1) = %d, %v; want 7", got, err)
			}
			if _, err := cc.GetBlob(2); err == nil {
				t.Errorf("GetBlob(2) error = nil, want non-nil")
			}
		})
	}
}

func TestClusterCacheEviction(t *testing.T) {
	c := newClusterCache(80)

	entry := func(size int) clusterData {
		return clusterData{offsets: []uint64{0, 4}, blobs: make([]byte, size)}
	}

	// Each entry is len(offsets)*8 + len(blobs) = 16 + 14 = 30 bytes.
	c.put(1, entry(14))
	c.put(2, entry(14))
	c.put(3, entry(14)) // 90 bytes > 80, evicts cluster 1

	if _, ok := c.get(1); ok {
		t.Errorf("cluster 1 should have been evicted")
	}
	if _, ok := c.get(3); !ok {
		t.Errorf("cluster 3 should still be present")
	}
	if _, ok := c.get(2); !ok {
		t.Errorf("cluster 2 should still be present")
	}
}

func TestClusterCacheReplace(t *testing.T) {
	c := newClusterCache(1 << 20)
	c.put(1, clusterData{offsets: []uint64{0, 2}, blobs: []byte("ab")})
	c.put(1, clusterData{offsets: []uint64{0, 1}, blobs: []byte("c")})

	data, ok := c.get(1)
	if !ok {
		t.Fatal("cluster 1 missing")
	}
	if string(data.blobs) != "c" {
		t.Errorf("blobs = %q, want %q", data.blobs, "c")
	}
}

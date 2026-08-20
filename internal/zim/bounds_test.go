package zim

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestFits(t *testing.T) {
	tests := []struct {
		name              string
		offset, n, length uint64
		want              bool
	}{
		{"empty range is always in bounds", 0, 0, 0, true},
		{"zero length excludes any non-empty range", 0, 1, 0, false},
		{"exact fit", 0, 1, 1, true},
		{"range past end", 0, 2, 1, false},
		{"offset past end", 2, 0, 1, false},
		{"interior fit", 5, 5, 10, true},
		{"interior overflow", 6, 5, 10, false},
		// The addition offset+n must not wrap around.
		{"near MaxUint64 offset", math.MaxUint64 - 1, 5, math.MaxUint64, false},
		{"MaxUint64 offset with zero length", math.MaxUint64, 0, math.MaxUint64, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := fits(tc.offset, tc.n, tc.length); got != tc.want {
				t.Errorf("fits(%d, %d, %d) = %v, want %v", tc.offset, tc.n, tc.length, got, tc.want)
			}
		})
	}
}

func TestPtrListEnd(t *testing.T) {
	tests := []struct {
		name  string
		start uint64
		count uint32
		want  uint64
		ok    bool
	}{
		{"empty list", 0, 0, 0, true},
		{"three pointers", 0, 3, 24, true},
		{"offset base", 10, 2, 26, true},
		{"overflow", math.MaxUint64 - 7, 2, 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ptrListEnd(tc.start, tc.count)
			if got != tc.want || ok != tc.ok {
				t.Errorf("ptrListEnd(%d, %d) = %d, %v; want %d, %v", tc.start, tc.count, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestReadUintHelpers(t *testing.T) {
	buf := make([]byte, 16)
	binary.LittleEndian.PutUint16(buf[1:3], 0x1234)
	binary.LittleEndian.PutUint32(buf[4:8], 0xdeadbeef)
	binary.LittleEndian.PutUint64(buf[8:16], 0x0123456789abcdef)

	if got := readUint16(buf, 1); got != 0x1234 {
		t.Errorf("readUint16 = %#x, want 0x1234", got)
	}
	if got := readUint32(buf, 4); got != 0xdeadbeef {
		t.Errorf("readUint32 = %#x, want 0xdeadbeef", got)
	}
	if got := readUint64(buf, 8); got != 0x0123456789abcdef {
		t.Errorf("readUint64 = %#x, want 0x0123456789abcdef", got)
	}
}

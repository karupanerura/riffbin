package riffbin_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/karupanerura/riffbin"
)

var fuzzSeeds = [][]byte{
	{},
	[]byte("RIF"),
	[]byte("LIFF"),
	{'R', 'I', 'F', 'F', 0x04, 0x00, 0x00},
	{'R', 'I', 'F', 'F', 0x04, 0x00, 0x00, 0x00, 'X', 'X', 'X'},
	{'R', 'I', 'F', 'F', 0x01, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D'},
	{'R', 'I', 'F', 'F', 0x07, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x00, 0x00, 0x00, 0x00},
	{'R', 'I', 'F', 'F', 0x09, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x00, 0x00, 0x00, 0x00},
	{'R', 'I', 'F', 'F', 0x08, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x01, 0x00, 0x00, 0x00},
	{'R', 'I', 'F', 'F', 0x09, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x02, 0x00, 0x00, 0x00, 'A', 'B'},
	{'R', 'I', 'F', 'F', 0x0A, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x01, 0x00, 0x00, 0x00, 'A', 'B'},
	// a LIST holding a non-empty sub-chunk, followed by another chunk
	{
		'R', 'I', 'F', 'F', 0x24, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
		'L', 'I', 'S', 'T', 0x10, 0x00, 0x00, 0x00, 'L', 'S', 'T', '1',
		'E', 'N', 'T', '1', 0x04, 0x00, 0x00, 0x00, 'a', 'b', 'c', 'd',
		'E', 'N', 'T', '2', 0x04, 0x00, 0x00, 0x00, 'w', 'x', 'y', 'z',
	},
	// a nested RIFF chunk, which the specification does not allow
	{
		'R', 'I', 'F', 'F', 0x14, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
		'R', 'I', 'F', 'F', 0x04, 0x00, 0x00, 0x00, 'N', 'E', 'S', 'T',
	},
	// an odd-sized chunk with its pad byte
	{
		'R', 'I', 'F', 'F', 0x10, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
		'E', 'N', 'T', '1', 0x03, 0x00, 0x00, 0x00, 'a', 'b', 'c', 0x00,
	},
	// big-endian RIFX
	{
		'R', 'I', 'F', 'X', 0x00, 0x00, 0x00, 0x10, 'T', 'E', 'S', 'T',
		'E', 'N', 'T', '1', 0x00, 0x00, 0x00, 0x03, 'a', 'b', 'c', 0x00,
	},
	// unsupported containers
	{'R', 'F', '6', '4', 0x04, 0x00, 0x00, 0x00, 'W', 'A', 'V', 'E'},
	{'B', 'W', '6', '4', 0x04, 0x00, 0x00, 0x00, 'W', 'A', 'V', 'E'},
}

// checkRoundTrip verifies that a tree that could be read is also writable, and that
// reading the bytes written back yields the very same tree.
func checkRoundTrip(t *testing.T, in []byte, chunk *riffbin.RIFFChunk) {
	t.Helper()

	var buf bytes.Buffer
	if _, err := riffbin.NewCompletedChunkWriter(&buf).WriteChunk(chunk); err != nil {
		t.Log(hex.Dump(in))
		t.Fatalf("write back: %v", err)
	}

	again, err := riffbin.ReadFull(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Log(hex.Dump(in))
		t.Log(hex.Dump(buf.Bytes()))
		t.Fatalf("read back: %v", err)
	}

	if df := cmp.Diff(flattenTree(t, chunk), flattenTree(t, again)); df != "" {
		t.Log(hex.Dump(in))
		t.Fatalf("round trip differs: %s", df)
	}
}

func FuzzReadFull(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := riffbin.ReadFull(bytes.NewReader(b))
		if (c == nil) == (err == nil) {
			t.Log(hex.Dump(b))
			t.Fatal("invalid result")
		}
		if c != nil {
			checkRoundTrip(t, b, c)
		}
	})
}

func FuzzReadSections(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := riffbin.ReadSections(bytes.NewReader(b))
		if (c == nil) == (err == nil) {
			t.Log(hex.Dump(b))
			t.Fatal("invalid result")
		}
		if c != nil {
			checkRoundTrip(t, b, c)
		}
	})
}

func FuzzReadFullLenient(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := riffbin.ReadFull(bytes.NewReader(b), riffbin.AllowUnpaddedChunks(), riffbin.AllowTrailingData())
		if (c == nil) == (err == nil) {
			t.Log(hex.Dump(b))
			t.Fatal("invalid result")
		}
	})
}

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
		'R', 'I', 'F', 'F', 0x28, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
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
	// an odd-sized chunk whose pad byte holds garbage
	{
		'R', 'I', 'F', 'F', 0x10, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
		'E', 'N', 'T', '1', 0x03, 0x00, 0x00, 0x00, 'a', 'b', 'c', 0xFF,
	},
	// big-endian RIFX
	{
		'R', 'I', 'F', 'X', 0x00, 0x00, 0x00, 0x10, 'T', 'E', 'S', 'T',
		'E', 'N', 'T', '1', 0x00, 0x00, 0x00, 0x03, 'a', 'b', 'c', 0x00,
	},
	// big-endian RIFX without the pad byte after an odd-sized chunk
	{
		'R', 'I', 'F', 'X', 0x00, 0x00, 0x00, 0x1B, 'T', 'E', 'S', 'T',
		'E', 'N', 'T', '1', 0x00, 0x00, 0x00, 0x03, 'a', 'b', 'c',
		'E', 'N', 'T', '2', 0x00, 0x00, 0x00, 0x04, 'w', 'x', 'y', 'z',
	},
	// an odd-sized chunk inside an odd-sized LIST, both without pad bytes
	{
		'R', 'I', 'F', 'F', 0x27, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
		'L', 'I', 'S', 'T', 0x0F, 0x00, 0x00, 0x00, 'L', 'S', 'T', '1',
		'E', 'N', 'T', '1', 0x03, 0x00, 0x00, 0x00, 'a', 'b', 'c',
		'E', 'N', 'T', '2', 0x04, 0x00, 0x00, 0x00, 'w', 'x', 'y', 'z',
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
		// a leniently-read tree must still write back as a compliant file that
		// re-reads strictly to the same tree
		if c != nil {
			checkRoundTrip(t, b, c)
		}
	})
}

// Whatever the input, ReadFull and ReadSections must agree: both accept or both
// reject, and on success they yield the same tree. This pins the two readers to a
// single definition of the format, in the strict and the lenient mode alike.
func FuzzReadersAgree(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		for _, mode := range []struct {
			name string
			opts []riffbin.ReaderOption
		}{
			{name: "strict"},
			{name: "lenient", opts: []riffbin.ReaderOption{riffbin.AllowUnpaddedChunks(), riffbin.AllowTrailingData()}},
		} {
			full, fullErr := riffbin.ReadFull(bytes.NewReader(b), mode.opts...)
			sections, sectionsErr := riffbin.ReadSections(bytes.NewReader(b), mode.opts...)
			if (fullErr == nil) != (sectionsErr == nil) {
				t.Log(hex.Dump(b))
				t.Fatalf("%s: the readers disagree: ReadFull=%v ReadSections=%v", mode.name, fullErr, sectionsErr)
			}
			if fullErr == nil {
				if df := cmp.Diff(flattenTree(t, full), flattenTree(t, sections)); df != "" {
					t.Log(hex.Dump(b))
					t.Fatalf("%s: the readers disagree on the tree: %s", mode.name, df)
				}
			}
		}
	})
}

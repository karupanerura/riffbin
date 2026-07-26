package riffbin_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/karupanerura/riffbin"
)

// RIFX is RIFF with big-endian size fields. Four-character codes keep their
// left-to-right order in both variants.
var rifxFileBytes = []byte{
	0x52, 0x49, 0x46, 0x58, // id (RIFX)
	0x00, 0x00, 0x00, 0x1C, // body size (4 + 8 + 3 + 1 + 8 + 4), big endian
	0x54, 0x45, 0x53, 0x54, // type (TEST)
	0x45, 0x4E, 0x54, 0x31, // id (ENT1)
	0x00, 0x00, 0x00, 0x03, // body size, big endian
	0x61, 0x62, 0x63, // "abc"
	0x00,                   // padding
	0x45, 0x4E, 0x54, 0x32, // id (ENT2)
	0x00, 0x00, 0x00, 0x04, // body size, big endian
	0x77, 0x78, 0x79, 0x7A, // "wxyz"
}

func rifxFileChunk() *riffbin.RIFFChunk {
	return &riffbin.RIFFChunk{
		ByteOrder: riffbin.BigEndian,
		FormType:  riffbin.MustFourCC("TEST"),
		Payload: []riffbin.Chunk{
			&riffbin.OnMemorySubChunk{ID: riffbin.MustFourCC("ENT1"), Payload: []byte("abc")},
			&riffbin.OnMemorySubChunk{ID: riffbin.MustFourCC("ENT2"), Payload: []byte("wxyz")},
		},
	}
}

func TestRIFXReadFull(t *testing.T) {
	t.Parallel()

	got, err := riffbin.ReadFull(bytes.NewReader(rifxFileBytes))
	if err != nil {
		t.Fatal(err)
	}
	if df := cmp.Diff(rifxFileChunk(), got); df != "" {
		t.Errorf("diff = %s", df)
	}
}

func TestRIFXReadSections(t *testing.T) {
	t.Parallel()

	got, err := riffbin.ReadSections(bytes.NewReader(rifxFileBytes))
	if err != nil {
		t.Fatal(err)
	}
	if got.ByteOrder != riffbin.BigEndian {
		t.Errorf("byte order should be BigEndian but got: %s", got.ByteOrder)
	}
	if df := cmp.Diff(flattenTree(t, rifxFileChunk()), flattenTree(t, got)); df != "" {
		t.Errorf("diff = %s", df)
	}
}

func TestRIFXWrite(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	n, err := riffbin.NewCompletedChunkWriter(&buf).WriteChunk(rifxFileChunk())
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(buf.Len()) {
		t.Errorf("n should be %d but got %d", buf.Len(), n)
	}
	if df := cmp.Diff(rifxFileBytes, buf.Bytes()); df != "" {
		t.Errorf("unexpected bytes are written: %s", df)
	}
}

// The same tree in either byte order must round-trip to the same bytes it came from.
func TestRIFXRoundTrip(t *testing.T) {
	t.Parallel()

	for _, byteOrder := range []riffbin.ByteOrder{riffbin.LittleEndian, riffbin.BigEndian} {
		byteOrder := byteOrder
		t.Run(byteOrder.String(), func(t *testing.T) {
			t.Parallel()

			chunk := nestedListTree()
			chunk.ByteOrder = byteOrder

			var buf bytes.Buffer
			if _, err := riffbin.NewCompletedChunkWriter(&buf).WriteChunk(chunk); err != nil {
				t.Fatal(err)
			}
			original := buf.Bytes()

			got, err := riffbin.ReadFull(bytes.NewReader(original))
			if err != nil {
				t.Fatal(err)
			}
			if got.ByteOrder != byteOrder {
				t.Errorf("byte order should be %s but got: %s", byteOrder, got.ByteOrder)
			}

			var out bytes.Buffer
			if _, err := riffbin.NewCompletedChunkWriter(&out).WriteChunk(got); err != nil {
				t.Fatal(err)
			}
			if df := cmp.Diff(original, out.Bytes()); df != "" {
				t.Errorf("round trip differs: %s", df)
			}
		})
	}
}

// A RIFX file read as if it were little-endian would report an absurd chunk size.
func TestRIFXSizeIsNotMisread(t *testing.T) {
	t.Parallel()

	littleEndian := append([]byte{}, rifxFileBytes...)
	copy(littleEndian, "RIFF")

	if _, err := riffbin.ReadFull(bytes.NewReader(littleEndian)); !errors.Is(err, riffbin.ErrInvalidFormat) {
		t.Errorf("should be ErrInvalidFormat but got: %v", err)
	}
}

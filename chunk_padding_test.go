package riffbin_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/karupanerura/riffbin"
)

var paddedFileBytes = []byte{
	0x52, 0x49, 0x46, 0x46, // id (RIFF)
	0x1C, 0x00, 0x00, 0x00, // body size (4 + 8 + 3 + 1 + 8 + 4)
	0x54, 0x45, 0x53, 0x54, // type (TEST)
	0x45, 0x4E, 0x54, 0x31, // id (ENT1)
	0x03, 0x00, 0x00, 0x00, // body size
	0x61, 0x62, 0x63, // "abc"
	0x00,                   // padding
	0x45, 0x4E, 0x54, 0x32, // id (ENT2)
	0x04, 0x00, 0x00, 0x00, // body size
	0x77, 0x78, 0x79, 0x7A, // "wxyz"
}

func paddedFileChunk() *riffbin.RIFFChunk {
	return &riffbin.RIFFChunk{
		FormType: [4]byte{'T', 'E', 'S', 'T'},
		Payload: []riffbin.Chunk{
			&riffbin.OnMemorySubChunk{
				ID:      [4]byte{'E', 'N', 'T', '1'},
				Payload: []byte("abc"),
			},
			&riffbin.OnMemorySubChunk{
				ID:      [4]byte{'E', 'N', 'T', '2'},
				Payload: []byte("wxyz"),
			},
		},
	}
}

func TestCompletedChunkWriterPadsOddChunk(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	n, err := riffbin.NewCompletedChunkWriter(&buf).WriteChunk(paddedFileChunk())
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(buf.Len()) {
		t.Errorf("n should be %d but got %d", buf.Len(), n)
	}
	if df := cmp.Diff(paddedFileBytes, buf.Bytes()); df != "" {
		t.Errorf("unexpected bytes are written: %s", df)
	}
}

func TestReadFullPadding(t *testing.T) {
	t.Parallel()
	t.Run("PaddedFile", func(t *testing.T) {
		t.Parallel()
		got, err := riffbin.ReadFull(bytes.NewReader(paddedFileBytes))
		if err != nil {
			t.Fatal(err)
		}
		if df := cmp.Diff(paddedFileChunk(), got); df != "" {
			t.Errorf("diff = %s", df)
		}
	})
	t.Run("LegacyUnpaddedFile", func(t *testing.T) {
		// riffbin up to v0.0.6 wrote no padding byte after odd-sized chunks.
		// Such files violate the specification, so they are rejected unless
		// AllowUnpaddedChunks is given.
		t.Parallel()
		legacy := []byte{
			0x52, 0x49, 0x46, 0x46, // id (RIFF)
			0x1B, 0x00, 0x00, 0x00, // body size (4 + 8 + 3 + 8 + 4)
			0x54, 0x45, 0x53, 0x54, // type (TEST)
			0x45, 0x4E, 0x54, 0x31, // id (ENT1)
			0x03, 0x00, 0x00, 0x00, // body size
			0x61, 0x62, 0x63, // "abc" (no padding)
			0x45, 0x4E, 0x54, 0x32, // id (ENT2)
			0x04, 0x00, 0x00, 0x00, // body size
			0x77, 0x78, 0x79, 0x7A, // "wxyz"
		}

		t.Run("Strict", func(t *testing.T) {
			t.Parallel()
			if _, err := riffbin.ReadFull(bytes.NewReader(legacy)); !errors.Is(err, riffbin.ErrInvalidFormat) {
				t.Errorf("should be ErrInvalidFormat but got: %v", err)
			}
		})
		t.Run("AllowUnpaddedChunks", func(t *testing.T) {
			t.Parallel()
			got, err := riffbin.ReadFull(bytes.NewReader(legacy), riffbin.AllowUnpaddedChunks())
			if err != nil {
				t.Fatal(err)
			}
			if df := cmp.Diff(paddedFileChunk(), got); df != "" {
				t.Errorf("diff = %s", df)
			}
		})
		t.Run("AllowUnpaddedChunksSections", func(t *testing.T) {
			t.Parallel()
			got, err := riffbin.ReadSections(bytes.NewReader(legacy), riffbin.AllowUnpaddedChunks())
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Payload) != 2 {
				t.Fatalf("should have 2 sub-chunks but got: %d", len(got.Payload))
			}
		})
	})
	t.Run("OddFinalChunkWithoutPadding", func(t *testing.T) {
		t.Parallel()
		b := []byte{
			0x52, 0x49, 0x46, 0x46, // id (RIFF)
			0x0F, 0x00, 0x00, 0x00, // body size (4 + 8 + 3)
			0x54, 0x45, 0x53, 0x54, // type (TEST)
			0x45, 0x4E, 0x54, 0x31, // id (ENT1)
			0x03, 0x00, 0x00, 0x00, // body size
			0x61, 0x62, 0x63, // "abc" (no padding at EOF)
		}

		got, err := riffbin.ReadFull(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Payload) != 1 {
			t.Fatalf("should have 1 sub-chunk but got: %d", len(got.Payload))
		}
	})
	t.Run("OddFinalChunkWithPaddingInsideSize", func(t *testing.T) {
		t.Parallel()
		b := []byte{
			0x52, 0x49, 0x46, 0x46, // id (RIFF)
			0x10, 0x00, 0x00, 0x00, // body size (4 + 8 + 3 + 1)
			0x54, 0x45, 0x53, 0x54, // type (TEST)
			0x45, 0x4E, 0x54, 0x31, // id (ENT1)
			0x03, 0x00, 0x00, 0x00, // body size
			0x61, 0x62, 0x63, // "abc"
			0x00, // padding
		}

		got, err := riffbin.ReadFull(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Payload) != 1 {
			t.Fatalf("should have 1 sub-chunk but got: %d", len(got.Payload))
		}
	})
	t.Run("OddFinalChunkWithPaddingOutsideSize", func(t *testing.T) {
		// some writers append the padding byte without counting it in the RIFF size
		t.Parallel()
		b := []byte{
			0x52, 0x49, 0x46, 0x46, // id (RIFF)
			0x0F, 0x00, 0x00, 0x00, // body size (4 + 8 + 3)
			0x54, 0x45, 0x53, 0x54, // type (TEST)
			0x45, 0x4E, 0x54, 0x31, // id (ENT1)
			0x03, 0x00, 0x00, 0x00, // body size
			0x61, 0x62, 0x63, // "abc"
			0x00, // padding outside of the RIFF chunk size
		}

		got, err := riffbin.ReadFull(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Payload) != 1 {
			t.Fatalf("should have 1 sub-chunk but got: %d", len(got.Payload))
		}
	})
	t.Run("TruncatedPadding", func(t *testing.T) {
		t.Parallel()
		b := []byte{
			0x52, 0x49, 0x46, 0x46, // id (RIFF)
			0x10, 0x00, 0x00, 0x00, // body size (4 + 8 + 3 + 1)
			0x54, 0x45, 0x53, 0x54, // type (TEST)
			0x45, 0x4E, 0x54, 0x31, // id (ENT1)
			0x03, 0x00, 0x00, 0x00, // body size
			0x61, 0x62, 0x63, // "abc" (the padding byte counted in the RIFF size is missing)
		}

		if _, err := riffbin.ReadFull(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrInvalidFormat) {
			t.Errorf("should be ErrInvalidFormat but got: %v", err)
		}
	})
	t.Run("StrayByteAfterOddChunk", func(t *testing.T) {
		t.Parallel()
		b := []byte{
			0x52, 0x49, 0x46, 0x46, // id (RIFF)
			0x10, 0x00, 0x00, 0x00, // body size (4 + 8 + 3 + 1)
			0x54, 0x45, 0x53, 0x54, // type (TEST)
			0x45, 0x4E, 0x54, 0x31, // id (ENT1)
			0x03, 0x00, 0x00, 0x00, // body size
			0x61, 0x62, 0x63, // "abc"
			0x41, // garbage where the padding or a next chunk header is expected
		}

		if _, err := riffbin.ReadFull(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrInvalidFormat) {
			t.Errorf("should be ErrInvalidFormat but got: %v", err)
		}
	})
	t.Run("PaddedChunkInsideList", func(t *testing.T) {
		t.Parallel()
		b := []byte{
			0x52, 0x49, 0x46, 0x46, // id (RIFF)
			0x1C, 0x00, 0x00, 0x00, // body size (4 + 8 + 16)
			0x54, 0x45, 0x53, 0x54, // type (TEST)
			0x4C, 0x49, 0x53, 0x54, // id (LIST)
			0x10, 0x00, 0x00, 0x00, // body size (4 + 8 + 3 + 1)
			0x4C, 0x53, 0x54, 0x31, // type (LST1)
			0x45, 0x4E, 0x54, 0x31, // id (ENT1)
			0x03, 0x00, 0x00, 0x00, // body size
			0x61, 0x62, 0x63, // "abc"
			0x00, // padding
		}

		got, err := riffbin.ReadFull(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}

		expected := &riffbin.RIFFChunk{
			FormType: [4]byte{'T', 'E', 'S', 'T'},
			Payload: []riffbin.Chunk{
				&riffbin.ListChunk{
					ListType: [4]byte{'L', 'S', 'T', '1'},
					Payload: []riffbin.Chunk{
						&riffbin.OnMemorySubChunk{
							ID:      [4]byte{'E', 'N', 'T', '1'},
							Payload: []byte("abc"),
						},
					},
				},
			},
		}
		if df := cmp.Diff(expected, got); df != "" {
			t.Errorf("diff = %s", df)
		}
	})
}

func TestReadSectionsPadding(t *testing.T) {
	t.Parallel()

	got, err := riffbin.ReadSections(bytes.NewReader(paddedFileBytes))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Payload) != 2 {
		t.Fatalf("should have 2 sub-chunks but got: %d", len(got.Payload))
	}

	for i, expected := range []string{"abc", "wxyz"} {
		subChunk, ok := got.Payload[i].(*riffbin.InStreamSubChunk)
		if !ok {
			t.Fatalf("payload[%d] should be *riffbin.InStreamSubChunk but got: %T", i, got.Payload[i])
		}

		body, err := io.ReadAll(subChunk)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != expected {
			t.Errorf("payload[%d] should be %q but got: %q", i, expected, body)
		}
	}
}

func TestRoundTripOddChunks(t *testing.T) {
	t.Parallel()

	riffChunk := &riffbin.RIFFChunk{
		FormType: [4]byte{'T', 'E', 'S', 'T'},
		Payload: []riffbin.Chunk{
			&riffbin.ListChunk{
				ListType: [4]byte{'L', 'S', 'T', '1'},
				Payload: []riffbin.Chunk{
					&riffbin.OnMemorySubChunk{
						ID:      [4]byte{'E', 'N', 'T', '1'},
						Payload: []byte{0x01},
					},
				},
			},
			&riffbin.OnMemorySubChunk{
				ID:      [4]byte{'E', 'N', 'T', '2'},
				Payload: []byte{0x01, 0x02, 0x03, 0x04, 0x05},
			},
			&riffbin.OnMemorySubChunk{
				ID:      [4]byte{'E', 'N', 'T', '3'},
				Payload: []byte{0x01, 0x02},
			},
		},
	}

	var buf bytes.Buffer
	if _, err := riffbin.NewCompletedChunkWriter(&buf).WriteChunk(riffChunk); err != nil {
		t.Fatal(err)
	}

	got, err := riffbin.ReadFull(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if df := cmp.Diff(riffChunk, got); df != "" {
		t.Errorf("diff = %s", df)
	}
}

func TestIncompleteChunkWriterPadsOddChunk(t *testing.T) {
	t.Parallel()

	// the size backfill offsets must account for padding bytes written before later chunks
	build := func() *riffbin.RIFFChunk {
		return &riffbin.RIFFChunk{
			FormType: [4]byte{'T', 'E', 'S', 'T'},
			Payload: []riffbin.Chunk{
				riffbin.NewIncompleteSubChunk([4]byte{'E', 'N', 'T', '1'}, strings.NewReader("abc")),
				&riffbin.OnMemorySubChunk{
					ID:      [4]byte{'E', 'N', 'T', '2'},
					Payload: []byte("wxyz"),
				},
			},
		}
	}

	t.Run("WriterAt", func(t *testing.T) {
		t.Parallel()
		f, err := os.CreateTemp("", "riffbin")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f.Name())

		w, err := riffbin.NewIncompleteChunkWriter(f)
		if err != nil {
			t.Fatal(err)
		}

		n, err := w.WriteChunk(build())
		if err != nil {
			t.Fatal(err)
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}

		got, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		if n != int64(len(got)) {
			t.Errorf("n should be %d but got %d", len(got), n)
		}
		if df := cmp.Diff(paddedFileBytes, got); df != "" {
			t.Errorf("unexpected bytes are written: %s", df)
		}
	})
	t.Run("Seek", func(t *testing.T) {
		t.Parallel()
		f, err := os.CreateTemp("", "riffbin")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f.Name())

		w, err := riffbin.NewIncompleteChunkWriter(&pureWriteSeeker{W: f})
		if err != nil {
			t.Fatal(err)
		}

		n, err := w.WriteChunk(build())
		if err != nil {
			t.Fatal(err)
		}

		// check seek position
		if pos, err := f.Seek(0, io.SeekCurrent); err != nil {
			t.Fatal(err)
		} else if pos != n {
			t.Errorf("unexpected seek position: %d", pos)
		}

		if err = f.Close(); err != nil {
			t.Fatal(err)
		}

		got, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		if n != int64(len(got)) {
			t.Errorf("n should be %d but got %d", len(got), n)
		}
		if df := cmp.Diff(paddedFileBytes, got); df != "" {
			t.Errorf("unexpected bytes are written: %s", df)
		}
	})
}

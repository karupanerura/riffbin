package riffbin_test

import (
	"bytes"
	"errors"
	"fmt"
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
			&riffbin.InMemorySubChunk{
				ID:      [4]byte{'E', 'N', 'T', '1'},
				Payload: []byte("abc"),
			},
			&riffbin.InMemorySubChunk{
				ID:      [4]byte{'E', 'N', 'T', '2'},
				Payload: []byte("wxyz"),
			},
		},
	}
}

// The policy names its own value, including the zero value and anything
// outside the three the package defines — a String that panics or returns ""
// on an unexpected value would only show up in an error message.
func TestPaddingPolicyString(t *testing.T) {
	t.Parallel()

	for policy, want := range map[riffbin.PaddingPolicy]string{
		riffbin.PadStrict:          "strict",
		riffbin.PadOmitted:         "omitted",
		riffbin.PadGarbage:         "garbage",
		riffbin.PaddingPolicy(200): "strict",
	} {
		if got := policy.String(); got != want {
			t.Errorf("PaddingPolicy(%d).String() = %q, want %q", uint8(policy), got, want)
		}
	}
}

func TestWriterPadsOddChunk(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	n, err := riffbin.NewWriter(&buf).WriteChunk(paddedFileChunk())
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

func TestReadAllPadding(t *testing.T) {
	t.Parallel()
	t.Run("PaddedFile", func(t *testing.T) {
		t.Parallel()
		got, err := riffbin.ReadAll(bytes.NewReader(paddedFileBytes))
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
		// PadOmitted is given.
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
			if _, err := riffbin.ReadAll(bytes.NewReader(legacy)); !errors.Is(err, riffbin.ErrInvalidFormat) {
				t.Errorf("should be ErrInvalidFormat but got: %v", err)
			}
		})
		t.Run("PadOmitted", func(t *testing.T) {
			t.Parallel()
			got, err := riffbin.ReadAll(bytes.NewReader(legacy), riffbin.PadOmitted)
			if err != nil {
				t.Fatal(err)
			}
			if df := cmp.Diff(paddedFileChunk(), got); df != "" {
				t.Errorf("diff = %s", df)
			}
		})
		t.Run("PadOmittedSections", func(t *testing.T) {
			t.Parallel()
			got, err := riffbin.ReadSections(bytes.NewReader(legacy), riffbin.PadOmitted)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Payload) != 2 {
				t.Fatalf("should have 2 sub-chunks but got: %d", len(got.Payload))
			}
		})
		t.Run("PadGarbage", func(t *testing.T) {
			// the garbage policy skips the byte at the pad position, which here
			// heads the next chunk header: this omission stays an error — see
			// TestOmittedPadIsAmbiguous for one that reads as a different tree
			t.Parallel()
			if _, err := riffbin.ReadAll(bytes.NewReader(legacy), riffbin.PadGarbage); !errors.Is(err, riffbin.ErrInvalidFormat) {
				t.Errorf("should be ErrInvalidFormat but got: %v", err)
			}
		})
	})
	t.Run("UnpaddedFileWithList", func(t *testing.T) {
		// in a file that omits pad bytes entirely, a LIST holding an odd-sized chunk
		// becomes odd-sized itself, so the byte where a pad byte belongs is the head
		// of the next chunk header — across nesting levels in both directions.
		t.Parallel()

		t.Run("NextHeaderOutsideTheList", func(t *testing.T) {
			// the odd chunk ends exactly at the end of the LIST; the missing pad byte
			// of the odd-sized LIST itself is followed by a sibling of the LIST
			t.Parallel()
			b := []byte{
				0x52, 0x49, 0x46, 0x46, // id (RIFF)
				0x27, 0x00, 0x00, 0x00, // body size (4 + 8 + 15 + 8 + 4), odd
				0x54, 0x45, 0x53, 0x54, // type (TEST)
				0x4C, 0x49, 0x53, 0x54, // id (LIST)
				0x0F, 0x00, 0x00, 0x00, // body size (4 + 8 + 3), odd
				0x4C, 0x53, 0x54, 0x31, // type (LST1)
				0x45, 0x4E, 0x54, 0x31, // id (ENT1)
				0x03, 0x00, 0x00, 0x00, // body size
				0x61, 0x62, 0x63, // "abc" (no padding)
				0x45, 0x4E, 0x54, 0x32, // id (ENT2), heading right where the LIST pad byte belongs
				0x04, 0x00, 0x00, 0x00, // body size
				0x77, 0x78, 0x79, 0x7A, // "wxyz"
			}
			expected := &riffbin.RIFFChunk{
				FormType: [4]byte{'T', 'E', 'S', 'T'},
				Payload: []riffbin.Chunk{
					&riffbin.ListChunk{
						ListType: [4]byte{'L', 'S', 'T', '1'},
						Payload: []riffbin.Chunk{
							&riffbin.InMemorySubChunk{ID: [4]byte{'E', 'N', 'T', '1'}, Payload: []byte("abc")},
						},
					},
					&riffbin.InMemorySubChunk{ID: [4]byte{'E', 'N', 'T', '2'}, Payload: []byte("wxyz")},
				},
			}

			if _, err := riffbin.ReadAll(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrInvalidFormat) {
				t.Errorf("strict should be ErrInvalidFormat but got: %v", err)
			}
			got, err := riffbin.ReadAll(bytes.NewReader(b), riffbin.PadOmitted)
			if err != nil {
				t.Fatal(err)
			}
			if df := cmp.Diff(expected, got); df != "" {
				t.Errorf("diff = %s", df)
			}
			sections, err := riffbin.ReadSections(bytes.NewReader(b), riffbin.PadOmitted)
			if err != nil {
				t.Fatal(err)
			}
			if df := cmp.Diff(flattenTree(t, expected), flattenTree(t, sections)); df != "" {
				t.Errorf("ReadSections: diff = %s", df)
			}
		})

		t.Run("NextHeaderInsideTheList", func(t *testing.T) {
			// the odd chunk is followed by a sibling inside the same LIST, and the
			// odd-sized LIST is the final chunk of the file
			t.Parallel()
			b := []byte{
				0x52, 0x49, 0x46, 0x46, // id (RIFF)
				0x25, 0x00, 0x00, 0x00, // body size (4 + 8 + 25), odd
				0x54, 0x45, 0x53, 0x54, // type (TEST)
				0x4C, 0x49, 0x53, 0x54, // id (LIST)
				0x19, 0x00, 0x00, 0x00, // body size (4 + 8 + 3 + 8 + 2), odd
				0x4C, 0x53, 0x54, 0x31, // type (LST1)
				0x45, 0x4E, 0x54, 0x31, // id (ENT1)
				0x03, 0x00, 0x00, 0x00, // body size
				0x61, 0x62, 0x63, // "abc" (no padding)
				0x45, 0x4E, 0x54, 0x32, // id (ENT2), heading right where the ENT1 pad byte belongs
				0x02, 0x00, 0x00, 0x00, // body size
				0x64, 0x65, // "de"
			}
			expected := &riffbin.RIFFChunk{
				FormType: [4]byte{'T', 'E', 'S', 'T'},
				Payload: []riffbin.Chunk{
					&riffbin.ListChunk{
						ListType: [4]byte{'L', 'S', 'T', '1'},
						Payload: []riffbin.Chunk{
							&riffbin.InMemorySubChunk{ID: [4]byte{'E', 'N', 'T', '1'}, Payload: []byte("abc")},
							&riffbin.InMemorySubChunk{ID: [4]byte{'E', 'N', 'T', '2'}, Payload: []byte("de")},
						},
					},
				},
			}

			if _, err := riffbin.ReadAll(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrInvalidFormat) {
				t.Errorf("strict should be ErrInvalidFormat but got: %v", err)
			}
			got, err := riffbin.ReadAll(bytes.NewReader(b), riffbin.PadOmitted)
			if err != nil {
				t.Fatal(err)
			}
			if df := cmp.Diff(expected, got); df != "" {
				t.Errorf("diff = %s", df)
			}
			sections, err := riffbin.ReadSections(bytes.NewReader(b), riffbin.PadOmitted)
			if err != nil {
				t.Fatal(err)
			}
			if df := cmp.Diff(flattenTree(t, expected), flattenTree(t, sections)); df != "" {
				t.Errorf("ReadSections: diff = %s", df)
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

		got, err := riffbin.ReadAll(bytes.NewReader(b))
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

		got, err := riffbin.ReadAll(bytes.NewReader(b))
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

		got, err := riffbin.ReadAll(bytes.NewReader(b))
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

		if _, err := riffbin.ReadAll(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrInvalidFormat) {
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

		if _, err := riffbin.ReadAll(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrInvalidFormat) {
			t.Errorf("should be ErrInvalidFormat but got: %v", err)
		}

		// under the omitted-padding policy a printable byte heads the next chunk
		// header, and no complete header follows it here: the file stays unreadable
		if _, err := riffbin.ReadAll(bytes.NewReader(b), riffbin.PadOmitted); !errors.Is(err, riffbin.ErrInvalidFormat) {
			t.Errorf("PadOmitted should still fail but got: %v", err)
		}

		// under the garbage-padding policy the byte is the pad byte, holding 'A'
		got, err := riffbin.ReadAll(bytes.NewReader(b), riffbin.PadGarbage)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Payload) != 1 {
			t.Fatalf("should have 1 sub-chunk but got: %d", len(got.Payload))
		}
	})
	t.Run("GarbagePadByte", func(t *testing.T) {
		// a pad byte holding garbage instead of 0x00. the specification requires the
		// writer to emit zero, so the strict mode reports it; the reference
		// implementations (x/image/riff, ffmpeg, libwebp) never inspect the pad value,
		// which is the behavior PadGarbage opts into.
		t.Parallel()
		b := []byte{
			0x52, 0x49, 0x46, 0x46, // id (RIFF)
			0x1C, 0x00, 0x00, 0x00, // body size (4 + 8 + 3 + 1 + 8 + 4)
			0x54, 0x45, 0x53, 0x54, // type (TEST)
			0x45, 0x4E, 0x54, 0x31, // id (ENT1)
			0x03, 0x00, 0x00, 0x00, // body size
			0x61, 0x62, 0x63, // "abc"
			0xFF,                   // pad byte holding garbage
			0x45, 0x4E, 0x54, 0x32, // id (ENT2)
			0x04, 0x00, 0x00, 0x00, // body size
			0x77, 0x78, 0x79, 0x7A, // "wxyz"
		}

		t.Run("Strict", func(t *testing.T) {
			t.Parallel()
			if _, err := riffbin.ReadAll(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrInvalidFormat) {
				t.Errorf("should be ErrInvalidFormat but got: %v", err)
			}
		})
		t.Run("PadGarbage", func(t *testing.T) {
			t.Parallel()
			got, err := riffbin.ReadAll(bytes.NewReader(b), riffbin.PadGarbage)
			if err != nil {
				t.Fatal(err)
			}
			if df := cmp.Diff(paddedFileChunk(), got); df != "" {
				t.Errorf("diff = %s", df)
			}
		})
		t.Run("PadGarbageSections", func(t *testing.T) {
			t.Parallel()
			got, err := riffbin.ReadSections(bytes.NewReader(b), riffbin.PadGarbage)
			if err != nil {
				t.Fatal(err)
			}
			if df := cmp.Diff(flattenTree(t, paddedFileChunk()), flattenTree(t, got)); df != "" {
				t.Errorf("diff = %s", df)
			}
		})
		t.Run("PadOmitted", func(t *testing.T) {
			// 0xFF is outside printable ASCII, so it cannot head a chunk header of
			// an unpadded file: under the omitted-padding policy it stays an error
			t.Parallel()
			if _, err := riffbin.ReadAll(bytes.NewReader(b), riffbin.PadOmitted); !errors.Is(err, riffbin.ErrInvalidFormat) {
				t.Errorf("should be ErrInvalidFormat but got: %v", err)
			}
		})
	})
	t.Run("TrailingZeroAfterEvenFinalChunk", func(t *testing.T) {
		// the single trailing 0x00 is tolerated only as the uncounted pad byte of an
		// odd-sized final chunk; after an even-sized one it is trailing garbage
		t.Parallel()
		b := append(append([]byte{}, paddedFileBytes...), 0x00)

		if _, err := riffbin.ReadAll(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrInvalidFormat) {
			t.Errorf("should be ErrInvalidFormat but got: %v", err)
		}
		if _, err := riffbin.ReadAll(bytes.NewReader(b), riffbin.AllowTrailingData()); err != nil {
			t.Errorf("AllowTrailingData should accept it but got: %v", err)
		}
	})
	t.Run("TwoTrailingZerosAfterOddFinalChunk", func(t *testing.T) {
		t.Parallel()
		b := []byte{
			0x52, 0x49, 0x46, 0x46, // id (RIFF)
			0x0F, 0x00, 0x00, 0x00, // body size (4 + 8 + 3)
			0x54, 0x45, 0x53, 0x54, // type (TEST)
			0x45, 0x4E, 0x54, 0x31, // id (ENT1)
			0x03, 0x00, 0x00, 0x00, // body size
			0x61, 0x62, 0x63, // "abc"
			0x00, 0x00, // the uncounted pad byte plus one stray zero
		}

		if _, err := riffbin.ReadAll(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrInvalidFormat) {
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

		got, err := riffbin.ReadAll(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}

		expected := &riffbin.RIFFChunk{
			FormType: [4]byte{'T', 'E', 'S', 'T'},
			Payload: []riffbin.Chunk{
				&riffbin.ListChunk{
					ListType: [4]byte{'L', 'S', 'T', '1'},
					Payload: []riffbin.Chunk{
						&riffbin.InMemorySubChunk{
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

// payloadShape renders the root's immediate children as "ID[size]" strings —
// the coarse shape the ambiguity tests below compare.
func payloadShape(t *testing.T, c *riffbin.RIFFChunk) []string {
	t.Helper()
	var out []string
	for _, p := range c.Payload {
		out = append(out, fmt.Sprintf("%s[%d]", p.ChunkID(), p.BodySize()))
	}
	return out
}

// A valid padded file except that ODD1's pad byte holds printable 'A' — the
// adversarial shape where the omitted-padding and the garbage-padding reading
// both form complete trees. The garbage policy reads the real chunks. The
// omitted policy must read 'A' as heading a chunk header, and here the bytes
// happen to form one — "AENT", whose size 49 is the '1' of "ENT1" — that
// swallows the real chunks exactly to the end of the root chunk. No reader
// can tell the interpretations apart, which is why the two policies are
// separate, mutually exclusive options instead of one heuristic: each is
// deterministic for the deviation it declares, and misreads only input that
// deviates the other way.
func TestPrintableGarbagePadIsAmbiguous(t *testing.T) {
	t.Parallel()

	b := []byte{
		'R', 'I', 'F', 'F', 70, 0x00, 0x00, 0x00, // body size (4 + 8+1+1 + 8+0 + 8+40)
		'T', 'E', 'S', 'T',
		'O', 'D', 'D', '1', 0x01, 0x00, 0x00, 0x00, 'x',
		'A',                                        // pad byte holding printable garbage
		'E', 'N', 'T', '1', 0x00, 0x00, 0x00, 0x00, // an empty chunk
		'D', 'A', 'T', 'A', 0x28, 0x00, 0x00, 0x00, // a 40 byte body
	}
	b = append(b, bytes.Repeat([]byte{'D'}, 40)...)

	t.Run("Strict", func(t *testing.T) {
		t.Parallel()
		if _, err := riffbin.ReadAll(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrInvalidFormat) {
			t.Errorf("should be ErrInvalidFormat but got: %v", err)
		}
	})
	t.Run("PadGarbage", func(t *testing.T) {
		t.Parallel()
		got, err := riffbin.ReadAll(bytes.NewReader(b), riffbin.PadGarbage)
		if err != nil {
			t.Fatal(err)
		}
		if df := cmp.Diff([]string{"ODD1[1]", "ENT1[0]", "DATA[40]"}, payloadShape(t, got)); df != "" {
			t.Errorf("diff = %s", df)
		}
	})
	t.Run("PadOmitted", func(t *testing.T) {
		// the documented sharp edge: declared as unpadded, the file reads as one
		t.Parallel()
		got, err := riffbin.ReadAll(bytes.NewReader(b), riffbin.PadOmitted)
		if err != nil {
			t.Fatal(err)
		}
		if df := cmp.Diff([]string{"ODD1[1]", "AENT[49]"}, payloadShape(t, got)); df != "" {
			t.Errorf("diff = %s", df)
		}
	})
}

// ambiguousUnpaddedFile is a valid unpadded file whose bytes also form a
// complete tree when the pad-position byte is skipped as garbage padding.
// It is also a fuzz seed: FuzzReadersAgree keeps the readers agreeing on it.
var ambiguousUnpaddedFile = append([]byte{
	'R', 'I', 'F', 'F', 86, 0x00, 0x00, 0x00, // body size (4 + 8+1 + 8+65, no pad bytes counted)
	'T', 'E', 'S', 'T',
	'O', 'D', 'D', '1', 0x01, 0x00, 0x00, 0x00, 'x', // odd body, its pad byte omitted
	'A', 'B', 'C', 'D', 0x41, 0x00, 0x00, 0x00, // the true next chunk, a 65 byte body:
	0x00,                                       // body[0] — the phantom "BCDA"'s fourth size byte, which must be zero
	'D', 'A', 'T', 'A', 0x38, 0x00, 0x00, 0x00, // body[1..8] — a phantom header that eats the rest
}, bytes.Repeat([]byte{'D'}, 56)...) // body[9..64]

// The mirror image of the ambiguity above: this time the file's actual
// deviation is the omitted pad byte, and it is the garbage policy that
// misreads. That policy skips the 'A' heading the true "ABCD" header as if
// it were a pad byte, and the shifted bytes happen to keep parsing — the
// phantom "BCDA" borrows the 0x41 of the size 65 as its fourth character,
// reads its size zero from body[0], and the phantom "DATA" header inside the
// true body swallows the rest exactly to the end of the root chunk. Neither
// policy is fail-closed against the other's deviation; each is deterministic
// only for the one it declares.
func TestOmittedPadIsAmbiguous(t *testing.T) {
	t.Parallel()

	t.Run("Strict", func(t *testing.T) {
		t.Parallel()
		if _, err := riffbin.ReadAll(bytes.NewReader(ambiguousUnpaddedFile)); !errors.Is(err, riffbin.ErrInvalidFormat) {
			t.Errorf("should be ErrInvalidFormat but got: %v", err)
		}
	})
	t.Run("PadOmitted", func(t *testing.T) {
		t.Parallel()
		got, err := riffbin.ReadAll(bytes.NewReader(ambiguousUnpaddedFile), riffbin.PadOmitted)
		if err != nil {
			t.Fatal(err)
		}
		if df := cmp.Diff([]string{"ODD1[1]", "ABCD[65]"}, payloadShape(t, got)); df != "" {
			t.Errorf("diff = %s", df)
		}
	})
	t.Run("PadGarbage", func(t *testing.T) {
		// the documented sharp edge: declared as garbage-padded, the unpadded
		// file reads as a different tree
		t.Parallel()
		got, err := riffbin.ReadAll(bytes.NewReader(ambiguousUnpaddedFile), riffbin.PadGarbage)
		if err != nil {
			t.Fatal(err)
		}
		if df := cmp.Diff([]string{"ODD1[1]", "BCDA[0]", "DATA[56]"}, payloadShape(t, got)); df != "" {
			t.Errorf("diff = %s", df)
		}
	})
}

// A single 0x00 after the root chunk is legitimate for two independent
// spec-level reasons: the root's own body size is odd — the specification pads
// every odd-sized chunk, the root included — or the final chunk's pad byte was
// left uncounted by every enclosing size. Either alone must suffice, whatever
// the order of the chunks: acceptance must not depend on which child happens
// to come last.
func TestTrailingRootPadByte(t *testing.T) {
	t.Parallel()

	// root body 23 (odd): "aaaa" omits its pad byte mid-file, "bbbb" is even.
	oddRootEvenFinal := []byte{
		'R', 'I', 'F', 'F', 0x17, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
		'a', 'a', 'a', 'a', 0x01, 0x00, 0x00, 0x00, 0xAA,
		'b', 'b', 'b', 'b', 0x02, 0x00, 0x00, 0x00, 0xBB, 0xBB,
		0x00, // the root chunk's own word-alignment pad byte
	}
	// the same two children swapped: root body still 23, "aaaa" now final and
	// its uncounted pad byte doubles as the root's.
	oddRootOddFinal := []byte{
		'R', 'I', 'F', 'F', 0x17, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
		'b', 'b', 'b', 'b', 0x02, 0x00, 0x00, 0x00, 0xBB, 0xBB,
		'a', 'a', 'a', 'a', 0x01, 0x00, 0x00, 0x00, 0xAA,
		0x00,
	}

	for name, b := range map[string][]byte{
		"OddRootEvenFinalChild": oddRootEvenFinal,
		"OddRootOddFinalChild":  oddRootOddFinal,
	} {
		name, b := name, b
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := riffbin.ReadAll(bytes.NewReader(b), riffbin.PadOmitted); err != nil {
				t.Errorf("ReadAll should accept the pad byte but got: %v", err)
			}
			if _, err := riffbin.ReadSections(bytes.NewReader(b), riffbin.PadOmitted); err != nil {
				t.Errorf("ReadSections should accept the pad byte but got: %v", err)
			}
			// the identical bytes as a concatenated stream: every entry point
			// must agree with the single-chunk readers
			stream := append(append([]byte{}, b...), b...)
			var count int
			for _, err := range riffbin.Concatenated(bytes.NewReader(stream), riffbin.PadOmitted) {
				if err != nil {
					t.Fatalf("chunk %d: %v", count, err)
				}
				count++
			}
			if count != 2 {
				t.Errorf("should yield 2 chunks but got: %d", count)
			}
		})
	}

	t.Run("StrictModeIsUnchanged", func(t *testing.T) {
		// without PadOmitted the mid-file omitted pad is rejected before the
		// trailing byte is ever reached
		t.Parallel()
		if _, err := riffbin.ReadAll(bytes.NewReader(oddRootEvenFinal)); !errors.Is(err, riffbin.ErrInvalidFormat) {
			t.Errorf("should be ErrInvalidFormat but got: %v", err)
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
		subChunk, ok := got.Payload[i].(*riffbin.SectionSubChunk)
		if !ok {
			t.Fatalf("payload[%d] should be *riffbin.SectionSubChunk but got: %T", i, got.Payload[i])
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
					&riffbin.InMemorySubChunk{
						ID:      [4]byte{'E', 'N', 'T', '1'},
						Payload: []byte{0x01},
					},
				},
			},
			&riffbin.InMemorySubChunk{
				ID:      [4]byte{'E', 'N', 'T', '2'},
				Payload: []byte{0x01, 0x02, 0x03, 0x04, 0x05},
			},
			&riffbin.InMemorySubChunk{
				ID:      [4]byte{'E', 'N', 'T', '3'},
				Payload: []byte{0x01, 0x02},
			},
		},
	}

	var buf bytes.Buffer
	if _, err := riffbin.NewWriter(&buf).WriteChunk(riffChunk); err != nil {
		t.Fatal(err)
	}

	got, err := riffbin.ReadAll(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if df := cmp.Diff(riffChunk, got); df != "" {
		t.Errorf("diff = %s", df)
	}
}

func TestStreamingWriterPadsOddChunk(t *testing.T) {
	t.Parallel()

	// the size backfill offsets must account for padding bytes written before later chunks
	build := func() *riffbin.RIFFChunk {
		return &riffbin.RIFFChunk{
			FormType: [4]byte{'T', 'E', 'S', 'T'},
			Payload: []riffbin.Chunk{
				riffbin.NewStreamingSubChunk([4]byte{'E', 'N', 'T', '1'}, strings.NewReader("abc")),
				&riffbin.InMemorySubChunk{
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

		w, err := riffbin.NewStreamingWriter(f)
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

		w, err := riffbin.NewStreamingWriter(&pureWriteSeeker{W: f})
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

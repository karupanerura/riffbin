package riffbin_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/karupanerura/riffbin"
)

// lyingWriteToReader writes data to the destination but returns claim from
// WriteTo — the shape of a buggy custom io.WriterTo, whose count io.Copy
// passes through as its own return value.
type lyingWriteToReader struct {
	data  string
	claim int64
}

func (r *lyingWriteToReader) Read(p []byte) (int, error) { return 0, io.EOF }

func (r *lyingWriteToReader) WriteTo(w io.Writer) (int64, error) {
	if _, err := io.WriteString(w, r.data); err != nil {
		return 0, err
	}
	return r.claim, nil
}

// lyingWriteToSubChunk is a non-streaming leaf whose body reader misreports
// its WriteTo count.
type lyingWriteToSubChunk struct {
	id       riffbin.FourCC
	declared int64
	body     *lyingWriteToReader
}

func (c *lyingWriteToSubChunk) ChunkID() riffbin.FourCC { return c.id }

func (c *lyingWriteToSubChunk) BodySize() int64 { return c.declared }

func (c *lyingWriteToSubChunk) Body() io.Reader { return c.body }

// A body whose WriteTo writes fewer bytes than declared but returns the
// declared count must fail on the bytes that actually arrived: trusting the
// claim would let the header promise bytes the file does not hold.
func TestWriterRejectsBodyClaimingDeclaredButWritingLess(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	n, err := riffbin.NewWriter(&buf).WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			&lyingWriteToSubChunk{
				id:       riffbin.MustParseFourCC("DAT1"),
				declared: 6,
				body:     &lyingWriteToReader{data: "abc", claim: 6},
			},
		},
	})
	if !errors.Is(err, riffbin.ErrSizeMismatch) {
		t.Errorf("should be ErrSizeMismatch but got: %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "produced 3") {
		t.Errorf("the error should report the 3 bytes measured, not the claimed count: %v", err)
	}
	// the root header (12), the sub-chunk header (8) and the 3 bytes the body
	// actually produced are out when the write stops
	if want := int64(12 + 8 + 3); n != want || int64(buf.Len()) != want {
		t.Errorf("n = %d, buf holds %d byte(s), want %d", n, buf.Len(), want)
	}
}

// A body that writes exactly its declared size succeeds no matter what count
// its WriteTo returns: the write is judged by the bytes that arrived.
func TestWriterTrustsMeasuredBytesOverClaimedCount(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if _, err := riffbin.NewWriter(&buf).WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			&lyingWriteToSubChunk{
				id:       riffbin.MustParseFourCC("DAT1"),
				declared: 4,
				body:     &lyingWriteToReader{data: "wxyz", claim: 999},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := riffbin.ReadAll(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("the written file does not parse: %v", err)
	}
	expected := flattenTree(t, &riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DAT1"), Payload: []byte("wxyz")},
		},
	})
	if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
		t.Errorf("diff = %s", df)
	}
}

// A streaming chunk is sized from the bytes its stream delivers, so a stream
// whose WriteTo misreports its count must not shift the size fields or the
// offsets they are backfilled at: the chunk after it stays intact.
func TestStreamingWriterSizesFromMeasuredBytes(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		data    string
		claim   int64
		padding int64
	}{
		{name: "ClaimsMore", data: "abc", claim: 100, padding: 1},
		{name: "ClaimsLess", data: "abcde", claim: 2, padding: 1},
		{name: "ClaimsZero", data: "wx", claim: 0, padding: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			streaming := riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DAT1"), &lyingWriteToReader{data: tt.data, claim: tt.claim})
			m := &memWriteSeeker{}
			w, err := riffbin.NewStreamingWriter(m)
			if err != nil {
				t.Fatal(err)
			}
			n, err := w.WriteChunk(&riffbin.RIFFChunk{
				FormType: riffbin.MustParseFourCC("TEST"),
				Payload: []riffbin.Chunk{
					streaming,
					&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("ENT2"), Payload: []byte("wxyz")},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if want := int64(12+8+8+4) + int64(len(tt.data)) + tt.padding; n != want {
				t.Errorf("n = %d, want %d", n, want)
			}
			if got, want := streaming.BodySize(), int64(len(tt.data)); got != want {
				t.Errorf("BodySize() = %d after draining, want the %d byte(s) measured", got, want)
			}

			got, err := riffbin.ReadAll(bytes.NewReader(m.buf))
			if err != nil {
				t.Fatalf("the written file does not parse: %v", err)
			}
			expected := flattenTree(t, &riffbin.RIFFChunk{
				FormType: riffbin.MustParseFourCC("TEST"),
				Payload: []riffbin.Chunk{
					&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DAT1"), Payload: []byte(tt.data)},
					&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("ENT2"), Payload: []byte("wxyz")},
				},
			})
			if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
				t.Errorf("diff = %s", df)
			}
		})
	}
}

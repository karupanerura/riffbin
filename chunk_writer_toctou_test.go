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

// encodedSize is the byte length a group body encodes to: the group type plus
// every child with its header and its word-alignment pad byte.
func encodedSize(children []riffbin.Chunk) int64 {
	size := int64(riffbin.TypeBytes)
	for _, c := range children {
		b := c.BodySize()
		size += riffbin.HeaderBytes + b + (b & 1)
	}
	return size
}

// mutatingGroupChunk hands out first on the first Children call and whatever
// later returns afterwards — the shape of a lazily regenerated or racily
// mutated tree.
type mutatingGroupChunk struct {
	id            riffbin.FourCC
	groupType     riffbin.FourCC
	first         []riffbin.Chunk
	later         func() []riffbin.Chunk
	childrenCalls int
}

func (c *mutatingGroupChunk) ChunkID() riffbin.FourCC { return c.id }

func (c *mutatingGroupChunk) GroupType() riffbin.FourCC { return c.groupType }

func (c *mutatingGroupChunk) BodySize() int64 { return encodedSize(c.first) }

func (c *mutatingGroupChunk) Children() []riffbin.Chunk {
	c.childrenCalls++
	if c.childrenCalls == 1 {
		return c.first
	}
	return c.later()
}

// The write works on a snapshot taken before the first byte goes out, so a
// Children that answers differently on a second call — here with the group
// itself, which would recurse forever — is never asked again: the tree the
// snapshot saw is the tree the file holds.
func TestWriterSnapshotsChildrenBeforeWriting(t *testing.T) {
	t.Parallel()

	build := func() (*mutatingGroupChunk, *riffbin.RIFFChunk) {
		group := &mutatingGroupChunk{
			id:        riffbin.MustParseFourCC("LIST"),
			groupType: riffbin.MustParseFourCC("TSTL"),
			first: []riffbin.Chunk{
				&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DAT1"), Payload: []byte("abc")},
			},
		}
		group.later = func() []riffbin.Chunk { return []riffbin.Chunk{group} }
		return group, &riffbin.RIFFChunk{
			FormType: riffbin.MustParseFourCC("TEST"),
			Payload:  []riffbin.Chunk{group},
		}
	}
	expected := flattenTree(t, &riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			&riffbin.ListChunk{
				ListType: riffbin.MustParseFourCC("TSTL"),
				Payload: []riffbin.Chunk{
					&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DAT1"), Payload: []byte("abc")},
				},
			},
		},
	})

	group, tree := build()
	var buf bytes.Buffer
	if _, err := riffbin.NewWriter(&buf).WriteChunk(tree); err != nil {
		t.Fatalf("Writer: %v", err)
	}
	if group.childrenCalls != 1 {
		t.Errorf("Writer read Children %d times, want exactly once", group.childrenCalls)
	}
	got, err := riffbin.ReadAll(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Writer output does not parse: %v", err)
	}
	if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
		t.Errorf("Writer: diff = %s", df)
	}

	group, tree = build()
	m := &memWriteSeeker{}
	w, err := riffbin.NewStreamingWriter(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.WriteChunk(tree); err != nil {
		t.Fatalf("StreamingWriter: %v", err)
	}
	if group.childrenCalls != 1 {
		t.Errorf("StreamingWriter read Children %d times, want exactly once", group.childrenCalls)
	}
	got, err = riffbin.ReadAll(bytes.NewReader(m.buf))
	if err != nil {
		t.Fatalf("StreamingWriter output does not parse: %v", err)
	}
	if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
		t.Errorf("StreamingWriter: diff = %s", df)
	}
}

// mutatingIDSubChunk answers its first ChunkID call with plan and any further
// call with later; its size and body stay stable.
type mutatingIDSubChunk struct {
	plan, later riffbin.FourCC
	payload     string
	idCalls     int
}

func (c *mutatingIDSubChunk) ChunkID() riffbin.FourCC {
	c.idCalls++
	if c.idCalls == 1 {
		return c.plan
	}
	return c.later
}

func (c *mutatingIDSubChunk) BodySize() int64 { return int64(len(c.payload)) }

func (c *mutatingIDSubChunk) Body() io.Reader { return strings.NewReader(c.payload) }

// The header carries the ID the snapshot read, exactly once: a later answer —
// here a structural ID, which would misparse the file — never reaches the
// output, because nothing asks again.
func TestWriterSnapshotsChunkIDBeforeWriting(t *testing.T) {
	t.Parallel()

	leaf := &mutatingIDSubChunk{
		plan:    riffbin.MustParseFourCC("DAT1"),
		later:   riffbin.MustParseFourCC("LIST"),
		payload: "wxyz",
	}
	var buf bytes.Buffer
	if _, err := riffbin.NewWriter(&buf).WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload:  []riffbin.Chunk{leaf},
	}); err != nil {
		t.Fatal(err)
	}
	if leaf.idCalls != 1 {
		t.Errorf("read ChunkID %d time(s), want exactly once", leaf.idCalls)
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

// The snapshot reads a leaf's BodySize exactly once — a group's size is
// derived from its children, never asked of the tree — so a BodySize that
// answers differently on any later call changes nothing: the header carries
// the planned size and the body is held to it.
func TestWriterReadsBodySizeExactlyOnce(t *testing.T) {
	t.Parallel()

	sized := &sizeOnlyMutatingSubChunk{id: riffbin.MustParseFourCC("DAT1"), planned: 4, body: "wxyz", planReads: 1}
	var buf bytes.Buffer
	if _, err := riffbin.NewWriter(&buf).WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{&riffbin.ListChunk{
			ListType: riffbin.MustParseFourCC("TSTL"),
			Payload:  []riffbin.Chunk{sized},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if sized.calls != 1 {
		t.Errorf("read BodySize %d time(s), want exactly once", sized.calls)
	}

	got, err := riffbin.ReadAll(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("the written file does not parse: %v", err)
	}
	expected := flattenTree(t, &riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{&riffbin.ListChunk{
			ListType: riffbin.MustParseFourCC("TSTL"),
			Payload: []riffbin.Chunk{
				&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DAT1"), Payload: []byte("wxyz")},
			},
		}},
	})
	if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
		t.Errorf("diff = %s", df)
	}
}

// A body producing a different number of bytes than the snapshot planned
// fails at that size, whatever BodySize answers once the snapshot is done:
// the header already carries the planned size, so the plan is what the body
// is held to.
func TestWriterHoldsBodyToSnapshottedSize(t *testing.T) {
	t.Parallel()

	// the single plan-time read answers 4; the body then produces only 2
	sized := &sizeOnlyMutatingSubChunk{id: riffbin.MustParseFourCC("DAT1"), planned: 4, body: "wx", planReads: 1}
	var buf bytes.Buffer
	_, err := riffbin.NewWriter(&buf).WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload:  []riffbin.Chunk{sized},
	})
	if !errors.Is(err, riffbin.ErrSizeMismatch) {
		t.Errorf("should be ErrSizeMismatch but got: %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "declares 4 bytes but produced 2") {
		t.Errorf("the error should hold the body to the snapshotted 4 bytes: %v", err)
	}
}

// sizeOnlyMutatingSubChunk reports planned from its first planReads BodySize
// calls and the actual body length afterwards.
type sizeOnlyMutatingSubChunk struct {
	id        riffbin.FourCC
	planned   int64
	body      string
	planReads int
	calls     int
}

func (c *sizeOnlyMutatingSubChunk) ChunkID() riffbin.FourCC { return c.id }

func (c *sizeOnlyMutatingSubChunk) BodySize() int64 {
	c.calls++
	if c.calls <= c.planReads {
		return c.planned
	}
	return int64(len(c.body))
}

func (c *sizeOnlyMutatingSubChunk) Body() io.Reader { return strings.NewReader(c.body) }

// sideEffectSubChunk runs effect when its body is requested — the only
// moment the write pass calls back into caller code.
type sideEffectSubChunk struct {
	id      riffbin.FourCC
	payload string
	effect  func()
}

func (c *sideEffectSubChunk) ChunkID() riffbin.FourCC { return c.id }

func (c *sideEffectSubChunk) BodySize() int64 { return int64(len(c.payload)) }

func (c *sideEffectSubChunk) Body() io.Reader {
	c.effect()
	return strings.NewReader(c.payload)
}

// swappableStreamingChunk embeds a *StreamingSubChunk that the test swaps
// mid-write: the stream captured at plan time must be the one drained, or
// the duplicate and consumed checks would have judged a different stream
// than the write uses.
type swappableStreamingChunk struct {
	*riffbin.StreamingSubChunk
}

// The plan captures the streaming body itself, so an embedder swapping its
// embedded chunk between planning and writing changes nothing: the stream
// the checks saw is the stream the write drains, and the replacement stays
// fresh for a later write.
func TestStreamingWriterDrainsStreamCapturedAtPlanTime(t *testing.T) {
	t.Parallel()

	original := riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DAT2"), strings.NewReader("original"))
	replacement := riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DAT2"), strings.NewReader("replacement"))
	swapper := &swappableStreamingChunk{StreamingSubChunk: original}

	m := &memWriteSeeker{}
	w, err := riffbin.NewStreamingWriter(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			&sideEffectSubChunk{id: riffbin.MustParseFourCC("ENT1"), payload: "xx", effect: func() {
				swapper.StreamingSubChunk = replacement
			}},
			swapper,
		},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := riffbin.ReadAll(bytes.NewReader(m.buf))
	if err != nil {
		t.Fatalf("the written file does not parse: %v", err)
	}
	expected := flattenTree(t, &riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("ENT1"), Payload: []byte("xx")},
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DAT2"), Payload: []byte("original")},
		},
	})
	if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
		t.Errorf("diff = %s", df)
	}
	if got := replacement.BodySize(); got != 0 {
		t.Errorf("the replacement stream was consumed: BodySize() = %d, want 0", got)
	}
}

// A group whose planned children do not fit the 32-bit size field fails
// before a single byte is written, even though every child fits on its own.
func TestWriterRejectsOversizedGroupSumBeforeWriting(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	n, err := riffbin.NewWriter(&buf).WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			&oversizedSubChunk{id: riffbin.MustParseFourCC("ENT1"), size: 3 << 30},
			&oversizedSubChunk{id: riffbin.MustParseFourCC("ENT2"), size: 3 << 30},
		},
	})
	if !errors.Is(err, riffbin.ErrChunkTooLarge) {
		t.Errorf("should be ErrChunkTooLarge but got: %v", err)
	}
	if n != 0 || buf.Len() != 0 {
		t.Errorf("wrote %d byte(s) before failing", buf.Len())
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

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

// memWriteSeeker is an in-memory io.WriteSeeker without io.WriterAt, with optional
// write-failure injection to exercise the backfill error paths.
type memWriteSeeker struct {
	buf       []byte
	pos       int64
	failWrite func(pos int64) error
}

func (m *memWriteSeeker) Write(p []byte) (int, error) {
	if m.failWrite != nil {
		if err := m.failWrite(m.pos); err != nil {
			return 0, err
		}
	}
	end := m.pos + int64(len(p))
	if end > int64(len(m.buf)) {
		nb := make([]byte, end)
		copy(nb, m.buf)
		m.buf = nb
	}
	copy(m.buf[m.pos:end], p)
	m.pos = end
	return len(p), nil
}

func (m *memWriteSeeker) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = m.pos + offset
	case io.SeekEnd:
		abs = int64(len(m.buf)) + offset
	}
	if abs < 0 {
		return 0, errors.New("negative position")
	}
	m.pos = abs
	return abs, nil
}

// failingWriterAt adds an io.WriterAt whose writes always fail.
type failingWriterAt struct {
	memWriteSeeker
}

func (f *failingWriterAt) WriteAt(p []byte, off int64) (int, error) {
	return 0, errors.New("injected WriteAt failure")
}

func buildStreamingTree(payload string) *riffbin.RIFFChunk {
	return &riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("ENT1"), strings.NewReader(payload)),
		},
	}
}

// The writers must reject, before writing anything, a tree that the readers would
// not read back as the same structure.
func TestWriterValidatesTreeBeforeWriting(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		tree *riffbin.RIFFChunk
	}{
		{"NonASCIIChunkID", &riffbin.RIFFChunk{
			FormType: riffbin.MustParseFourCC("TEST"),
			Payload:  []riffbin.Chunk{&riffbin.InMemorySubChunk{ID: riffbin.FourCC{0x01, 0x02, 0x03, 0x04}, Payload: []byte("xy")}},
		}},
		{"NonASCIIFormType", &riffbin.RIFFChunk{
			FormType: riffbin.FourCC{0xFF, 'A', 'B', 'C'},
		}},
		{"NonASCIIListType", &riffbin.RIFFChunk{
			FormType: riffbin.MustParseFourCC("TEST"),
			Payload:  []riffbin.Chunk{&riffbin.ListChunk{ListType: riffbin.FourCC{0xFF, 'A', 'B', 'C'}}},
		}},
		{"SubChunkWithLISTID", &riffbin.RIFFChunk{
			FormType: riffbin.MustParseFourCC("TEST"),
			Payload:  []riffbin.Chunk{&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("LIST"), Payload: []byte("ab")}},
		}},
		{"SubChunkWithRIFFID", &riffbin.RIFFChunk{
			FormType: riffbin.MustParseFourCC("TEST"),
			Payload:  []riffbin.Chunk{&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("RIFF"), Payload: []byte("abcd")}},
		}},
		{"NestedRIFFChunk", &riffbin.RIFFChunk{
			FormType: riffbin.MustParseFourCC("TEST"),
			Payload:  []riffbin.Chunk{&riffbin.RIFFChunk{FormType: riffbin.MustParseFourCC("NEST")}},
		}},
		{"NestedRIFXChunk", &riffbin.RIFFChunk{
			FormType: riffbin.MustParseFourCC("TEST"),
			Payload: []riffbin.Chunk{&riffbin.RIFFChunk{
				ByteOrder: riffbin.BigEndian,
				FormType:  riffbin.MustParseFourCC("NEST"),
			}},
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			n, err := riffbin.NewWriter(&buf).WriteChunk(tc.tree)
			if !errors.Is(err, riffbin.ErrUnwritableChunk) {
				t.Errorf("Writer: should be ErrUnwritableChunk but got: %v", err)
			}
			if n != 0 || buf.Len() != 0 {
				t.Errorf("Writer: wrote %d byte(s) before failing", buf.Len())
			}

			m := &memWriteSeeker{}
			w, err := riffbin.NewStreamingWriter(m)
			if err != nil {
				t.Fatal(err)
			}
			n, err = w.WriteChunk(tc.tree)
			if !errors.Is(err, riffbin.ErrUnwritableChunk) {
				t.Errorf("StreamingWriter: should be ErrUnwritableChunk but got: %v", err)
			}
			if n != 0 || len(m.buf) != 0 {
				t.Errorf("StreamingWriter: wrote %d byte(s) before failing", len(m.buf))
			}
		})
	}
}

// The readers refuse chunks nested deeper than 100 levels, so the writers refuse
// to produce such a file — with an error, not a stack overflow: validation and
// size computation recurse per level, and a hand-built tree can nest arbitrarily.
func TestWriterRejectsTooDeepNesting(t *testing.T) {
	t.Parallel()

	deepTree := func(groups int) *riffbin.RIFFChunk {
		var c riffbin.Chunk = &riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DATA"), Payload: []byte("ab")}
		for i := 0; i < groups-1; i++ {
			c = &riffbin.ListChunk{ListType: riffbin.MustParseFourCC("DEEP"), Payload: []riffbin.Chunk{c}}
		}
		return &riffbin.RIFFChunk{FormType: riffbin.MustParseFourCC("TEST"), Payload: []riffbin.Chunk{c}}
	}

	t.Run("OneTooDeep", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		n, err := riffbin.NewWriter(&buf).WriteChunk(deepTree(101))
		if !errors.Is(err, riffbin.ErrUnwritableChunk) {
			t.Errorf("Writer: should be ErrUnwritableChunk but got: %v", err)
		}
		if n != 0 || buf.Len() != 0 {
			t.Errorf("Writer: wrote %d byte(s) before failing", buf.Len())
		}

		m := &memWriteSeeker{}
		w, err := riffbin.NewStreamingWriter(m)
		if err != nil {
			t.Fatal(err)
		}
		n, err = w.WriteChunk(deepTree(101))
		if !errors.Is(err, riffbin.ErrUnwritableChunk) {
			t.Errorf("StreamingWriter: should be ErrUnwritableChunk but got: %v", err)
		}
		if n != 0 || len(m.buf) != 0 {
			t.Errorf("StreamingWriter: wrote %d byte(s) before failing", len(m.buf))
		}
	})
	t.Run("DeepestAllowed", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		if _, err := riffbin.NewWriter(&buf).WriteChunk(deepTree(100)); err != nil {
			t.Fatalf("should write the deepest tree the readers accept but got: %v", err)
		}
		if _, err := riffbin.ReadAll(bytes.NewReader(buf.Bytes())); err != nil {
			t.Errorf("the readers should read it back but got: %v", err)
		}
	})
}

// A single StreamingWriter must be able to write several chunks in a row:
// each backfill is relative to where its own chunk started.
func TestStreamingWriterConsecutiveWrites(t *testing.T) {
	t.Parallel()

	expected := []byte{
		// first chunk
		0x52, 0x49, 0x46, 0x46, // id (RIFF)
		0x10, 0x00, 0x00, 0x00, // body size (4 + 8 + 3 + 1)
		0x54, 0x45, 0x53, 0x54, // type (TEST)
		0x45, 0x4E, 0x54, 0x31, // id (ENT1)
		0x03, 0x00, 0x00, 0x00, // body size
		0x61, 0x62, 0x63, // "abc"
		0x00, // padding
		// second chunk
		0x52, 0x49, 0x46, 0x46, // id (RIFF)
		0x12, 0x00, 0x00, 0x00, // body size (4 + 8 + 5 + 1)
		0x54, 0x45, 0x53, 0x54, // type (TEST)
		0x45, 0x4E, 0x54, 0x31, // id (ENT1)
		0x05, 0x00, 0x00, 0x00, // body size
		0x64, 0x65, 0x66, 0x67, 0x68, // "defgh"
		0x00, // padding
	}

	verify := func(t *testing.T, got []byte, n1, n2 int64) {
		t.Helper()
		if n1 != 24 || n2 != 26 {
			t.Errorf("n1 should be 24 and n2 should be 26 but got: %d and %d", n1, n2)
		}
		if df := cmp.Diff(expected, got); df != "" {
			t.Errorf("unexpected bytes are written: %s", df)
		}
		if _, err := riffbin.ReadAll(bytes.NewReader(got[:24])); err != nil {
			t.Errorf("first chunk does not parse: %v", err)
		}
		if _, err := riffbin.ReadAll(bytes.NewReader(got[24:])); err != nil {
			t.Errorf("second chunk does not parse: %v", err)
		}
	}

	t.Run("Seek", func(t *testing.T) {
		t.Parallel()
		m := &memWriteSeeker{}
		w, err := riffbin.NewStreamingWriter(m)
		if err != nil {
			t.Fatal(err)
		}
		n1, err := w.WriteChunk(buildStreamingTree("abc"))
		if err != nil {
			t.Fatal(err)
		}
		n2, err := w.WriteChunk(buildStreamingTree("defgh"))
		if err != nil {
			t.Fatal(err)
		}
		if m.pos != int64(len(m.buf)) {
			t.Errorf("the writer should be left at the end of the data but is at %d of %d", m.pos, len(m.buf))
		}
		verify(t, m.buf, n1, n2)
	})
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
		n1, err := w.WriteChunk(buildStreamingTree("abc"))
		if err != nil {
			t.Fatal(err)
		}
		n2, err := w.WriteChunk(buildStreamingTree("defgh"))
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
		verify(t, got, n1, n2)
	})
}

// A write failure on the first pass — before any backfill — must surface as an error.
func TestStreamingWriterReportsFirstPassError(t *testing.T) {
	t.Parallel()

	m := &memWriteSeeker{}
	m.failWrite = func(pos int64) error { return errors.New("injected write failure") }
	w, err := riffbin.NewStreamingWriter(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.WriteChunk(buildStreamingTree("abc")); err == nil {
		t.Fatal("the write failure should be reported")
	}
}

// A failure while backfilling the size fields must surface as an error: the file
// holds placeholder sizes, so pretending the write succeeded would hand the caller
// a corrupt file.
func TestStreamingWriterReportsBackfillError(t *testing.T) {
	t.Parallel()

	t.Run("Seek", func(t *testing.T) {
		t.Parallel()
		m := &memWriteSeeker{}
		// the first pass only appends; every overwrite is a backfill write
		m.failWrite = func(pos int64) error {
			if pos < int64(len(m.buf)) {
				return errors.New("injected write failure")
			}
			return nil
		}
		w, err := riffbin.NewStreamingWriter(m)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.WriteChunk(buildStreamingTree("abc")); err == nil {
			t.Fatal("the backfill failure should be reported")
		}
	})
	t.Run("WriterAt", func(t *testing.T) {
		t.Parallel()
		m := &failingWriterAt{}
		w, err := riffbin.NewStreamingWriter(m)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.WriteChunk(buildStreamingTree("abc")); err == nil {
			t.Fatal("the backfill failure should be reported")
		}
	})
}

// A streaming sub-chunk whose stream turns out to be empty is a zero-sized
// sub-chunk: the backfilled size is 0 and no pad byte is emitted.
func TestStreamingWriterEmptyBody(t *testing.T) {
	t.Parallel()

	expected := []byte{
		0x52, 0x49, 0x46, 0x46, // id (RIFF)
		0x0C, 0x00, 0x00, 0x00, // body size (4 + 8 + 0)
		0x54, 0x45, 0x53, 0x54, // type (TEST)
		0x45, 0x4E, 0x54, 0x31, // id (ENT1)
		0x00, 0x00, 0x00, 0x00, // body size
	}

	m := &memWriteSeeker{}
	w, err := riffbin.NewStreamingWriter(m)
	if err != nil {
		t.Fatal(err)
	}
	n, err := w.WriteChunk(buildStreamingTree(""))
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(m.buf)) {
		t.Errorf("n should be %d but got %d", len(m.buf), n)
	}
	if df := cmp.Diff(expected, m.buf); df != "" {
		t.Errorf("unexpected bytes are written: %s", df)
	}
	if _, err := riffbin.ReadAll(bytes.NewReader(m.buf)); err != nil {
		t.Errorf("the written chunk does not parse: %v", err)
	}
}

// misreportingList reports a body size that ignores its children — a broken
// custom GroupedChunk implementation.
type misreportingList struct {
	children      []riffbin.Chunk
	bodySizeCalls int
}

func (c *misreportingList) ChunkID() riffbin.FourCC { return riffbin.MustParseFourCC("LIST") }
func (c *misreportingList) BodySize() int64 {
	c.bodySizeCalls++
	return riffbin.TypeBytes
}
func (c *misreportingList) GroupType() riffbin.FourCC { return riffbin.MustParseFourCC("LST1") }
func (c *misreportingList) Children() []riffbin.Chunk { return c.children }

// A group's size on disk is derived from its planned children — the group's
// own BodySize is never consulted — so a custom implementation misreporting
// it cannot desynchronize the header from the bytes below it: the file holds
// the derived size and reads back as the real tree.
func TestWriterDerivesGroupSize(t *testing.T) {
	t.Parallel()

	expected := flattenTree(t, &riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{&riffbin.ListChunk{
			ListType: riffbin.MustParseFourCC("LST1"),
			Payload: []riffbin.Chunk{
				&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DATA"), Payload: []byte("wxyz")},
			},
		}},
	})

	check := func(t *testing.T, list *misreportingList, out []byte) {
		t.Helper()
		if list.bodySizeCalls != 0 {
			t.Errorf("the group's BodySize was consulted %d time(s), want never", list.bodySizeCalls)
		}
		got, err := riffbin.ReadAll(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("the written file does not parse: %v", err)
		}
		if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
			t.Errorf("diff = %s", df)
		}
	}

	list := &misreportingList{children: []riffbin.Chunk{
		&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DATA"), Payload: []byte("wxyz")},
	}}
	var buf bytes.Buffer
	if _, err := riffbin.NewWriter(&buf).WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload:  []riffbin.Chunk{list},
	}); err != nil {
		t.Fatalf("Writer: %v", err)
	}
	check(t, list, buf.Bytes())

	list = &misreportingList{children: []riffbin.Chunk{
		&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DATA"), Payload: []byte("wxyz")},
	}}
	m := &memWriteSeeker{}
	w, err := riffbin.NewStreamingWriter(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload:  []riffbin.Chunk{list},
	}); err != nil {
		t.Fatalf("StreamingWriter: %v", err)
	}
	check(t, list, m.buf)
}

// The same streaming sub-chunk placed twice in one tree would drain its stream
// at the first occurrence and write a lying header at the second; the tree is
// rejected before the first byte, like every defect that is checkable up front.
func TestStreamingWriterRejectsDuplicateStreamingChunk(t *testing.T) {
	t.Parallel()

	shared := riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DATA"), strings.NewReader("abcdef"))
	tree := &riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload:  []riffbin.Chunk{shared, shared},
	}

	m := &memWriteSeeker{}
	w, err := riffbin.NewStreamingWriter(m)
	if err != nil {
		t.Fatal(err)
	}
	n, err := w.WriteChunk(tree)
	if !errors.Is(err, riffbin.ErrConsumedStreamingChunk) {
		t.Errorf("should be ErrConsumedStreamingChunk but got: %v", err)
	}
	if n != 0 || len(m.buf) != 0 {
		t.Errorf("wrote %d byte(s) before failing", len(m.buf))
	}
}

// lyingSizeStreamingChunk embeds a *StreamingSubChunk and overrides BodySize
// with a lie. The embedded stream is what makes it streaming; the writers
// work on that stream alone.
type lyingSizeStreamingChunk struct {
	*riffbin.StreamingSubChunk
}

func (c *lyingSizeStreamingChunk) BodySize() int64 { return 1 }

// A type embedding *StreamingSubChunk is itself streaming: the stream
// accessor is promoted, so Writer — which takes no streaming chunk — rejects
// the tree before writing anything.
func TestWriterRejectsEmbeddedStreamingChunk(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	n, err := riffbin.NewWriter(&buf).WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			&lyingSizeStreamingChunk{riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DAT1"), strings.NewReader("abc"))},
		},
	})
	if !errors.Is(err, riffbin.ErrUnexpectedStreamingChunk) {
		t.Errorf("should be ErrUnexpectedStreamingChunk but got: %v", err)
	}
	if n != 0 || buf.Len() != 0 {
		t.Errorf("wrote %d byte(s) before failing", buf.Len())
	}
}

// A streaming chunk is drained through its library-owned stream and the size
// fields are backfilled from the bytes actually written, so nothing a type
// layered on top reports can desynchronize the output: the lie never reaches
// the file.
func TestStreamingWriterIgnoresOverriddenBodySize(t *testing.T) {
	t.Parallel()

	m := &memWriteSeeker{}
	w, err := riffbin.NewStreamingWriter(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			&lyingSizeStreamingChunk{riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DAT1"), strings.NewReader("hello"))},
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("ENT2"), Payload: []byte("wxyz")},
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
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DAT1"), Payload: []byte("hello")},
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("ENT2"), Payload: []byte("wxyz")},
		},
	})
	if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
		t.Errorf("diff = %s", df)
	}
}

// Two streaming sub-chunks built over one underlying reader: the first would
// drain the stream and the second would silently write an empty chunk. The
// plan keys the duplicate check by the reader itself, so the tree is rejected
// before the first byte — reusing a reader variable is all it takes to hit
// this, no contract violation required.
func TestStreamingWriterRejectsSharedUnderlyingReader(t *testing.T) {
	t.Parallel()

	r := strings.NewReader("abcdef")
	m := &memWriteSeeker{}
	w, err := riffbin.NewStreamingWriter(m)
	if err != nil {
		t.Fatal(err)
	}
	n, err := w.WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DAT1"), r),
			riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DAT2"), r),
		},
	})
	if !errors.Is(err, riffbin.ErrConsumedStreamingChunk) {
		t.Errorf("should be ErrConsumedStreamingChunk but got: %v", err)
	}
	if n != 0 || len(m.buf) != 0 {
		t.Errorf("wrote %d byte(s) before failing", len(m.buf))
	}
	if r.Len() != 6 {
		t.Errorf("the stream should be untouched but %d of 6 byte(s) remain", r.Len())
	}
}

// nonComparableReader is an io.Reader whose dynamic type cannot be a map key —
// the duplicate-stream check must fall back to the library-owned body instead
// of panicking, and distinct readers must not be mistaken for one another.
type nonComparableReader struct {
	parts [][]byte
}

func (r nonComparableReader) Read(p []byte) (int, error) {
	if len(r.parts) == 0 || len(r.parts[0]) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.parts[0])
	return n, io.EOF
}

// Distinct streams behind non-comparable reader types must both write — the
// reader-identity check degrades to the per-chunk stream, never to a panic or
// a false rejection.
func TestStreamingWriterAcceptsDistinctNonComparableReaders(t *testing.T) {
	t.Parallel()

	m := &memWriteSeeker{}
	w, err := riffbin.NewStreamingWriter(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DAT1"), nonComparableReader{parts: [][]byte{[]byte("ab")}}),
			riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DAT2"), nonComparableReader{parts: [][]byte{[]byte("cd")}}),
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
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DAT1"), Payload: []byte("ab")},
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DAT2"), Payload: []byte("cd")},
		},
	})
	if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
		t.Errorf("diff = %s", df)
	}
}

// funcReader is an io.Reader of a type that cannot be hashed at all: a func
// type is not even comparable.
type funcReader func([]byte) (int, error)

func (f funcReader) Read(p []byte) (int, error) { return f(p) }

// wrapReader is the shape io.NopCloser has — a value struct holding an
// interface. reflect reports the type comparable, because an interface field
// is, but hashing a value of it hashes whatever the interface carries: over a
// funcReader that panics. The duplicate-stream check must not key on such a
// value; nothing about the tree is wrong here.
type wrapReader struct{ io.Reader }

func onceReader(payload string) funcReader {
	done := false
	return func(p []byte) (int, error) {
		if done {
			return 0, io.EOF
		}
		done = true
		return copy(p, payload), nil
	}
}

// A reader whose type only compares in principle must not be used as a map
// key: the write plans and runs instead of panicking with "hash of unhashable
// type" halfway through the tree.
func TestStreamingWriterAcceptsUnhashableWrappedReader(t *testing.T) {
	t.Parallel()

	m := &memWriteSeeker{}
	w, err := riffbin.NewStreamingWriter(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DAT1"), wrapReader{onceReader("ab")}),
			riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DAT2"), wrapReader{onceReader("cd")}),
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
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DAT1"), Payload: []byte("ab")},
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DAT2"), Payload: []byte("cd")},
		},
	})
	if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
		t.Errorf("diff = %s", df)
	}
}

// constReader is a stateless reader: its Read has a value receiver, so it has
// nothing to advance and every copy of it produces the same payload.
type constReader struct{ payload string }

func (c constReader) Read(p []byte) (int, error) {
	if c.payload == "" {
		return 0, io.EOF
	}
	return copy(p, c.payload), io.EOF
}

// Two equal stateless readers are two independent streams — a value receiver
// cannot drain anything — so both chunks must be written. Keying the
// duplicate-stream check on the reader's value instead of its identity would
// reject this perfectly writable tree.
func TestStreamingWriterAcceptsEqualStatelessReaders(t *testing.T) {
	t.Parallel()

	m := &memWriteSeeker{}
	w, err := riffbin.NewStreamingWriter(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DAT1"), constReader{payload: "ab"}),
			riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DAT2"), constReader{payload: "ab"}),
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
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DAT1"), Payload: []byte("ab")},
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DAT2"), Payload: []byte("ab")},
		},
	})
	if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
		t.Errorf("diff = %s", df)
	}
}

// A streaming sub-chunk built over a nil reader fails while planning, as a
// typed error before the first byte — never as a panic after the headers are
// already in the output.
func TestStreamingWriterRejectsNilReader(t *testing.T) {
	t.Parallel()

	m := &memWriteSeeker{}
	w, err := riffbin.NewStreamingWriter(m)
	if err != nil {
		t.Fatal(err)
	}
	n, err := w.WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DAT1"), nil),
		},
	})
	if !errors.Is(err, riffbin.ErrUnwritableChunk) {
		t.Errorf("should be ErrUnwritableChunk but got: %v", err)
	}
	if n != 0 || len(m.buf) != 0 {
		t.Errorf("wrote %d byte(s) before failing", len(m.buf))
	}
}

// nilBodySubChunk answers Body with nil — a broken custom SubChunk.
type nilBodySubChunk struct{}

func (nilBodySubChunk) ChunkID() riffbin.FourCC { return riffbin.MustParseFourCC("DAT1") }
func (nilBodySubChunk) BodySize() int64         { return 0 }
func (nilBodySubChunk) Body() io.Reader         { return nil }

// A sub-chunk whose Body is nil fails while planning, like every other defect
// the plan can see — not as a nil dereference inside the copy.
func TestWriterRejectsNilBody(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	n, err := riffbin.NewWriter(&buf).WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload:  []riffbin.Chunk{nilBodySubChunk{}},
	})
	if !errors.Is(err, riffbin.ErrUnwritableChunk) {
		t.Errorf("should be ErrUnwritableChunk but got: %v", err)
	}
	if n != 0 || buf.Len() != 0 {
		t.Errorf("wrote %d byte(s) before failing", buf.Len())
	}
}

// A streaming sub-chunk whose stream was already consumed would write a header
// counting bytes that can no longer be produced.
func TestStreamingWriterRejectsConsumedChunk(t *testing.T) {
	t.Parallel()

	tree := buildStreamingTree("abc")

	m1 := &memWriteSeeker{}
	w1, err := riffbin.NewStreamingWriter(m1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w1.WriteChunk(tree); err != nil {
		t.Fatal(err)
	}

	m2 := &memWriteSeeker{}
	w2, err := riffbin.NewStreamingWriter(m2)
	if err != nil {
		t.Fatal(err)
	}
	n, err := w2.WriteChunk(tree)
	if !errors.Is(err, riffbin.ErrConsumedStreamingChunk) {
		t.Errorf("should be ErrConsumedStreamingChunk but got: %v", err)
	}
	if n != 0 || len(m2.buf) != 0 {
		t.Errorf("wrote %d byte(s) before failing", len(m2.buf))
	}
}

// A drained stream that happened to be empty is still consumed: writing the
// chunk again must fail like any other reuse, not silently write whatever the
// underlying reader holds by then. A byte count cannot tell the two apart —
// consumption is tracked as its own state.
func TestStreamingWriterRejectsConsumedEmptyChunk(t *testing.T) {
	t.Parallel()

	var backing bytes.Buffer
	tree := &riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("ENT1"), &backing),
		},
	}

	m1 := &memWriteSeeker{}
	w1, err := riffbin.NewStreamingWriter(m1)
	if err != nil {
		t.Fatal(err)
	}
	// an empty streaming chunk is a valid zero-sized chunk
	if _, err = w1.WriteChunk(tree); err != nil {
		t.Fatal(err)
	}

	// refill the underlying reader; the stream is consumed regardless
	backing.WriteString("late data")

	m2 := &memWriteSeeker{}
	w2, err := riffbin.NewStreamingWriter(m2)
	if err != nil {
		t.Fatal(err)
	}
	n, err := w2.WriteChunk(tree)
	if !errors.Is(err, riffbin.ErrConsumedStreamingChunk) {
		t.Errorf("should be ErrConsumedStreamingChunk but got: %v", err)
	}
	if n != 0 || len(m2.buf) != 0 {
		t.Errorf("wrote %d byte(s) before failing", len(m2.buf))
	}
}

// endlessReader yields bytes forever.
type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'z'
	}
	return len(p), nil
}

// overrunningSubChunk declares a single byte but its body never ends — a
// broken custom SubChunk implementation.
type overrunningSubChunk struct{}

func (overrunningSubChunk) ChunkID() riffbin.FourCC { return riffbin.MustParseFourCC("OVER") }
func (overrunningSubChunk) BodySize() int64         { return 1 }
func (overrunningSubChunk) Body() io.Reader         { return endlessReader{} }

// A body is copied only up to the size its header declared: a byte past it is
// a defect no matter what follows, so an endless body fails with
// ErrSizeMismatch at the boundary instead of flooding the output.
func TestWriterStopsCopyAtDeclaredSize(t *testing.T) {
	t.Parallel()

	tree := &riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload:  []riffbin.Chunk{overrunningSubChunk{}},
	}
	// the root header and group type, the sub-chunk header, and the declared single byte
	const want = riffbin.HeaderBytes + riffbin.TypeBytes + riffbin.HeaderBytes + 1

	t.Run("Writer", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		n, err := riffbin.NewWriter(&buf).WriteChunk(tree)
		if !errors.Is(err, riffbin.ErrSizeMismatch) {
			t.Errorf("should be ErrSizeMismatch but got: %v", err)
		}
		if n != want || buf.Len() != want {
			t.Errorf("the copy should stop at the declared size: n = %d, wrote %d byte(s), want %d", n, buf.Len(), want)
		}
	})
	t.Run("StreamingWriter", func(t *testing.T) {
		t.Parallel()
		m := &memWriteSeeker{}
		w, err := riffbin.NewStreamingWriter(m)
		if err != nil {
			t.Fatal(err)
		}
		n, err := w.WriteChunk(tree)
		if !errors.Is(err, riffbin.ErrSizeMismatch) {
			t.Errorf("should be ErrSizeMismatch but got: %v", err)
		}
		if n != want || len(m.buf) != want {
			t.Errorf("the copy should stop at the declared size: n = %d, wrote %d byte(s), want %d", n, len(m.buf), want)
		}
	})
}

// A streaming body has no declared size to cap its copy, but once the tree
// outgrows the largest possible RIFF file, failure is inevitable: the write
// must stop at that bound with ErrChunkTooLarge instead of draining the rest
// of an endless stream. This test pushes ~4 GiB through the copy loop.
func TestStreamingWriterStopsAtFileSizeBound(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("writes 4 GiB through the copy loop")
	}

	w, err := riffbin.NewStreamingWriter(&fakeSeeker{Writer: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	n, err := w.WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DAT1"), endlessReader{}),
		},
	})
	if !errors.Is(err, riffbin.ErrChunkTooLarge) {
		t.Errorf("should be ErrChunkTooLarge but got: %v", err)
	}
	if want := riffbin.HeaderBytes + riffbin.MaxBodySize; n != want {
		t.Errorf("the write should stop at the file size bound: n = %d, want %d", n, want)
	}
}

// sliceBackedSubChunk is a custom SubChunk whose concrete type is not
// comparable — a value type holding slices.
type sliceBackedSubChunk struct {
	id    riffbin.FourCC
	parts [][]byte
}

func (c sliceBackedSubChunk) ChunkID() riffbin.FourCC { return c.id }

func (c sliceBackedSubChunk) BodySize() (n int64) {
	for _, p := range c.parts {
		n += int64(len(p))
	}
	return
}

func (c sliceBackedSubChunk) Body() io.Reader {
	rs := make([]io.Reader, len(c.parts))
	for i, p := range c.parts {
		rs[i] = bytes.NewReader(p)
	}
	return io.MultiReader(rs...)
}

// Any type honoring the SubChunk contract must be writable; in particular the
// writers may not require the dynamic type to be comparable — a map keyed by
// the interface value, or a comparison of chunk values, would panic on this
// one. Streams are tracked by their library-owned body pointers instead.
func TestWriterAcceptsNonComparableSubChunk(t *testing.T) {
	t.Parallel()

	tree := func() *riffbin.RIFFChunk {
		return &riffbin.RIFFChunk{
			FormType: riffbin.MustParseFourCC("TEST"),
			Payload: []riffbin.Chunk{
				sliceBackedSubChunk{id: riffbin.MustParseFourCC("DAT1"), parts: [][]byte{[]byte("ab"), []byte("cd")}},
			},
		}
	}
	expected := flattenTree(t, &riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DAT1"), Payload: []byte("abcd")},
		},
	})

	var buf bytes.Buffer
	if _, err := riffbin.NewWriter(&buf).WriteChunk(tree()); err != nil {
		t.Fatalf("Writer: %v", err)
	}
	got, err := riffbin.ReadAll(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Writer output does not parse: %v", err)
	}
	if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
		t.Errorf("Writer: diff = %s", df)
	}

	m := &memWriteSeeker{}
	w, err := riffbin.NewStreamingWriter(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.WriteChunk(tree()); err != nil {
		t.Fatalf("StreamingWriter: %v", err)
	}
	got, err = riffbin.ReadAll(bytes.NewReader(m.buf))
	if err != nil {
		t.Fatalf("StreamingWriter output does not parse: %v", err)
	}
	if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
		t.Errorf("StreamingWriter: diff = %s", df)
	}
}

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
	children []riffbin.Chunk
}

func (c *misreportingList) ChunkID() riffbin.FourCC   { return riffbin.MustParseFourCC("LIST") }
func (c *misreportingList) BodySize() int64           { return riffbin.TypeBytes }
func (c *misreportingList) GroupType() riffbin.FourCC { return riffbin.MustParseFourCC("LST1") }
func (c *misreportingList) Children() []riffbin.Chunk { return c.children }

// A grouped chunk whose BodySize is not what its type and children encode to
// would write a header the readers cannot reconcile with the bytes that
// follow; the tree is rejected before the first byte.
func TestWriterRejectsMisreportedGroupSize(t *testing.T) {
	t.Parallel()

	tree := func() *riffbin.RIFFChunk {
		return &riffbin.RIFFChunk{
			FormType: riffbin.MustParseFourCC("TEST"),
			Payload: []riffbin.Chunk{&misreportingList{children: []riffbin.Chunk{
				&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DATA"), Payload: []byte("wxyz")},
			}}},
		}
	}

	var buf bytes.Buffer
	n, err := riffbin.NewWriter(&buf).WriteChunk(tree())
	if !errors.Is(err, riffbin.ErrSizeMismatch) {
		t.Errorf("Writer: should be ErrSizeMismatch but got: %v", err)
	}
	if n != 0 || buf.Len() != 0 {
		t.Errorf("Writer: wrote %d byte(s) before failing", buf.Len())
	}

	m := &memWriteSeeker{}
	w, err := riffbin.NewStreamingWriter(m)
	if err != nil {
		t.Fatal(err)
	}
	n, err = w.WriteChunk(tree())
	if !errors.Is(err, riffbin.ErrSizeMismatch) {
		t.Errorf("StreamingWriter: should be ErrSizeMismatch but got: %v", err)
	}
	if n != 0 || len(m.buf) != 0 {
		t.Errorf("StreamingWriter: wrote %d byte(s) before failing", len(m.buf))
	}
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

// misreportingStreamingChunk drains its reader but keeps reporting a zero
// BodySize — a broken custom SubChunk implementation.
type misreportingStreamingChunk struct {
	r io.Reader
}

func (c *misreportingStreamingChunk) ChunkID() riffbin.FourCC { return riffbin.MustParseFourCC("LIED") }
func (c *misreportingStreamingChunk) BodySize() int64         { return 0 }
func (c *misreportingStreamingChunk) Streaming() bool         { return true }
func (c *misreportingStreamingChunk) Body() io.Reader         { return c.r }

// The size fields are backed by what BodySize reports once the stream is
// drained, so a streaming body that produced bytes its BodySize does not
// report would desynchronize every offset after it. The write must stop with
// ErrSizeMismatch at the divergence instead of emitting a corrupt file.
func TestStreamingWriterRejectsMisreportedBodySize(t *testing.T) {
	t.Parallel()

	m := &memWriteSeeker{}
	w, err := riffbin.NewStreamingWriter(m)
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			&misreportingStreamingChunk{r: strings.NewReader("hello")},
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("ENT2"), Payload: []byte("wxyz")},
		},
	})
	if !errors.Is(err, riffbin.ErrSizeMismatch) {
		t.Errorf("should be ErrSizeMismatch but got: %v", err)
	}
}

// Two streaming sub-chunks sharing one underlying reader are beyond what the
// writer can see: the first drains the stream and the second truthfully
// reports the zero bytes it produced, so the output is a valid file whose
// second chunk is empty.
func TestStreamingWriterSharedUnderlyingReader(t *testing.T) {
	t.Parallel()

	r := strings.NewReader("abcdef")
	m := &memWriteSeeker{}
	w, err := riffbin.NewStreamingWriter(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DAT1"), r),
			riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("DAT2"), r),
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
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DAT1"), Payload: []byte("abcdef")},
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("DAT2"), Payload: []byte{}},
		},
	})
	if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
		t.Errorf("diff = %s", df)
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

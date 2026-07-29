package riffbin_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/google/go-cmp/cmp"
	"github.com/karupanerura/riffbin"
)

// flatChunk is a chunk tree flattened for comparison across the two readers,
// which materialize sub-chunk bodies as different concrete types.
type flatChunk struct {
	Path string
	ID   string
	Body string
}

func flatten(t *testing.T, chunk riffbin.Chunk, prefix string, out *[]flatChunk) {
	t.Helper()

	path := prefix + "/" + chunk.ChunkID().String()
	switch c := chunk.(type) {
	case riffbin.GroupedChunk:
		path += "(" + c.GroupType().String() + ")"
		*out = append(*out, flatChunk{Path: path, ID: c.ChunkID().String()})
		for _, cc := range c.Children() {
			flatten(t, cc, path, out)
		}
	case riffbin.SubChunk:
		body, err := io.ReadAll(c.Body())
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if int64(len(body)) != c.BodySize() {
			t.Errorf("%s: BodySize is %d but the body holds %d byte(s)", path, c.BodySize(), len(body))
		}
		*out = append(*out, flatChunk{Path: path, ID: c.ChunkID().String(), Body: string(body)})
	default:
		t.Fatalf("%s: unexpected chunk type %T", path, chunk)
	}
}

func flattenTree(t *testing.T, chunk riffbin.Chunk) []flatChunk {
	t.Helper()

	var out []flatChunk
	flatten(t, chunk, "", &out)
	return out
}

// nestedListTree exercises a LIST holding non-empty sub-chunks, a LIST inside a LIST,
// chunks following a nested group, and odd-sized bodies that need a pad byte.
func nestedListTree() *riffbin.RIFFChunk {
	return &riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			&riffbin.ListChunk{
				ListType: riffbin.MustParseFourCC("LST1"),
				Payload: []riffbin.Chunk{
					&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("ENT1"), Payload: []byte("abcd")},
					&riffbin.ListChunk{
						ListType: riffbin.MustParseFourCC("LST2"),
						Payload: []riffbin.Chunk{
							&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("ENT2"), Payload: []byte("xyz")},
						},
					},
					&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("ENT3"), Payload: []byte("ijklm")},
				},
			},
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("ENT4"), Payload: []byte("mnop")},
		},
	}
}

// A RIFF file whose LIST holds a non-empty sub-chunk was unreadable by ReadSections:
// the body was skipped by seeking, and only the innermost boundary counter was adjusted,
// so every enclosing chunk believed its body still had unread bytes.
func TestReadSectionsNestedList(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if _, err := riffbin.NewWriter(&buf).WriteChunk(nestedListTree()); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()

	expected := flattenTree(t, nestedListTree())

	full, err := riffbin.ReadAll(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if df := cmp.Diff(expected, flattenTree(t, full)); df != "" {
		t.Errorf("ReadAll: diff = %s", df)
	}

	sections, err := riffbin.ReadSections(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("ReadSections: %v", err)
	}
	if df := cmp.Diff(expected, flattenTree(t, sections)); df != "" {
		t.Errorf("ReadSections: diff = %s", df)
	}
}

// Both readers must agree on which inputs are valid. ReadSections used to accept
// truncated files silently, because seeking past the end of a file succeeds.
func TestReadersAgreeOnTruncatedInput(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if _, err := riffbin.NewWriter(&buf).WriteChunk(nestedListTree()); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()

	for n := 0; n < len(b); n++ {
		truncated := b[:n]

		// truncation to nothing at all is a clean end of stream, not a malformed file
		want := riffbin.ErrInvalidFormat
		if n == 0 {
			want = io.EOF
		}

		_, fullErr := riffbin.ReadAll(bytes.NewReader(truncated))
		_, sectionsErr := riffbin.ReadSections(bytes.NewReader(truncated))
		if !errors.Is(fullErr, want) {
			t.Errorf("ReadAll(%d bytes): should be %v but got: %v", n, want, fullErr)
		}
		if !errors.Is(sectionsErr, want) {
			t.Errorf("ReadSections(%d bytes): should be %v but got: %v", n, want, sectionsErr)
		}
	}
}

// A hostile size field must not be trusted enough to allocate against.
func TestReadAllDoesNotAllocateDeclaredSize(t *testing.T) {
	b := []byte{
		'R', 'I', 'F', 'F',
		0xF0, 0xFF, 0xFF, 0xFF, // root body size: nearly 4 GiB
		'T', 'E', 'S', 'T',
		'E', 'N', 'T', '1',
		0x00, 0xFF, 0xFF, 0xFF, // sub-chunk body size: nearly 4 GiB
		'a', 'b', 'c',
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := riffbin.ReadAll(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrInvalidFormat) {
		t.Fatalf("should be ErrInvalidFormat but got: %v", err)
	}
	runtime.ReadMemStats(&after)

	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 16<<20 {
		t.Errorf("allocated %d bytes for a %d byte input", allocated, len(b))
	}
}

func TestReadDeeplyNestedList(t *testing.T) {
	t.Parallel()

	listChunk := func(listType string, body []byte) []byte {
		b := make([]byte, 0, riffbin.HeaderBytes+len(listType)+len(body))
		b = append(b, "LIST"...)
		b = binary.LittleEndian.AppendUint32(b, uint32(len(listType)+len(body)))
		b = append(b, listType...)
		return append(b, body...)
	}

	var body []byte
	for i := 0; i < 200; i++ {
		body = listChunk("LST1", body)
	}
	b := append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(4+len(body)))...)
	b = append(b, "TEST"...)
	b = append(b, body...)

	for name, read := range map[string]func([]byte) error{
		"ReadAll":      func(b []byte) error { _, err := riffbin.ReadAll(bytes.NewReader(b)); return err },
		"ReadSections": func(b []byte) error { _, err := riffbin.ReadSections(bytes.NewReader(b)); return err },
	} {
		name, read := name, read
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := read(b); !errors.Is(err, riffbin.ErrInvalidFormat) {
				t.Errorf("should be ErrInvalidFormat but got: %v", err)
			}
		})
	}
}

func TestReadRejectsSpecViolations(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		Name  string
		Bytes []byte
	}{
		{"NestedRIFFChunk", []byte{
			'R', 'I', 'F', 'F', 0x14, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
			'R', 'I', 'F', 'F', 0x04, 0x00, 0x00, 0x00, 'N', 'E', 'S', 'T',
		}},
		{"NestedRIFXChunk", []byte{
			'R', 'I', 'F', 'F', 0x14, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
			'R', 'I', 'F', 'X', 0x04, 0x00, 0x00, 0x00, 'N', 'E', 'S', 'T',
		}},
		{"NonASCIIChunkID", []byte{
			'R', 'I', 'F', 'F', 0x10, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
			'E', 'N', 'T', 0x00, 0x00, 0x00, 0x00, 0x00,
		}},
		{"NonASCIIFormType", []byte{
			'R', 'I', 'F', 'F', 0x04, 0x00, 0x00, 0x00, 'T', 'E', 'S', 0x01,
		}},
		{"NonASCIIListType", []byte{
			'R', 'I', 'F', 'F', 0x10, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
			'L', 'I', 'S', 'T', 0x04, 0x00, 0x00, 0x00, 'L', 'S', 'T', 0xFF,
		}},
		{"TrailingData", []byte{
			'R', 'I', 'F', 'F', 0x04, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T', 'j', 'u', 'n', 'k',
		}},
	} {
		tt := tt
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()
			if _, err := riffbin.ReadAll(bytes.NewReader(tt.Bytes)); !errors.Is(err, riffbin.ErrInvalidFormat) {
				t.Errorf("ReadAll should be ErrInvalidFormat but got: %v", err)
			}
			if _, err := riffbin.ReadSections(bytes.NewReader(tt.Bytes)); !errors.Is(err, riffbin.ErrInvalidFormat) {
				t.Errorf("ReadSections should be ErrInvalidFormat but got: %v", err)
			}
		})
	}
}

// A grouped chunk's body starts with its four-byte group type, so a LIST declaring
// fewer than four bytes cannot even hold its type, and one declaring four to eleven
// bytes has no room left for a complete sub-chunk header.
func TestListTooSmallForItsParts(t *testing.T) {
	t.Parallel()

	build := func(listBody []byte) []byte {
		b := []byte{'R', 'I', 'F', 'F'}
		b = binary.LittleEndian.AppendUint32(b, uint32(4+riffbin.HeaderBytes+len(listBody)))
		b = append(b, "TEST"...)
		b = append(b, "LIST"...)
		b = binary.LittleEndian.AppendUint32(b, uint32(len(listBody)))
		return append(b, listBody...)
	}

	for n := 0; n < riffbin.TypeBytes; n++ {
		n := n
		t.Run(fmt.Sprintf("TooShortForGroupType%d", n), func(t *testing.T) {
			t.Parallel()
			b := build([]byte("LST1")[:n])
			if _, err := riffbin.ReadAll(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrInvalidFormat) {
				t.Errorf("ReadAll should be ErrInvalidFormat but got: %v", err)
			}
			if _, err := riffbin.ReadSections(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrInvalidFormat) {
				t.Errorf("ReadSections should be ErrInvalidFormat but got: %v", err)
			}
		})
	}

	for n := 1; n < riffbin.HeaderBytes; n++ {
		n := n
		t.Run(fmt.Sprintf("TooShortForSubChunkHeader%d", n), func(t *testing.T) {
			t.Parallel()
			b := build(append([]byte("LST1"), []byte("ENT1\x00\x00\x00\x00")[:n]...))
			if _, err := riffbin.ReadAll(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrInvalidFormat) {
				t.Errorf("ReadAll should be ErrInvalidFormat but got: %v", err)
			}
			if _, err := riffbin.ReadSections(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrInvalidFormat) {
				t.Errorf("ReadSections should be ErrInvalidFormat but got: %v", err)
			}
		})
	}
}

// ReadSections must read a RIFF chunk embedded at a non-zero offset: every boundary
// is relative to the position the reader was handed at, not to the start of the file.
func TestReadSectionsFromNonZeroOffset(t *testing.T) {
	t.Parallel()

	prefix := []byte("leading garbage.")
	b := append(append([]byte{}, prefix...), paddedFileBytes...)

	r := bytes.NewReader(b)
	if _, err := r.Seek(int64(len(prefix)), io.SeekStart); err != nil {
		t.Fatal(err)
	}

	got, err := riffbin.ReadSections(r)
	if err != nil {
		t.Fatal(err)
	}
	if df := cmp.Diff(flattenTree(t, paddedFileChunk()), flattenTree(t, got)); df != "" {
		t.Errorf("diff = %s", df)
	}
}

func TestAllowTrailingData(t *testing.T) {
	t.Parallel()

	b := []byte{
		'R', 'I', 'F', 'F', 0x04, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T', 'j', 'u', 'n', 'k',
	}

	got, err := riffbin.ReadAll(bytes.NewReader(b), riffbin.AllowTrailingData())
	if err != nil {
		t.Fatal(err)
	}
	if got.FormType != riffbin.MustParseFourCC("TEST") {
		t.Errorf("unexpected form type: %s", got.FormType)
	}
}

// A stream of concatenated RIFF chunks — the layout AVI 2.0 uses to grow past the
// 32-bit size field by appending RIFF("AVIX") chunks — is read by calling a reader
// repeatedly with AllowTrailingData: each call consumes exactly one root chunk, and
// the end of the stream is io.EOF.
func TestReadConcatenatedRIFFChunks(t *testing.T) {
	t.Parallel()

	concatenated := append(append([]byte{}, paddedFileBytes...), paddedFileBytes...)
	expected := flattenTree(t, paddedFileChunk())

	t.Run("ReadAll", func(t *testing.T) {
		t.Parallel()

		r := bytes.NewReader(concatenated)
		for i := 0; i < 2; i++ {
			got, err := riffbin.ReadAll(r, riffbin.AllowTrailingData())
			if err != nil {
				t.Fatalf("chunk %d: %v", i, err)
			}
			if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
				t.Errorf("chunk %d: diff = %s", i, df)
			}
		}
		if _, err := riffbin.ReadAll(r, riffbin.AllowTrailingData()); !errors.Is(err, io.EOF) {
			t.Errorf("should be io.EOF at the end of the stream but got: %v", err)
		}
	})

	t.Run("ReadSections", func(t *testing.T) {
		t.Parallel()

		r := bytes.NewReader(concatenated)
		for i := 0; i < 2; i++ {
			got, err := riffbin.ReadSections(r, riffbin.AllowTrailingData())
			if err != nil {
				t.Fatalf("chunk %d: %v", i, err)
			}
			if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
				t.Errorf("chunk %d: diff = %s", i, df)
			}
		}
		if _, err := riffbin.ReadSections(r, riffbin.AllowTrailingData()); !errors.Is(err, io.EOF) {
			t.Errorf("should be io.EOF at the end of the stream but got: %v", err)
		}
	})
}

// A writer that pads its odd-sized final chunk without counting the pad byte in the
// RIFF size leaves a 0x00 between the chunks of a concatenated stream — the byte
// verifyEnd tolerates after a single chunk. AllowTrailingData must skip it, or the
// next call starts at the pad byte and misreads the root chunk header.
func TestReadConcatenatedRIFFChunksWithUncountedPad(t *testing.T) {
	t.Parallel()

	// the RIFF size 0x0F does not count ENT1's pad byte, so the trailing 0x00
	// lies outside the declared root chunk body
	padded := []byte{
		'R', 'I', 'F', 'F', 0x0F, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
		'E', 'N', 'T', '1', 0x03, 0x00, 0x00, 0x00, 'a', 'b', 'c', 0x00,
	}
	// the same chunk from a writer that omits the pad byte entirely: the next
	// header follows the odd body directly and must not lose its first byte
	unpadded := padded[:len(padded)-1]
	expected := flattenTree(t, &riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload:  []riffbin.Chunk{&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("ENT1"), Payload: []byte("abc")}},
	})

	streams := map[string][]byte{
		"UncountedPads": append(append([]byte{}, padded...), padded...),
		"NoPads":        append(append([]byte{}, unpadded...), unpadded...),
		"PadThenNoPad":  append(append([]byte{}, padded...), unpadded...),
	}

	for name, stream := range streams {
		name, stream := name, stream
		t.Run("ReadAll/"+name, func(t *testing.T) {
			t.Parallel()

			r := bytes.NewReader(stream)
			for i := 0; i < 2; i++ {
				got, err := riffbin.ReadAll(r, riffbin.AllowTrailingData())
				if err != nil {
					t.Fatalf("chunk %d: %v", i, err)
				}
				if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
					t.Errorf("chunk %d: diff = %s", i, df)
				}
			}
			if _, err := riffbin.ReadAll(r, riffbin.AllowTrailingData()); !errors.Is(err, io.EOF) {
				t.Errorf("should be io.EOF at the end of the stream but got: %v", err)
			}
		})
		t.Run("ReadSections/"+name, func(t *testing.T) {
			t.Parallel()

			r := bytes.NewReader(stream)
			for i := 0; i < 2; i++ {
				got, err := riffbin.ReadSections(r, riffbin.AllowTrailingData())
				if err != nil {
					t.Fatalf("chunk %d: %v", i, err)
				}
				if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
					t.Errorf("chunk %d: diff = %s", i, df)
				}
			}
			if _, err := riffbin.ReadSections(r, riffbin.AllowTrailingData()); !errors.Is(err, io.EOF) {
				t.Errorf("should be io.EOF at the end of the stream but got: %v", err)
			}
		})
		t.Run("Concatenated/"+name, func(t *testing.T) {
			t.Parallel()

			n := 0
			for got, err := range riffbin.Concatenated(bytes.NewReader(stream)) {
				if err != nil {
					t.Fatalf("chunk %d: %v", n, err)
				}
				if df := cmp.Diff(expected, flattenTree(t, got)); df != "" {
					t.Errorf("chunk %d: diff = %s", n, df)
				}
				n++
			}
			if n != 2 {
				t.Errorf("should yield 2 chunks but got: %d", n)
			}
		})
	}
}

func TestReadUnsupportedContainers(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"RF64", "BW64", "FFIR", "XFIR"} {
		id := id
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			b := append([]byte(id), 0x04, 0x00, 0x00, 0x00, 'W', 'A', 'V', 'E')
			if _, err := riffbin.ReadAll(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrUnsupportedFormat) {
				t.Errorf("should be ErrUnsupportedFormat but got: %v", err)
			}
		})
	}
}

func TestSyntaxErrorReportsPosition(t *testing.T) {
	t.Parallel()

	b := []byte{
		'R', 'I', 'F', 'F', 0x18, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
		'L', 'I', 'S', 'T', 0x0C, 0x00, 0x00, 0x00, 'L', 'S', 'T', '1',
		'E', 'N', 'T', 0x00, 0x00, 0x00, 0x00, 0x00, // the chunk ID at offset 24 is not ASCII
	}

	_, err := riffbin.ReadAll(bytes.NewReader(b))

	var syntaxErr *riffbin.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("should be a *SyntaxError but got: %v", err)
	}
	if !errors.Is(err, riffbin.ErrInvalidFormat) {
		t.Error("a *SyntaxError should wrap ErrInvalidFormat")
	}
	if syntaxErr.Offset != 24 {
		t.Errorf("offset should be 24 but got: %d", syntaxErr.Offset)
	}
	if syntaxErr.Path != "RIFF(TEST)/LIST(LST1)" {
		t.Errorf("unexpected path: %q", syntaxErr.Path)
	}
}

// oversizedSubChunk reports a body that the 32-bit RIFF size field cannot express.
type oversizedSubChunk struct {
	id   riffbin.FourCC
	size int64
}

func (c *oversizedSubChunk) ChunkID() riffbin.FourCC { return c.id }
func (c *oversizedSubChunk) BodySize() int64         { return c.size }
func (c *oversizedSubChunk) Streaming() bool         { return false }
func (c *oversizedSubChunk) Body() io.Reader         { return strings.NewReader("") }

// shortSubChunk produces fewer bytes than it declares.
type shortSubChunk struct {
	id riffbin.FourCC
}

func (c *shortSubChunk) ChunkID() riffbin.FourCC { return c.id }
func (c *shortSubChunk) BodySize() int64         { return 10 }
func (c *shortSubChunk) Streaming() bool         { return false }
func (c *shortSubChunk) Body() io.Reader         { return strings.NewReader("abc") }

// A Chunk implementation reporting a negative body size cannot be encoded; the
// writers reject the tree before writing anything.
func TestWriteRejectsNegativeBodySize(t *testing.T) {
	t.Parallel()

	_, err := riffbin.NewWriter(io.Discard).WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			&oversizedSubChunk{id: riffbin.MustParseFourCC("ENT1"), size: -1},
		},
	})
	if !errors.Is(err, riffbin.ErrSizeMismatch) {
		t.Errorf("should be ErrSizeMismatch but got: %v", err)
	}
}

// An I/O failure of the underlying reader is not a format defect: it must surface
// as-is, never classified as ErrInvalidFormat, at whatever position it strikes.
func TestReadPropagatesIOError(t *testing.T) {
	t.Parallel()

	errDisk := errors.New("injected read failure")

	// positions: the root header, the form type, a sub-chunk header, a sub-chunk
	// body, and the end-of-input probe after the root chunk
	for _, n := range []int{0, 8, 12, 20, len(paddedFileBytes)} {
		n := n
		t.Run(fmt.Sprintf("After%dBytes", n), func(t *testing.T) {
			t.Parallel()
			r := io.MultiReader(bytes.NewReader(paddedFileBytes[:n]), iotest.ErrReader(errDisk))
			_, err := riffbin.ReadAll(r)
			if !errors.Is(err, errDisk) {
				t.Errorf("the underlying error should be preserved but got: %v", err)
			}
			if errors.Is(err, riffbin.ErrInvalidFormat) {
				t.Errorf("an I/O error must not be a format error: %v", err)
			}
		})
	}

	t.Run("SeekError", func(t *testing.T) {
		t.Parallel()
		_, err := riffbin.ReadSections(&failingSeeker{ReadSeekerAt: bytes.NewReader(paddedFileBytes), err: errDisk})
		if !errors.Is(err, errDisk) {
			t.Errorf("the underlying error should be preserved but got: %v", err)
		}
		if errors.Is(err, riffbin.ErrInvalidFormat) {
			t.Errorf("an I/O error must not be a format error: %v", err)
		}
	})
}

// failingSeeker fails every Seek call.
type failingSeeker struct {
	riffbin.ReadSeekerAt
	err error
}

func (f *failingSeeker) Seek(offset int64, whence int) (int64, error) { return 0, f.err }

func TestWriteRejectsOversizedChunk(t *testing.T) {
	t.Parallel()

	t.Run("SubChunk", func(t *testing.T) {
		t.Parallel()
		_, err := riffbin.NewWriter(io.Discard).WriteChunk(&riffbin.RIFFChunk{
			FormType: riffbin.MustParseFourCC("TEST"),
			Payload: []riffbin.Chunk{
				&oversizedSubChunk{id: riffbin.MustParseFourCC("ENT1"), size: riffbin.MaxBodySize + 1},
			},
		})
		if !errors.Is(err, riffbin.ErrChunkTooLarge) {
			t.Errorf("should be ErrChunkTooLarge but got: %v", err)
		}
	})

	// the sum of the payload overflows even though no single chunk does
	t.Run("Group", func(t *testing.T) {
		t.Parallel()
		_, err := riffbin.NewWriter(io.Discard).WriteChunk(&riffbin.RIFFChunk{
			FormType: riffbin.MustParseFourCC("TEST"),
			Payload: []riffbin.Chunk{
				&oversizedSubChunk{id: riffbin.MustParseFourCC("ENT1"), size: 3 << 30},
				&oversizedSubChunk{id: riffbin.MustParseFourCC("ENT2"), size: 3 << 30},
			},
		})
		if !errors.Is(err, riffbin.ErrChunkTooLarge) {
			t.Errorf("should be ErrChunkTooLarge but got: %v", err)
		}
	})

	if riffbin.MaxBodySize != int64(math.MaxUint32) {
		t.Errorf("unexpected MaxBodySize: %d", riffbin.MaxBodySize)
	}
}

func TestWriteRejectsShortBody(t *testing.T) {
	t.Parallel()

	_, err := riffbin.NewWriter(io.Discard).WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload:  []riffbin.Chunk{&shortSubChunk{id: riffbin.MustParseFourCC("ENT1")}},
	})
	if !errors.Is(err, riffbin.ErrSizeMismatch) {
		t.Errorf("should be ErrSizeMismatch but got: %v", err)
	}
}

// Writing the same tree twice must produce the same bytes: sub-chunk bodies are
// re-readable, so a second write is not silently truncated.
func TestWriteIsRepeatable(t *testing.T) {
	t.Parallel()

	t.Run("InMemorySubChunk", func(t *testing.T) {
		t.Parallel()
		chunk := nestedListTree()

		var first, second bytes.Buffer
		if _, err := riffbin.NewWriter(&first).WriteChunk(chunk); err != nil {
			t.Fatal(err)
		}
		if _, err := riffbin.NewWriter(&second).WriteChunk(chunk); err != nil {
			t.Fatal(err)
		}
		if df := cmp.Diff(first.Bytes(), second.Bytes()); df != "" {
			t.Errorf("the second write differs: %s", df)
		}
	})

	t.Run("SectionSubChunk", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		if _, err := riffbin.NewWriter(&buf).WriteChunk(nestedListTree()); err != nil {
			t.Fatal(err)
		}
		original := buf.Bytes()

		chunk, err := riffbin.ReadSections(bytes.NewReader(original))
		if err != nil {
			t.Fatal(err)
		}

		for i := 0; i < 2; i++ {
			var out bytes.Buffer
			if _, err := riffbin.NewWriter(&out).WriteChunk(chunk); err != nil {
				t.Fatalf("write %d: %v", i, err)
			}
			if df := cmp.Diff(original, out.Bytes()); df != "" {
				t.Errorf("write %d differs from the input: %s", i, df)
			}
		}
	})
}

func ExampleSyntaxError() {
	_, err := riffbin.ReadAll(bytes.NewReader([]byte{
		'R', 'I', 'F', 'F', 0x0C, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
		'E', 'N', 'T', 0x00, 0x00, 0x00, 0x00, 0x00,
	}))

	var syntaxErr *riffbin.SyntaxError
	if errors.As(err, &syntaxErr) {
		fmt.Println(syntaxErr.Reason)
		fmt.Println(syntaxErr.Offset)
		fmt.Println(syntaxErr.Path)
	}
	fmt.Println(errors.Is(err, riffbin.ErrInvalidFormat))

	// Output:
	// chunk ID "ENT\x00" is not printable ASCII
	// 12
	// RIFF(TEST)
	// true
}

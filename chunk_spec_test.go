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
		for _, cc := range c.SubChunks() {
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
		FormType: riffbin.MustFourCC("TEST"),
		Payload: []riffbin.Chunk{
			&riffbin.ListChunk{
				ListType: riffbin.MustFourCC("LST1"),
				Payload: []riffbin.Chunk{
					&riffbin.OnMemorySubChunk{ID: riffbin.MustFourCC("ENT1"), Payload: []byte("abcd")},
					&riffbin.ListChunk{
						ListType: riffbin.MustFourCC("LST2"),
						Payload: []riffbin.Chunk{
							&riffbin.OnMemorySubChunk{ID: riffbin.MustFourCC("ENT2"), Payload: []byte("xyz")},
						},
					},
					&riffbin.OnMemorySubChunk{ID: riffbin.MustFourCC("ENT3"), Payload: []byte("ijklm")},
				},
			},
			&riffbin.OnMemorySubChunk{ID: riffbin.MustFourCC("ENT4"), Payload: []byte("mnop")},
		},
	}
}

// A RIFF file whose LIST holds a non-empty sub-chunk was unreadable by ReadSections:
// the body was skipped by seeking, and only the innermost boundary counter was adjusted,
// so every enclosing chunk believed its body still had unread bytes.
func TestReadSectionsNestedList(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if _, err := riffbin.NewCompletedChunkWriter(&buf).WriteChunk(nestedListTree()); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()

	expected := flattenTree(t, nestedListTree())

	full, err := riffbin.ReadFull(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if df := cmp.Diff(expected, flattenTree(t, full)); df != "" {
		t.Errorf("ReadFull: diff = %s", df)
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
	if _, err := riffbin.NewCompletedChunkWriter(&buf).WriteChunk(nestedListTree()); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()

	for n := 0; n < len(b); n++ {
		truncated := b[:n]

		_, fullErr := riffbin.ReadFull(bytes.NewReader(truncated))
		_, sectionsErr := riffbin.ReadSections(bytes.NewReader(truncated))
		if !errors.Is(fullErr, riffbin.ErrInvalidFormat) {
			t.Errorf("ReadFull(%d bytes): should be ErrInvalidFormat but got: %v", n, fullErr)
		}
		if !errors.Is(sectionsErr, riffbin.ErrInvalidFormat) {
			t.Errorf("ReadSections(%d bytes): should be ErrInvalidFormat but got: %v", n, sectionsErr)
		}
	}
}

// A hostile size field must not be trusted enough to allocate against.
func TestReadFullDoesNotAllocateDeclaredSize(t *testing.T) {
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
	if _, err := riffbin.ReadFull(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrInvalidFormat) {
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
		"ReadFull":     func(b []byte) error { _, err := riffbin.ReadFull(bytes.NewReader(b)); return err },
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
			if _, err := riffbin.ReadFull(bytes.NewReader(tt.Bytes)); !errors.Is(err, riffbin.ErrInvalidFormat) {
				t.Errorf("ReadFull should be ErrInvalidFormat but got: %v", err)
			}
			if _, err := riffbin.ReadSections(bytes.NewReader(tt.Bytes)); !errors.Is(err, riffbin.ErrInvalidFormat) {
				t.Errorf("ReadSections should be ErrInvalidFormat but got: %v", err)
			}
		})
	}
}

func TestAllowTrailingData(t *testing.T) {
	t.Parallel()

	b := []byte{
		'R', 'I', 'F', 'F', 0x04, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T', 'j', 'u', 'n', 'k',
	}

	got, err := riffbin.ReadFull(bytes.NewReader(b), riffbin.AllowTrailingData())
	if err != nil {
		t.Fatal(err)
	}
	if got.FormType != riffbin.MustFourCC("TEST") {
		t.Errorf("unexpected form type: %s", got.FormType)
	}
}

func TestReadUnsupportedContainers(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"RF64", "BW64", "FFIR", "XFIR"} {
		id := id
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			b := append([]byte(id), 0x04, 0x00, 0x00, 0x00, 'W', 'A', 'V', 'E')
			if _, err := riffbin.ReadFull(bytes.NewReader(b)); !errors.Is(err, riffbin.ErrUnsupportedFormat) {
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

	_, err := riffbin.ReadFull(bytes.NewReader(b))

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
func (c *oversizedSubChunk) Incomplete() bool        { return false }
func (c *oversizedSubChunk) Body() io.Reader         { return strings.NewReader("") }

// shortSubChunk produces fewer bytes than it declares.
type shortSubChunk struct {
	id riffbin.FourCC
}

func (c *shortSubChunk) ChunkID() riffbin.FourCC { return c.id }
func (c *shortSubChunk) BodySize() int64         { return 10 }
func (c *shortSubChunk) Incomplete() bool        { return false }
func (c *shortSubChunk) Body() io.Reader         { return strings.NewReader("abc") }

func TestWriteRejectsOversizedChunk(t *testing.T) {
	t.Parallel()

	t.Run("SubChunk", func(t *testing.T) {
		t.Parallel()
		_, err := riffbin.NewCompletedChunkWriter(io.Discard).WriteChunk(&riffbin.RIFFChunk{
			FormType: riffbin.MustFourCC("TEST"),
			Payload: []riffbin.Chunk{
				&oversizedSubChunk{id: riffbin.MustFourCC("ENT1"), size: riffbin.MaxBodySize + 1},
			},
		})
		if !errors.Is(err, riffbin.ErrChunkTooLarge) {
			t.Errorf("should be ErrChunkTooLarge but got: %v", err)
		}
	})

	// the sum of the payload overflows even though no single chunk does
	t.Run("Group", func(t *testing.T) {
		t.Parallel()
		_, err := riffbin.NewCompletedChunkWriter(io.Discard).WriteChunk(&riffbin.RIFFChunk{
			FormType: riffbin.MustFourCC("TEST"),
			Payload: []riffbin.Chunk{
				&oversizedSubChunk{id: riffbin.MustFourCC("ENT1"), size: 3 << 30},
				&oversizedSubChunk{id: riffbin.MustFourCC("ENT2"), size: 3 << 30},
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

	_, err := riffbin.NewCompletedChunkWriter(io.Discard).WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustFourCC("TEST"),
		Payload:  []riffbin.Chunk{&shortSubChunk{id: riffbin.MustFourCC("ENT1")}},
	})
	if !errors.Is(err, riffbin.ErrSizeMismatch) {
		t.Errorf("should be ErrSizeMismatch but got: %v", err)
	}
}

// Writing the same tree twice must produce the same bytes: sub-chunk bodies are
// re-readable, so a second write is not silently truncated.
func TestWriteIsRepeatable(t *testing.T) {
	t.Parallel()

	t.Run("OnMemorySubChunk", func(t *testing.T) {
		t.Parallel()
		chunk := nestedListTree()

		var first, second bytes.Buffer
		if _, err := riffbin.NewCompletedChunkWriter(&first).WriteChunk(chunk); err != nil {
			t.Fatal(err)
		}
		if _, err := riffbin.NewCompletedChunkWriter(&second).WriteChunk(chunk); err != nil {
			t.Fatal(err)
		}
		if df := cmp.Diff(first.Bytes(), second.Bytes()); df != "" {
			t.Errorf("the second write differs: %s", df)
		}
	})

	t.Run("InStreamSubChunk", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		if _, err := riffbin.NewCompletedChunkWriter(&buf).WriteChunk(nestedListTree()); err != nil {
			t.Fatal(err)
		}
		original := buf.Bytes()

		chunk, err := riffbin.ReadSections(bytes.NewReader(original))
		if err != nil {
			t.Fatal(err)
		}

		for i := 0; i < 2; i++ {
			var out bytes.Buffer
			if _, err := riffbin.NewCompletedChunkWriter(&out).WriteChunk(chunk); err != nil {
				t.Fatalf("write %d: %v", i, err)
			}
			if df := cmp.Diff(original, out.Bytes()); df != "" {
				t.Errorf("write %d differs from the input: %s", i, df)
			}
		}
	})
}

func ExampleSyntaxError() {
	_, err := riffbin.ReadFull(bytes.NewReader([]byte{
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

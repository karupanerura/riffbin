package riffbin_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/karupanerura/riffbin"
)

// basicChunk implements Chunk but is neither a GroupedChunk nor a SubChunk.
type basicChunk struct {
	id       riffbin.FourCC
	bodySize int64
}

var _ riffbin.Chunk = (*basicChunk)(nil)

func (c *basicChunk) ChunkID() riffbin.FourCC {
	return c.id
}

func (c *basicChunk) BodySize() int64 {
	return c.bodySize
}

func TestFourCC(t *testing.T) {
	t.Parallel()
	t.Run("Parse", func(t *testing.T) {
		t.Parallel()
		for _, tt := range []struct {
			In       string
			Expected riffbin.FourCC
		}{
			{"WAVE", riffbin.FourCC{'W', 'A', 'V', 'E'}},
			{"fmt", riffbin.FourCC{'f', 'm', 't', ' '}},
			{"", riffbin.FourCC{' ', ' ', ' ', ' '}},
		} {
			got, err := riffbin.ParseFourCC(tt.In)
			if err != nil {
				t.Fatalf("ParseFourCC(%q): %v", tt.In, err)
			}
			if got != tt.Expected {
				t.Errorf("ParseFourCC(%q) = %q, want %q", tt.In, got, tt.Expected)
			}
		}
	})
	t.Run("ParseError", func(t *testing.T) {
		t.Parallel()
		for _, in := range []string{"TOOLONG", "a\x00b", "\x7f"} {
			if _, err := riffbin.ParseFourCC(in); err == nil {
				t.Errorf("ParseFourCC(%q) should fail", in)
			}
		}
	})
	t.Run("String", func(t *testing.T) {
		t.Parallel()
		if got := riffbin.MustParseFourCC("fmt").String(); got != "fmt " {
			t.Errorf("unexpected string: %q", got)
		}
	})
	t.Run("Valid", func(t *testing.T) {
		t.Parallel()
		if !(riffbin.FourCC{'W', 'A', 'V', 'E'}).Valid() {
			t.Error("WAVE should be valid")
		}
		if (riffbin.FourCC{'W', 'A', 'V', 0x00}).Valid() {
			t.Error("a NUL byte should be invalid")
		}
	})
	t.Run("MustParseFourCCPanics", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if recover() == nil {
				t.Error("MustParseFourCC should panic on an invalid four-character code")
			}
		}()
		riffbin.MustParseFourCC("TOOLONG")
	})
}

func TestSyntaxErrorMessage(t *testing.T) {
	t.Parallel()

	withPath := &riffbin.SyntaxError{Offset: 12, Path: "RIFF(WAVE)", Reason: "bad chunk"}
	if got, want := withPath.Error(), "riffbin: invalid format: bad chunk at offset 12 in RIFF(WAVE)"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	withoutPath := &riffbin.SyntaxError{Offset: 0, Reason: "bad root"}
	if got, want := withoutPath.Error(), "riffbin: invalid format: bad root at offset 0"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	if !errors.Is(withPath, riffbin.ErrInvalidFormat) {
		t.Error("a *SyntaxError should wrap ErrInvalidFormat")
	}
}

func TestRIFFChunk(t *testing.T) {
	ent1 := &basicChunk{
		id:       riffbin.MustParseFourCC("ENT1"),
		bodySize: 17,
	}
	ent2 := &basicChunk{
		id:       riffbin.MustParseFourCC("ENT2"),
		bodySize: 11,
	}
	chunk := riffbin.RIFFChunk{
		FormType: [4]byte{'A', 'B', 'C', 'D'},
		Payload:  []riffbin.Chunk{ent1, ent2},
	}

	if chunk.ChunkID() != riffbin.MustParseFourCC("RIFF") {
		t.Errorf("unexpected id: %s", chunk.ChunkID())
	}
	// 4 (form type) + 8 + 17 + 1 (padding) + 8 + 11 + 1 (padding)
	if chunk.BodySize() != 50 {
		t.Errorf("unexpected body size: %d", chunk.BodySize())
	}
}

func TestRIFXChunkID(t *testing.T) {
	chunk := riffbin.RIFFChunk{
		ByteOrder: riffbin.BigEndian,
		FormType:  [4]byte{'A', 'B', 'C', 'D'},
	}

	if chunk.ChunkID() != riffbin.MustParseFourCC("RIFX") {
		t.Errorf("unexpected id: %s", chunk.ChunkID())
	}
}

func TestListChunk(t *testing.T) {
	chunk := riffbin.ListChunk{
		ListType: [4]byte{'A', 'B', 'C', 'D'},
		Payload: []riffbin.Chunk{
			&basicChunk{
				id:       riffbin.MustParseFourCC("ENT1"),
				bodySize: 11,
			},
			&basicChunk{
				id:       riffbin.MustParseFourCC("ENT2"),
				bodySize: 17,
			},
		},
	}

	if chunk.ChunkID() != riffbin.MustParseFourCC("LIST") {
		t.Errorf("unexpected id: %s", chunk.ChunkID())
	}
	// 4 (list type) + 8 + 11 + 1 (padding) + 8 + 17 + 1 (padding)
	if chunk.BodySize() != 50 {
		t.Errorf("unexpected body size: %d", chunk.BodySize())
	}
}

// a Chunk that is neither grouped nor a sub-chunk must be rejected, not panic
func TestWriteUnsupportedChunkType(t *testing.T) {
	t.Parallel()

	_, err := riffbin.NewWriter(io.Discard).WriteChunk(&riffbin.RIFFChunk{
		FormType: [4]byte{'T', 'E', 'S', 'T'},
		Payload: []riffbin.Chunk{
			&basicChunk{id: riffbin.MustParseFourCC("ENT1"), bodySize: 4},
		},
	})
	if !errors.Is(err, riffbin.ErrUnsupportedChunkType) {
		t.Errorf("should be ErrUnsupportedChunkType but got: %v", err)
	}
}

func TestInMemorySubChunk(t *testing.T) {
	chunk := riffbin.InMemorySubChunk{
		ID:      [4]byte{'A', 'B', 'C', 'D'},
		Payload: []byte("foobar"),
	}

	if chunk.ChunkID() != riffbin.MustParseFourCC("ABCD") {
		t.Errorf("unexpected id: %s", chunk.ChunkID())
	}
	if chunk.BodySize() != 6 {
		t.Errorf("unexpected body size: %d", chunk.BodySize())
	}

	// Body must be independent on every call
	for i := 0; i < 2; i++ {
		body, err := io.ReadAll(chunk.Body())
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "foobar" {
			t.Errorf("body[%d] should be %q but got: %q", i, "foobar", body)
		}
	}
}

type fakeSeeker struct {
	io.Writer
	pos int64
}

func (s *fakeSeeker) Write(p []byte) (n int, err error) {
	n, err = s.Writer.Write(p)
	s.pos += int64(n)
	return
}

func (s *fakeSeeker) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		s.pos = offset
		return offset, nil
	case io.SeekCurrent:
		return s.pos, nil
	}

	panic("should not reach here")
}

func TestStreamingSubChunk(t *testing.T) {
	chunk := riffbin.NewStreamingSubChunk([4]byte{'A', 'B', 'C', 'D'}, strings.NewReader("foobar"))

	if chunk.ChunkID() != riffbin.MustParseFourCC("ABCD") {
		t.Errorf("unexpected id: %s", chunk.ChunkID())
	}
	if chunk.BodySize() != 0 {
		t.Errorf("unexpected body size: %d", chunk.BodySize())
	}

	w, err := riffbin.NewStreamingWriter(&fakeSeeker{Writer: io.Discard})
	if err != nil {
		panic(err)
	}

	_, err = w.WriteChunk(&riffbin.RIFFChunk{
		FormType: [4]byte{'T', 'E', 'S', 'T'},
		Payload:  []riffbin.Chunk{chunk},
	})
	if err != nil {
		t.Fatal(err)
	}

	if chunk.BodySize() != 6 {
		t.Errorf("unexpected body size: %d", chunk.BodySize())
	}
}

// the body of a streaming sub-chunk counts what it hands out, however it is consumed
func TestStreamingSubChunkBodyTracksReads(t *testing.T) {
	t.Parallel()

	chunk := riffbin.NewStreamingSubChunk([4]byte{'A', 'B', 'C', 'D'}, strings.NewReader("foobar"))

	buf := make([]byte, 3)
	if _, err := io.ReadFull(chunk.Body(), buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "foo" {
		t.Errorf("unexpected body: %q", buf)
	}
	if chunk.BodySize() != 3 {
		t.Errorf("body size should be 3 but got: %d", chunk.BodySize())
	}

	rest, err := io.ReadAll(chunk.Body())
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != "bar" {
		t.Errorf("unexpected rest: %q", rest)
	}
	if chunk.BodySize() != 6 {
		t.Errorf("body size should be 6 but got: %d", chunk.BodySize())
	}
}

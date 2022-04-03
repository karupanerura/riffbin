package riffbin_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/karupanerura/riffbin"
)

type basicChunk struct {
	id       []byte
	bodySize uint32
}

var _ riffbin.Chunk = (*basicChunk)(nil)

func (c *basicChunk) ChunkID() []byte {
	return c.id
}

func (c *basicChunk) BodySize() uint32 {
	return c.bodySize
}

func TestRIFFChunk(t *testing.T) {
	chunk := riffbin.RIFFChunk{
		FormType: [4]byte{'A', 'B', 'C', 'D'},
		Payload: []riffbin.Chunk{
			&basicChunk{
				id:       []byte("ENT1"),
				bodySize: 17,
			},
			&basicChunk{
				id:       []byte("ENT2"),
				bodySize: 11,
			},
		},
	}

	if !bytes.Equal(chunk.ChunkID(), []byte("RIFF")) {
		t.Errorf("unexpected id: %s", chunk.ChunkID())
	}
	if chunk.BodySize() != 48 {
		t.Errorf("unexpected body size: %d", chunk.BodySize())
	}
}

func TestListChunk(t *testing.T) {
	chunk := riffbin.ListChunk{
		ListType: [4]byte{'A', 'B', 'C', 'D'},
		Payload: []riffbin.Chunk{
			&basicChunk{
				id:       []byte("ENT1"),
				bodySize: 11,
			},
			&basicChunk{
				id:       []byte("ENT2"),
				bodySize: 17,
			},
		},
	}

	if !bytes.Equal(chunk.ChunkID(), []byte("LIST")) {
		t.Errorf("unexpected id: %s", chunk.ChunkID())
	}
	if chunk.BodySize() != 48 {
		t.Errorf("unexpected body size: %d", chunk.BodySize())
	}
}

func TestCompletedSubChunk(t *testing.T) {
	chunk := riffbin.CompletedSubChunk{
		ID:      [4]byte{'A', 'B', 'C', 'D'},
		Payload: []byte("foobar"),
	}

	if !bytes.Equal(chunk.ChunkID(), []byte("ABCD")) {
		t.Errorf("unexpected id: %s", chunk.ChunkID())
	}
	if chunk.BodySize() != 6 {
		t.Errorf("unexpected body size: %d", chunk.BodySize())
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

func TestIncompleteSubChunk(t *testing.T) {
	chunk := riffbin.NewIncompleteSubChunk([4]byte{'A', 'B', 'C', 'D'}, strings.NewReader("foobar"))

	if !bytes.Equal(chunk.ChunkID(), []byte("ABCD")) {
		t.Errorf("unexpected id: %s", chunk.ChunkID())
	}
	if chunk.BodySize() != 0 {
		t.Errorf("unexpected body size: %d", chunk.BodySize())
	}

	w, err := riffbin.NewIncompleteChunkWriter(&fakeSeeker{Writer: io.Discard})
	if err != nil {
		panic(err)
	}

	_, err = w.Write(&riffbin.RIFFChunk{
		FormType: [4]byte{'T', 'E', 'S', 'T'},
		Payload:  []riffbin.Chunk{chunk},
	})

	if chunk.BodySize() != 6 {
		t.Errorf("unexpected body size: %d", chunk.BodySize())
	}
}

package riffbin

import (
	"bytes"
	"io"
	"os"
	"sync"
)

const (
	idBytes   = 4
	sizeBytes = 4
	typeBytes = 4
)

const (
	HeaderBytes = 8
)

var (
	riffID = [idBytes]byte{'R', 'I', 'F', 'F'}
	listID = [idBytes]byte{'L', 'I', 'S', 'T'}
)

// Chunk is a chunk of RIFF spec
type Chunk interface {
	// ChunkID is the chunk ID. this must be 4 byte and must not be modified.
	ChunkID() []byte

	// BodySize is byte lenght of the chunk body.
	BodySize() uint32
}

type groupedChunk interface {
	Chunk

	groupType() []byte
	payload() []Chunk
}

// RIFFChunk is a RIFF chunk. This is must be the root chunk.
type RIFFChunk struct {
	FormType [typeBytes]byte
	Payload  []Chunk
}

var _ groupedChunk = (*RIFFChunk)(nil)

func (c *RIFFChunk) ChunkID() []byte {
	return riffID[:]
}

func (c *RIFFChunk) BodySize() (size uint32) {
	size = typeBytes
	for _, p := range c.Payload {
		size += HeaderBytes + p.BodySize()
	}
	return
}

func (c *RIFFChunk) groupType() []byte {
	return c.FormType[:]
}

func (c *RIFFChunk) payload() []Chunk {
	return c.Payload
}

// ListChunk is a LIST chunk.
type ListChunk struct {
	ListType [typeBytes]byte
	Payload  []Chunk
}

var _ groupedChunk = (*ListChunk)(nil)

func (c *ListChunk) ChunkID() []byte {
	return listID[:]
}

func (c *ListChunk) BodySize() (size uint32) {
	size = typeBytes
	for _, p := range c.Payload {
		size += HeaderBytes + p.BodySize()
	}
	return
}

func (c *ListChunk) groupType() []byte {
	return c.ListType[:]
}

func (c *ListChunk) payload() []Chunk {
	return c.Payload
}

type SubChunk interface {
	Chunk
	io.Reader

	// Incomplete returns true if the SubChunk payload is fluid, or not it returns false.
	// Incomplete sub-chunk is only after the payload have been read that the BodySize is determined.
	// Completed sub-chunk have a stable size of the payload.
	Incomplete() bool
}

// OnMemorySubChunk is a sub-chunk with the payload on memory.
type OnMemorySubChunk struct {
	ID      [idBytes]byte
	Payload []byte

	once sync.Once
	r    *bytes.Reader
}

var _ SubChunk = (*OnMemorySubChunk)(nil)

func (c *OnMemorySubChunk) ChunkID() []byte {
	return c.ID[:]
}

func (c *OnMemorySubChunk) BodySize() uint32 {
	return uint32(len(c.Payload))
}

func (c *OnMemorySubChunk) Incomplete() bool {
	return false
}

func (c *OnMemorySubChunk) Read(p []byte) (int, error) {
	c.once.Do(func() {
		c.r = bytes.NewReader(c.Payload)
	})
	return c.r.Read(p)
}

func (c *OnMemorySubChunk) WriteTo(w io.Writer) (int64, error) {
	c.once.Do(func() {
		c.r = bytes.NewReader(c.Payload)
	})
	return c.r.WriteTo(w)
}

// IncompleteSubChunk is a sub-chunk with the incomplete payload provided from io.Reader.
type IncompleteSubChunk struct {
	id [idBytes]byte
	incompleteChunkBody
}

var _ SubChunk = (*IncompleteSubChunk)(nil)

func NewIncompleteSubChunk(id [idBytes]byte, r io.Reader) *IncompleteSubChunk {
	return &IncompleteSubChunk{id, incompleteChunkBody{reader: r}}
}

func (c *IncompleteSubChunk) ChunkID() []byte {
	return c.id[:]
}

func (c *IncompleteSubChunk) BodySize() uint32 {
	return c.writtenLength
}

func (c *IncompleteSubChunk) Incomplete() bool {
	return true
}

type incompleteChunkBody struct {
	writtenLength uint32
	reader        io.Reader
}

func (c *incompleteChunkBody) Read(p []byte) (n int, err error) {
	n, err = c.reader.Read(p)
	c.writtenLength += uint32(n)
	return
}

func (c *incompleteChunkBody) WriteTo(w io.Writer) (n int64, err error) {
	n, err = io.Copy(w, c.reader)
	c.writtenLength += uint32(n)
	return
}

// FileSubChunk is a sub-chunk with the payload on *os.File.
type FileSubChunk struct {
	ID   [idBytes]byte
	Size uint32
	File *os.File

	once sync.Once
	r    *io.LimitedReader
}

var _ SubChunk = (*FileSubChunk)(nil)

func (c *FileSubChunk) ChunkID() []byte {
	return c.ID[:]
}

func (c *FileSubChunk) BodySize() uint32 {
	return c.Size
}

func (c *FileSubChunk) Read(p []byte) (n int, err error) {
	c.once.Do(func() {
		c.r = &io.LimitedReader{R: c.File, N: int64(c.Size)}
	})
	return c.r.Read(p)
}

func (c *FileSubChunk) Incomplete() bool {
	return false
}

func (c *FileSubChunk) ReadAll() (*OnMemorySubChunk, error) {
	payload, err := io.ReadAll(c)
	if err != nil {
		return nil, err
	}

	return &OnMemorySubChunk{ID: c.ID, Payload: payload}, nil
}

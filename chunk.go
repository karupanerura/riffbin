package riffbin

import (
	"bytes"
	"io"
)

const (
	idBytes   = 4
	sizeBytes = 4
	typeBytes = 4
)

var (
	riffID = [idBytes]byte{'R', 'I', 'F', 'F'}
	listID = [idBytes]byte{'L', 'I', 'S', 'T'}
)

// Chunk is a chunk of RIFF spec
type Chunk interface {
	// ID is the chunk ID. this must be 4 byte and must not be modified.
	ChunkID() []byte

	// HeaderSize is byte lenght of the chunk header.
	HeaderSize() uint32

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

func (c *RIFFChunk) HeaderSize() uint32 {
	return idBytes + sizeBytes + typeBytes
}

func (c *RIFFChunk) BodySize() (size uint32) {
	size = 4 // for form type size
	for _, p := range c.Payload {
		size += p.HeaderSize() + p.BodySize()
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

func (c *ListChunk) HeaderSize() uint32 {
	return idBytes + sizeBytes + typeBytes
}

func (c *ListChunk) BodySize() (size uint32) {
	size = 4 // for form type size
	for _, p := range c.Payload {
		size += p.HeaderSize() + p.BodySize()
	}
	return
}

func (c *ListChunk) groupType() []byte {
	return c.ListType[:]
}

func (c *ListChunk) payload() []Chunk {
	return c.Payload
}

type subChunk interface {
	Chunk
	incomplete() bool
	bodyReader() io.Reader
}

// CompletedSubChunk is a fixed size sub-chunk.
type CompletedSubChunk struct {
	ID      [idBytes]byte
	Payload []byte
}

var _ subChunk = (*CompletedSubChunk)(nil)

func (c *CompletedSubChunk) ChunkID() []byte {
	return c.ID[:]
}

func (c *CompletedSubChunk) HeaderSize() uint32 {
	return idBytes + sizeBytes
}

func (c *CompletedSubChunk) BodySize() uint32 {
	return uint32(len(c.Payload))
}

func (c *CompletedSubChunk) incomplete() bool {
	return false
}

func (c *CompletedSubChunk) bodyReader() io.Reader {
	return bytes.NewReader(c.Payload)
}

// IncompleteSubChunk is a stream sub-chunk.
type IncompleteSubChunk struct {
	id   [idBytes]byte
	body *incompleteChunkBody
}

var _ subChunk = (*IncompleteSubChunk)(nil)

func NewIncompleteSubChunk(id [idBytes]byte, r io.Reader) *IncompleteSubChunk {
	return &IncompleteSubChunk{id: id, body: &incompleteChunkBody{reader: r}}
}

func (c *IncompleteSubChunk) ChunkID() []byte {
	return c.id[:]
}

func (c *IncompleteSubChunk) HeaderSize() uint32 {
	return idBytes + sizeBytes
}

func (c *IncompleteSubChunk) BodySize() uint32 {
	return c.body.writtenLength
}

func (c *IncompleteSubChunk) incomplete() bool {
	return true
}

func (c *IncompleteSubChunk) bodyReader() io.Reader {
	return c.body
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

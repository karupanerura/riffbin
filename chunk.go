package riffbin

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
)

const (
	// IDBytes is the byte length of a chunk ID.
	IDBytes = 4

	// SizeBytes is the byte length of a chunk size field.
	SizeBytes = 4

	// TypeBytes is the byte length of a group type.
	TypeBytes = 4

	// HeaderBytes is the byte length of a chunk header, that is the chunk ID plus the size field.
	HeaderBytes = IDBytes + SizeBytes
)

// MaxBodySize is the largest chunk body that fits in the 32-bit RIFF size field.
const MaxBodySize = int64(math.MaxUint32)

// ByteOrder selects the byte order of the size fields of a RIFF tree.
// It does not affect chunk IDs or group types, which are byte sequences in either order.
type ByteOrder uint8

const (
	// LittleEndian is the byte order of a standard "RIFF" container. It is the zero value.
	LittleEndian ByteOrder = iota

	// BigEndian is the byte order of a "RIFX" container.
	BigEndian
)

func (o ByteOrder) String() string {
	if o == BigEndian {
		return "RIFX"
	}
	return "RIFF"
}

func (o ByteOrder) binary() binary.ByteOrder {
	if o == BigEndian {
		return binary.BigEndian
	}
	return binary.LittleEndian
}

// Chunk is a chunk of the RIFF format.
//
// Every chunk is either a GroupedChunk or a SubChunk; the writer rejects anything else
// with ErrUnsupportedChunkType.
type Chunk interface {
	// ChunkID is the four-character chunk ID.
	ChunkID() FourCC

	// BodySize is the byte length of the chunk body.
	// It excludes the header and the word-alignment pad byte that follows an odd-sized body.
	BodySize() int64
}

// GroupedChunk is a chunk whose body is a group type followed by other chunks.
// RIFF and LIST are the grouped chunks defined by the specification.
type GroupedChunk interface {
	Chunk

	// GroupType is the four-character form type (RIFF) or list type (LIST).
	GroupType() FourCC

	// SubChunks are the chunks contained in this chunk.
	SubChunks() []Chunk
}

// SubChunk is a leaf chunk carrying a byte payload.
type SubChunk interface {
	Chunk

	// Body returns a reader over the chunk payload.
	//
	// A completed sub-chunk returns an independent reader on every call, so it can be
	// written more than once. An incomplete sub-chunk returns the underlying stream,
	// which can only be consumed once.
	Body() io.Reader

	// Incomplete returns true if the SubChunk payload is fluid, or not it returns false.
	// Incomplete sub-chunk is only after the payload have been read that the BodySize is determined.
	// Completed sub-chunk have a stable size of the payload.
	Incomplete() bool
}

// groupBodySize is the body size of a grouped chunk: the group type plus every
// sub-chunk with its header and its word-alignment pad byte.
func groupBodySize(payload []Chunk) (size int64) {
	size = TypeBytes
	for _, p := range payload {
		b := p.BodySize()
		size += HeaderBytes + b + (b & 1) // an odd-sized chunk is followed by a padding byte
	}
	return
}

// RIFFChunk is a RIFF chunk. This is must be the root chunk.
type RIFFChunk struct {
	// ByteOrder selects a "RIFF" (LittleEndian, the zero value) or a "RIFX" (BigEndian) container.
	ByteOrder ByteOrder

	FormType FourCC
	Payload  []Chunk
}

var _ GroupedChunk = (*RIFFChunk)(nil)

func (c *RIFFChunk) ChunkID() FourCC {
	if c.ByteOrder == BigEndian {
		return rifxID
	}
	return riffID
}

func (c *RIFFChunk) BodySize() int64 { return groupBodySize(c.Payload) }

func (c *RIFFChunk) GroupType() FourCC { return c.FormType }

func (c *RIFFChunk) SubChunks() []Chunk { return c.Payload }

// ListChunk is a LIST chunk.
type ListChunk struct {
	ListType FourCC
	Payload  []Chunk
}

var _ GroupedChunk = (*ListChunk)(nil)

func (c *ListChunk) ChunkID() FourCC { return listID }

func (c *ListChunk) BodySize() int64 { return groupBodySize(c.Payload) }

func (c *ListChunk) GroupType() FourCC { return c.ListType }

func (c *ListChunk) SubChunks() []Chunk { return c.Payload }

// OnMemorySubChunk is a sub-chunk with the payload on memory.
type OnMemorySubChunk struct {
	ID      FourCC
	Payload []byte
}

var _ SubChunk = (*OnMemorySubChunk)(nil)

func (c *OnMemorySubChunk) ChunkID() FourCC { return c.ID }

func (c *OnMemorySubChunk) BodySize() int64 { return int64(len(c.Payload)) }

func (c *OnMemorySubChunk) Incomplete() bool { return false }

func (c *OnMemorySubChunk) Body() io.Reader { return bytes.NewReader(c.Payload) }

// IncompleteSubChunk is a sub-chunk with the incomplete payload provided from io.Reader.
type IncompleteSubChunk struct {
	id   FourCC
	body incompleteChunkBody
}

var _ SubChunk = (*IncompleteSubChunk)(nil)

func NewIncompleteSubChunk(id FourCC, r io.Reader) *IncompleteSubChunk {
	return &IncompleteSubChunk{id: id, body: incompleteChunkBody{reader: r}}
}

func (c *IncompleteSubChunk) ChunkID() FourCC { return c.id }

func (c *IncompleteSubChunk) BodySize() int64 { return c.body.readLength }

func (c *IncompleteSubChunk) Incomplete() bool { return true }

// Body returns the underlying stream. It can only be consumed once, and the chunk
// only knows its BodySize after it has been consumed.
func (c *IncompleteSubChunk) Body() io.Reader { return &c.body }

type incompleteChunkBody struct {
	readLength int64
	reader     io.Reader
}

func (c *incompleteChunkBody) Read(p []byte) (n int, err error) {
	n, err = c.reader.Read(p)
	c.readLength += int64(n)
	return
}

func (c *incompleteChunkBody) WriteTo(w io.Writer) (n int64, err error) {
	n, err = io.Copy(w, c.reader)
	c.readLength += n
	return
}

// InStreamSubChunk is a sub-chunk whose payload is a section of a seekable stream,
// read on demand. ReadSections creates these; a hand-made value needs a non-nil
// embedded *io.SectionReader, or BodySize and Body panic.
type InStreamSubChunk struct {
	ID FourCC
	*io.SectionReader
}

var _ SubChunk = (*InStreamSubChunk)(nil)

func (c *InStreamSubChunk) ChunkID() FourCC { return c.ID }

func (c *InStreamSubChunk) BodySize() int64 { return c.SectionReader.Size() }

func (c *InStreamSubChunk) Incomplete() bool { return false }

// Body returns an independent reader over the section, leaving the embedded
// *io.SectionReader untouched so the chunk can be written repeatedly.
func (c *InStreamSubChunk) Body() io.Reader {
	return io.NewSectionReader(c.SectionReader, 0, c.SectionReader.Size())
}

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

// String returns the four-character container ID the byte order selects:
// "RIFF" for LittleEndian and "RIFX" for BigEndian.
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
//
// A group's size on disk is derived: the group type plus every child with its
// header and its word-alignment pad byte. The writers compute it from
// Children and never consult a group's BodySize, so a custom implementation
// cannot desynchronize a group header from the bytes below it; the built-in
// types compute BodySize the same way, for the caller's own arithmetic.
type GroupedChunk interface {
	Chunk

	// GroupType is the four-character form type (RIFF) or list type (LIST).
	GroupType() FourCC

	// Children are the chunks contained in this chunk.
	Children() []Chunk
}

// SubChunk is a leaf chunk carrying a byte payload.
//
// Body must produce exactly the BodySize the chunk declares; the writers
// verify this and fail with ErrSizeMismatch. A payload of unknown length is
// a *StreamingSubChunk instead — the writers recognize one by its type (or
// by an embedded one) and size it from the bytes its stream produces.
type SubChunk interface {
	Chunk

	// Body returns a reader over the chunk payload.
	//
	// A sub-chunk returns an independent reader on every call, so it can be
	// written more than once. A *StreamingSubChunk returns its underlying
	// stream instead, which can only be consumed once.
	Body() io.Reader
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

// RIFFChunk is the RIFF chunk, the root of the tree: its body is a form type
// such as "WAVE" followed by every other chunk of the file. The specification
// allows it only at the top level, so the writers reject a nested one.
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

func (c *RIFFChunk) Children() []Chunk { return c.Payload }

// ListChunk is a LIST chunk: an ordered sequence of sub-chunks under a
// four-character list type. LIST is the only chunk besides RIFF that the
// specification allows to contain other chunks.
type ListChunk struct {
	ListType FourCC
	Payload  []Chunk
}

var _ GroupedChunk = (*ListChunk)(nil)

func (c *ListChunk) ChunkID() FourCC { return listID }

func (c *ListChunk) BodySize() int64 { return groupBodySize(c.Payload) }

func (c *ListChunk) GroupType() FourCC { return c.ListType }

func (c *ListChunk) Children() []Chunk { return c.Payload }

// InMemorySubChunk is a sub-chunk holding its payload in memory.
type InMemorySubChunk struct {
	ID      FourCC
	Payload []byte
}

var _ SubChunk = (*InMemorySubChunk)(nil)

func (c *InMemorySubChunk) ChunkID() FourCC { return c.ID }

func (c *InMemorySubChunk) BodySize() int64 { return int64(len(c.Payload)) }

func (c *InMemorySubChunk) Body() io.Reader { return bytes.NewReader(c.Payload) }

// StreamingSubChunk is a sub-chunk whose payload comes from an io.Reader of
// unknown length. Only StreamingWriter can write it: the size field is
// not known until the reader has been drained.
//
// It is what makes a chunk streaming: the writers recognize a sub-chunk as
// streaming iff it is a *StreamingSubChunk or embeds one (a custom type
// needs a non-nil embedded pointer, or writing panics), and they size it
// from the bytes its stream produces — nothing such a type reports can
// desynchronize the size fields from the bytes actually written.
type StreamingSubChunk struct {
	id   FourCC
	body streamingChunkBody
}

var _ SubChunk = (*StreamingSubChunk)(nil)

// NewStreamingSubChunk returns a sub-chunk that streams its payload from r.
// The length of r does not have to be known in advance: StreamingWriter
// writes the payload through and fixes the size fields afterwards.
func NewStreamingSubChunk(id FourCC, r io.Reader) *StreamingSubChunk {
	return &StreamingSubChunk{id: id, body: streamingChunkBody{reader: r}}
}

func (c *StreamingSubChunk) ChunkID() FourCC { return c.id }

// BodySize is the number of bytes the stream has produced so far: zero
// before the chunk is written, the final body size once it has been.
func (c *StreamingSubChunk) BodySize() int64 { return c.body.readLength }

// Body returns the underlying stream, which can only be consumed once: any
// read from it marks the chunk consumed — a later write fails with
// ErrConsumedStreamingChunk — and the chunk knows its BodySize only after
// the stream has been drained.
func (c *StreamingSubChunk) Body() io.Reader { return &c.body }

func (c *StreamingSubChunk) streamingBody() *streamingChunkBody { return &c.body }

// streamer is how the writers recognize a streaming sub-chunk. Only
// *StreamingSubChunk can carry the unexported method — a custom type becomes
// streaming by embedding one — so every streaming body is library-owned: the
// writers track duplication and consumption on the *streamingChunkBody
// itself, drain it directly, and never depend on what an implementation
// layered on top reports.
type streamer interface {
	streamingBody() *streamingChunkBody
}

// streamingChunkBody is the single-consumption stream of a StreamingSubChunk.
// Any Read or WriteTo call marks it consumed — one producing no bytes
// included — which is what the writers check: a byte count cannot tell a
// drained empty stream from a fresh one.
type streamingChunkBody struct {
	readLength int64
	consumed   bool
	reader     io.Reader
}

func (c *streamingChunkBody) Read(p []byte) (n int, err error) {
	c.consumed = true
	n, err = c.reader.Read(p)
	c.readLength += int64(n)
	return
}

func (c *streamingChunkBody) WriteTo(w io.Writer) (n int64, err error) {
	c.consumed = true
	// count at the destination: io.Copy hands the copy to the reader's own
	// WriteTo when it has one, and the count that call returns is its claim.
	// readLength sizes the chunk in the output, so it holds the bytes that
	// actually arrived, whatever the reader reported
	cw := countingWriter{w: w}
	_, err = io.Copy(&cw, c.reader)
	c.readLength += cw.n
	return cw.n, err
}

// SectionSubChunk is a sub-chunk whose payload is a section of a seekable stream,
// read on demand. ReadSections creates these; a hand-made value needs a non-nil
// embedded *io.SectionReader, or BodySize and Body panic.
type SectionSubChunk struct {
	ID FourCC
	*io.SectionReader
}

var _ SubChunk = (*SectionSubChunk)(nil)

func (c *SectionSubChunk) ChunkID() FourCC { return c.ID }

func (c *SectionSubChunk) BodySize() int64 { return c.SectionReader.Size() }

// Body returns an independent reader over the section, leaving the embedded
// *io.SectionReader untouched so the chunk can be written repeatedly.
func (c *SectionSubChunk) Body() io.Reader {
	return io.NewSectionReader(c.SectionReader, 0, c.SectionReader.Size())
}

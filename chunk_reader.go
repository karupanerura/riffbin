package riffbin

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var (
	// ErrInvalidFormat is an error for invalid RIFF file format.
	ErrInvalidFormat = errors.New("invlaid format")
)

type PartialReader interface {
	io.ReadSeeker
	io.ReaderAt
}

// ReadFull reads RIFF binary from io.Reader.
// It creates *RIFFChunk with *OnMemorySubChunk for sub-chunks.
func ReadFull(r io.Reader) (*RIFFChunk, error) {
	return read(r, createOnMemorySubChunk)
}

// ReadSections reads RIFF binary from io.ReadSeeker to use less memory than ReadFull.
// It creates *RIFFChunk with *InStreamSubChunk for sub-chunks.
func ReadSections(r PartialReader) (*RIFFChunk, error) {
	return read(r, createInStreamSubChunk)
}

func read(r io.Reader, f subChunkConstructorFn) (*RIFFChunk, error) {
	var buf [HeaderBytes]byte

	// read header
	if _, err := io.ReadFull(r, buf[:]); err == io.EOF || err == io.ErrUnexpectedEOF {
		return nil, ErrInvalidFormat
	} else if err != nil {
		return nil, err
	}

	// verify id
	if !bytes.Equal(riffID[:], buf[:idBytes]) {
		return nil, ErrInvalidFormat
	}

	ch := groupedChunkHeader{id: riffID}
	rr := &io.LimitedReader{R: r, N: int64(binary.LittleEndian.Uint32(buf[idBytes:]))}
	chunk, err := readGroupedChunkBody(r, rr, &ch, f)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, ErrInvalidFormat
	} else if err != nil {
		return nil, err
	}

	// verify EOF (tolerate a single 0x00 after an odd-sized final chunk: some writers
	// append the alignment padding byte without counting it in the RIFF chunk size)
	if n, err := r.Read(buf[:1]); err == nil || (n == 1 && err == io.EOF) {
		if n != 1 || buf[0] != 0x00 {
			// too long payload (too small payload size)
			return nil, ErrInvalidFormat
		}
		if n, err = r.Read(buf[:1]); n != 0 || err != io.EOF {
			// too long payload (too small payload size)
			return nil, ErrInvalidFormat
		}
	} else if n == 0 && err == io.EOF {
		// OK
	} else {
		// any other I/O error is occurred
		return nil, err
	}

	return chunk.(*RIFFChunk), nil
}

type groupedChunkHeader struct {
	id        [idBytes]byte
	groupType [idBytes]byte
}

func (h *groupedChunkHeader) toGroupedChunk(payload []Chunk) groupedChunk {
	if bytes.Equal(listID[:], h.id[:]) {
		return &ListChunk{
			ListType: h.groupType,
			Payload:  payload,
		}
	}
	if bytes.Equal(riffID[:], h.id[:]) {
		return &RIFFChunk{
			FormType: h.groupType,
			Payload:  payload,
		}
	}

	panic("should not reach here")
}

func readGroupedChunkBody(src io.Reader, r *io.LimitedReader, chunk *groupedChunkHeader, f subChunkConstructorFn) (groupedChunk, error) {
	var buf [HeaderBytes]byte

	// read type
	if _, err := io.ReadFull(r, chunk.groupType[:typeBytes]); err != nil {
		return nil, err
	}
	if r.N == 0 {
		return chunk.toGroupedChunk([]Chunk{}), nil
	}

	// read sub-chunks
	var payload []Chunk
	pending := 0 // bytes of buf already holding the head of the next chunk header
	for r.N > 0 || pending > 0 {
		if _, err := io.ReadFull(r, buf[pending:]); err != nil {
			return nil, err
		}
		pending = 0
		bodyLen := binary.LittleEndian.Uint32(buf[idBytes:])

		// check wel-known id
		if bytes.Equal(listID[:], buf[:idBytes]) || bytes.Equal(riffID[:], buf[:idBytes]) {
			ch := groupedChunkHeader{}
			rr := &io.LimitedReader{R: r, N: int64(bodyLen)}
			copy(ch.id[:], buf[:idBytes])
			chunk, err := readGroupedChunkBody(src, rr, &ch, f)
			if err != nil {
				return nil, err
			}

			payload = append(payload, chunk)
		} else {
			// or not, this is a simple sub-chunk
			chunk, err := f(src, r, buf[:idBytes], bodyLen)
			if err != nil {
				return nil, fmt.Errorf("construct sub-chunk: %w", err)
			}

			payload = append(payload, chunk)
		}

		// RIFF word alignment: an odd-sized chunk is followed by a padding byte.
		// Files written by riffbin up to v0.0.6 lack the padding, so consume the next
		// byte only when it is 0x00; chunk IDs are printable ASCII, so a non-zero byte
		// must be the head of the next chunk header instead.
		if bodyLen%2 == 1 && r.N > 0 {
			if _, err := io.ReadFull(r, buf[:1]); err != nil {
				return nil, err
			}
			if buf[0] != 0x00 {
				pending = 1
			}
		}
	}

	return chunk.toGroupedChunk(payload), nil
}

type subChunkConstructorFn = func(src io.Reader, r *io.LimitedReader, id []byte, bodyLen uint32) (SubChunk, error)

func createOnMemorySubChunk(_ io.Reader, r *io.LimitedReader, id []byte, bodyLen uint32) (SubChunk, error) {
	chunk := &OnMemorySubChunk{}
	copy(chunk.ID[:], id)

	// read body payload
	chunk.Payload = make([]byte, bodyLen)
	if _, err := io.ReadFull(r, chunk.Payload); err != nil {
		return nil, err
	}

	return chunk, nil
}

func createInStreamSubChunk(src io.Reader, r *io.LimitedReader, id []byte, bodyLen uint32) (SubChunk, error) {
	pr := src.(PartialReader)

	// get seek position
	pos, err := pr.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, fmt.Errorf("get seek position: %w", err)
	}

	// skip sub-chunk body
	_, err = pr.Seek(int64(bodyLen), io.SeekCurrent)
	if err != nil {
		return nil, fmt.Errorf("seek: %w", err)
	}
	r.N -= int64(bodyLen)

	chunk := &InStreamSubChunk{SectionReader: io.NewSectionReader(pr, pos, int64(bodyLen))}
	copy(chunk.ID[:], id)
	return chunk, nil
}

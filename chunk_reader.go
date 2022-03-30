package riffbin

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

var (
	// ErrInvalidFormat is an error for invalid RIFF file format.
	ErrInvalidFormat = errors.New("invlaid format")
)

func ReadFull(r io.Reader) (*RIFFChunk, error) {
	var buf [4]byte

	// read id and verify
	if _, err := io.ReadFull(r, buf[:idBytes]); err == io.EOF || err == io.ErrUnexpectedEOF {
		return nil, ErrInvalidFormat
	} else if err != nil {
		return nil, err
	}
	if !bytes.Equal(riffID[:], buf[:idBytes]) {
		return nil, ErrInvalidFormat
	}

	ch := groupedChunkHeader{id: riffID}
	chunk, err := readGroupedChunkAfterID(r, &ch)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return nil, ErrInvalidFormat
	} else if err != nil {
		return nil, err
	}

	// verify EOF
	if n, err := r.Read(buf[:1]); err == nil {
		// too long payload (too small payload size)
		return nil, ErrInvalidFormat
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

func readGroupedChunkAfterID(r io.Reader, chunk *groupedChunkHeader) (groupedChunk, error) {
	var buf [4]byte

	// read body length
	if _, err := io.ReadFull(r, buf[:sizeBytes]); err != nil {
		return nil, err
	}
	bodyLen := binary.LittleEndian.Uint32(buf[:])

	// read type
	if _, err := io.ReadFull(r, chunk.groupType[:typeBytes]); err != nil {
		return nil, err
	}
	if bodyLen == 0 {
		return chunk.toGroupedChunk([]Chunk{}), nil
	}

	// read sub-chunks
	rr := &io.LimitedReader{R: r, N: int64(bodyLen)}
	var payload []Chunk
	for rr.N > 0 {
		if _, err := io.ReadFull(rr, buf[:idBytes]); err != nil {
			return nil, err
		}

		// check wel-known id
		if bytes.Equal(listID[:], buf[:idBytes]) || bytes.Equal(riffID[:], buf[:idBytes]) {
			ch := groupedChunkHeader{}
			copy(ch.id[:], buf[:idBytes])
			chunk, err := readGroupedChunkAfterID(rr, &ch)
			if err != nil {
				return nil, err
			}

			payload = append(payload, chunk)
		} else {
			// or not, this is a simple sub-chunk
			chunk := &CompletedSubChunk{}
			copy(chunk.ID[:], buf[:idBytes])

			// read body length
			if _, err := io.ReadFull(rr, buf[:sizeBytes]); err != nil {
				return nil, err
			}
			bodyLen := binary.LittleEndian.Uint32(buf[:])

			// read body payload
			chunk.Payload = make([]byte, bodyLen)
			if _, err := io.ReadFull(rr, chunk.Payload); err != nil {
				return nil, err
			}

			payload = append(payload, chunk)
		}
	}

	return chunk.toGroupedChunk(payload), nil
}

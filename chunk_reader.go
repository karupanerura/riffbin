package riffbin

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

var (
	// ErrInvalidFormat is an error for invalid RIFF file format.
	ErrInvalidFormat = errors.New("invlaid format")
)

// ReadFull reads RIFF binary from io.Reader.
// It creates *RIFFChunk with *OnMemorySubChunk for sub-chunks.
func ReadFull(r io.Reader) (*RIFFChunk, error) {
	return read(r, createOnMemorySubChunk)
}

// ReadFile reads RIFF binary from *os.File to use less memory than ReadFull.
// It creates *RIFFChunk with *FileSubChunk for sub-chunks.
func ReadFile(f *os.File) (*RIFFChunk, error) {
	return read(f, createFileSubChunk)
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
	chunk, err := readGroupedChunkBody(rr, &ch, f)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
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

func readGroupedChunkBody(r *io.LimitedReader, chunk *groupedChunkHeader, f subChunkConstructorFn) (groupedChunk, error) {
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
	for r.N > 0 {
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return nil, err
		}
		bodyLen := binary.LittleEndian.Uint32(buf[idBytes:])

		// check wel-known id
		if bytes.Equal(listID[:], buf[:idBytes]) || bytes.Equal(riffID[:], buf[:idBytes]) {
			ch := groupedChunkHeader{}
			rr := &io.LimitedReader{R: r, N: int64(bodyLen)}
			copy(ch.id[:], buf[:idBytes])
			chunk, err := readGroupedChunkBody(rr, &ch, f)
			if err != nil {
				return nil, err
			}

			payload = append(payload, chunk)
		} else {
			// or not, this is a simple sub-chunk
			chunk, err := f(buf[:idBytes], bodyLen, r)
			if err != nil {
				return nil, fmt.Errorf("construct sub-chunk: %w", err)
			}

			payload = append(payload, chunk)
		}
	}

	return chunk.toGroupedChunk(payload), nil
}

type subChunkConstructorFn = func(id []byte, bodyLen uint32, r *io.LimitedReader) (SubChunk, error)

func createOnMemorySubChunk(id []byte, bodyLen uint32, r *io.LimitedReader) (SubChunk, error) {
	chunk := &OnMemorySubChunk{}
	copy(chunk.ID[:], id)

	// read body payload
	chunk.Payload = make([]byte, bodyLen)
	if _, err := io.ReadFull(r, chunk.Payload); err != nil {
		return nil, err
	}

	return chunk, nil
}

func createFileSubChunk(id []byte, bodyLen uint32, r *io.LimitedReader) (SubChunk, error) {
	// get source file object
	rr := r.R
	for {
		if lr, ok := rr.(*io.LimitedReader); ok {
			rr = lr.R
		} else if rs, ok := rr.(*os.File); ok {
			rr = rs
			break
		} else {
			panic("unexpected sequence")
		}
	}
	src := rr.(*os.File)

	// re-open file to emulate dup(2)
	f, err := os.Open(src.Name())
	if err != nil {
		return nil, fmt.Errorf("re-open File %s: %w", src.Name(), err)
	}

	// copy seek position
	if pos, err := src.Seek(0, io.SeekCurrent); err != nil {
		return nil, fmt.Errorf("get seek position: %w", err)
	} else {
		_, err = f.Seek(pos, io.SeekStart)
		if err != nil {
			return nil, fmt.Errorf("seek: %w", err)
		}
	}

	// skip seek position to next chunk
	if _, err := src.Seek(int64(bodyLen), io.SeekCurrent); err != nil {
		return nil, fmt.Errorf("seek: %w", err)
	}
	r.N -= int64(bodyLen)

	chunk := &FileSubChunk{Size: bodyLen, File: f}
	copy(chunk.ID[:], id)
	return chunk, nil
}

func CloseAllIncludedFileSubChunkFiles(chunk Chunk) error {
	switch c := chunk.(type) {
	case groupedChunk:
		var mErr multiError
		for _, chunk := range c.payload() {
			err := CloseAllIncludedFileSubChunkFiles(chunk)
			if err != nil {
				mErr = append(mErr, err)
			}
		}

		if len(mErr) == 0 {
			return nil
		}
		return mErr
	case *FileSubChunk:
		return c.File.Close()
	default:
		return nil
	}
}

type multiError []error

func (e multiError) Error() string {
	b := strings.Builder{}
	for i, err := range e {
		if i != 0 {
			b.WriteString(", ")
		}
		b.WriteString(err.Error())
	}

	return b.String()
}

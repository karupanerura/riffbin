package riffbin

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var ErrUnexpectedIncompleteChunk = errors.New("unexpected incomplete chunk")

// ChunkWriter is a interface for RIFF chunk writer.
type ChunkWriter interface {
	// Write writes the RIFF message to the underlying data stream.
	// It returns the number of bytes written and any error encountered that caused the write to stop early. (same as Write of io.Writer)
	Write(*RIFFChunk) (int64, error)
}

// CompletedChunkWriter is a RIFF chunk writer for the completed chunk.
type CompletedChunkWriter struct {
	w io.Writer
}

var _ ChunkWriter = (*CompletedChunkWriter)(nil)

func NewCompletedChunkWriter(w io.Writer) *CompletedChunkWriter {
	return &CompletedChunkWriter{w: w}
}

// Write writes the RIFF message to the underlying data stream.
// It returns the number of bytes written and any error encountered that caused the write to stop early. (same as Write of io.Writer)
func (w *CompletedChunkWriter) Write(c *RIFFChunk) (int64, error) {
	return writeChunk(w.w, c, false)
}

// IncompleteChunkWriter is a RIFF chunk writer for the incomplete chunk.
type IncompleteChunkWriter struct {
	w    io.WriteSeeker
	head int64
}

var _ ChunkWriter = (*IncompleteChunkWriter)(nil)

// NewIncompleteChunkWriter creates a new IncompleteChunkWriter.
func NewIncompleteChunkWriter(w io.WriteSeeker) (*IncompleteChunkWriter, error) {
	pos, err := w.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, fmt.Errorf("seek: %w", err)
	}

	return &IncompleteChunkWriter{w: w, head: pos}, nil
}

// Write writes the RIFF message to the underlying data stream, and re-write the bytes of the all chunk headers size to fix incomplete body bytes by random write.
// It returns the number of bytes written and any error encountered that caused the write to stop early. (same as Write of io.Writer)
func (w *IncompleteChunkWriter) Write(c *RIFFChunk) (n int64, err error) {
	n, err = writeChunk(w.w, c, true)
	if err != nil {
		err = fmt.Errorf("writeChunk at first: %w", err)
		return
	}

	// XXX: shared state for absolute seek position
	posState := w.head

	var chunkBodyRandomWriter func(b uint32) error
	if ww, ok := w.w.(io.WriterAt); ok {
		// io.WriterAt for optimize
		chunkBodyRandomWriter = func(b uint32) error {
			_, err := writeChunkBodySizeAt(ww, b, posState)
			if err != nil {
				return fmt.Errorf("writeChunkBodySizeAt: %w", err)
			}

			return nil
		}
	} else {
		// revert seek position
		defer func() {
			_, err = w.w.Seek(w.head+n, io.SeekStart)
			if err != nil {
				err = fmt.Errorf("seek: %w", err)
			}
		}()

		// random write by io.WriteSeeker
		chunkBodyRandomWriter = func(b uint32) error {
			_, err := w.w.Seek(posState, io.SeekStart)
			if err != nil {
				return fmt.Errorf("seek: %w", err)
			}

			_, err = writeChunkBodySize(w.w, b)
			if err != nil {
				return fmt.Errorf("writeChunkBodySizeAt: %w", err)
			}

			return nil
		}
	}

	// write complete to re-write finally fixed body size
	err = writeComplete(c, &posState, chunkBodyRandomWriter)
	if err != nil {
		err = fmt.Errorf("write complete: %w", err)
		return
	}

	return
}

func writeComplete(c Chunk, pos *int64, f func(b uint32) error) error {
	*pos += idBytes
	b := c.BodySize()
	err := f(b)
	if err != nil {
		return err
	}
	*pos += sizeBytes

	switch cc := c.(type) {
	case groupedChunk:
		*pos += typeBytes
		for _, p := range cc.payload() {
			err := writeComplete(p, pos, f)
			if err != nil {
				return err
			}
		}
	case SubChunk:
		*pos += int64(b)
	}

	return nil
}

func writeChunk(w io.Writer, c Chunk, allowIncomplete bool) (n int64, err error) {
	n, err = writeChunkHeader(w, c)
	if err != nil {
		err = fmt.Errorf("chunk[%q] header: %w", string(c.ChunkID()), err)
		return
	}

	var nn int64
	nn, err = writeChunkBody(w, c, allowIncomplete)
	n += nn
	if err != nil {
		err = fmt.Errorf("chunk[%q] body: %w", string(c.ChunkID()), err)
		return
	}

	return
}

func writeChunkHeader(w io.Writer, c Chunk) (n int64, err error) {
	var nn int

	nn, err = w.Write(c.ChunkID())
	n = int64(nn)
	if err != nil {
		err = fmt.Errorf("id: %w", err)
		return
	}

	nn, err = writeChunkBodySize(w, c.BodySize())
	n += int64(nn)
	if err != nil {
		err = fmt.Errorf("size: %w", err)
		return
	}

	if cc, ok := c.(groupedChunk); ok {
		nn, err = w.Write(cc.groupType())
		n += int64(nn)
		if err != nil {
			err = fmt.Errorf("type: %w", err)
			return
		}
	}

	return
}

func writeChunkBodySize(w io.Writer, b uint32) (int, error) {
	var buf [sizeBytes]byte
	binary.LittleEndian.PutUint32(buf[:], b)
	return w.Write(buf[:])
}

func writeChunkBodySizeAt(w io.WriterAt, b uint32, off int64) (int, error) {
	var buf [sizeBytes]byte
	binary.LittleEndian.PutUint32(buf[:], b)
	return w.WriteAt(buf[:], off)
}

func writeChunkBody(w io.Writer, c Chunk, allowIncomplete bool) (n int64, err error) {
	switch cc := c.(type) {
	case groupedChunk:
		var nn int64
		for i, p := range cc.payload() {
			nn, err = writeChunk(w, p, allowIncomplete)
			n += nn
			if err != nil {
				err = fmt.Errorf("payload[%d]: %w", i, err)
				return
			}
		}
	case SubChunk:
		if !allowIncomplete && cc.Incomplete() {
			err = ErrUnexpectedIncompleteChunk
			return
		}

		n, err = io.Copy(w, cc)
	default:
		panic(fmt.Sprintf("unknown chunk type: %+v", c))
	}
	return
}

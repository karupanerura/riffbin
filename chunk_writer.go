package riffbin

import (
	"encoding/binary"
	"fmt"
	"io"
)

// ChunkWriter is a interface for RIFF chunk writer.
type ChunkWriter interface {
	// WriteChunk writes the RIFF message to the underlying data stream.
	// It returns the number of bytes written and any error encountered that caused the write to stop early. (same as Write of io.Writer)
	WriteChunk(*RIFFChunk) (int64, error)
}

// CompletedChunkWriter is a RIFF chunk writer for the completed chunk.
type CompletedChunkWriter struct {
	w io.Writer
}

var _ ChunkWriter = (*CompletedChunkWriter)(nil)

func NewCompletedChunkWriter(w io.Writer) *CompletedChunkWriter {
	return &CompletedChunkWriter{w: w}
}

// WriteChunk writes the RIFF message to the underlying data stream.
// It returns the number of bytes written and any error encountered that caused the write to stop early. (same as Write of io.Writer)
func (w *CompletedChunkWriter) WriteChunk(c *RIFFChunk) (int64, error) {
	return writeChunk(w.w, c, c.ByteOrder.binary(), false)
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

// WriteChunk writes the RIFF message to the underlying data stream, and re-write the bytes of the all chunk headers size to fix incomplete body bytes by random write.
// It returns the number of bytes written and any error encountered that caused the write to stop early. (same as Write of io.Writer)
func (w *IncompleteChunkWriter) WriteChunk(c *RIFFChunk) (n int64, err error) {
	order := c.ByteOrder.binary()

	n, err = writeChunk(w.w, c, order, true)
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
			_, err := writeChunkBodySizeAt(ww, order, b, posState)
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

			_, err = writeChunkBodySize(w.w, order, b)
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
	*pos += IDBytes
	b, err := chunkBodySize(c)
	if err != nil {
		return err
	}
	err = f(b)
	if err != nil {
		return err
	}
	*pos += SizeBytes

	switch cc := c.(type) {
	case GroupedChunk:
		*pos += TypeBytes
		for _, p := range cc.SubChunks() {
			err := writeComplete(p, pos, f)
			if err != nil {
				return err
			}
		}
	case SubChunk:
		*pos += int64(b) + int64(b&1) // skip the padding byte after an odd-sized body
	default:
		return unsupportedChunkTypeError(c)
	}

	return nil
}

var paddingByte = [1]byte{0x00}

func writeChunk(w io.Writer, c Chunk, order binary.ByteOrder, allowIncomplete bool) (n int64, err error) {
	n, err = writeChunkHeader(w, c, order)
	if err != nil {
		err = fmt.Errorf("chunk[%q] header: %w", c.ChunkID(), err)
		return
	}

	var nn int64
	nn, err = writeChunkBody(w, c, order, allowIncomplete)
	n += nn
	if err != nil {
		err = fmt.Errorf("chunk[%q] body: %w", c.ChunkID(), err)
		return
	}

	// RIFF word alignment: an odd-sized chunk body is followed by a padding byte.
	// The padding is not counted in the chunk's own size, but is counted in the parent's size.
	// Grouped chunks are always even-sized because their children are padded.
	if nn%2 == 1 {
		var pn int
		pn, err = w.Write(paddingByte[:])
		n += int64(pn)
		if err != nil {
			err = fmt.Errorf("chunk[%q] padding: %w", c.ChunkID(), err)
			return
		}
	}

	return
}

func writeChunkHeader(w io.Writer, c Chunk, order binary.ByteOrder) (n int64, err error) {
	var b uint32
	b, err = chunkBodySize(c)
	if err != nil {
		return
	}

	var nn int
	id := c.ChunkID()
	nn, err = w.Write(id[:])
	n = int64(nn)
	if err != nil {
		err = fmt.Errorf("id: %w", err)
		return
	}

	nn, err = writeChunkBodySize(w, order, b)
	n += int64(nn)
	if err != nil {
		err = fmt.Errorf("size: %w", err)
		return
	}

	if cc, ok := c.(GroupedChunk); ok {
		groupType := cc.GroupType()
		nn, err = w.Write(groupType[:])
		n += int64(nn)
		if err != nil {
			err = fmt.Errorf("type: %w", err)
			return
		}
	}

	return
}

// chunkBodySize converts the declared body size to the on-disk 32-bit size field,
// rejecting sizes that the RIFF format cannot express instead of silently wrapping around.
func chunkBodySize(c Chunk) (uint32, error) {
	b := c.BodySize()
	if b < 0 {
		return 0, fmt.Errorf("%w: chunk[%q] reports a negative body size %d", ErrSizeMismatch, c.ChunkID(), b)
	}
	if b > MaxBodySize {
		return 0, fmt.Errorf("%w: chunk[%q] body is %d bytes", ErrChunkTooLarge, c.ChunkID(), b)
	}
	return uint32(b), nil
}

func writeChunkBodySize(w io.Writer, order binary.ByteOrder, b uint32) (int, error) {
	var buf [SizeBytes]byte
	order.PutUint32(buf[:], b)
	return w.Write(buf[:])
}

func writeChunkBodySizeAt(w io.WriterAt, order binary.ByteOrder, b uint32, off int64) (int, error) {
	var buf [SizeBytes]byte
	order.PutUint32(buf[:], b)
	return w.WriteAt(buf[:], off)
}

func writeChunkBody(w io.Writer, c Chunk, order binary.ByteOrder, allowIncomplete bool) (n int64, err error) {
	switch cc := c.(type) {
	case GroupedChunk:
		var nn int64
		for i, p := range cc.SubChunks() {
			nn, err = writeChunk(w, p, order, allowIncomplete)
			n += nn
			if err != nil {
				err = fmt.Errorf("payload[%d]: %w", i, err)
				return
			}
		}
	case SubChunk:
		if cc.Incomplete() {
			if !allowIncomplete {
				err = ErrUnexpectedIncompleteChunk
				return
			}

			// the body size of an incomplete chunk is only known once it has been read,
			// so there is nothing to verify it against
			n, err = io.Copy(w, cc.Body())
			return
		}

		want := cc.BodySize()
		n, err = io.Copy(w, cc.Body())
		if err != nil {
			return
		}
		if n != want {
			// the header has already been written with the declared size, so carrying on
			// would emit a corrupt file
			err = fmt.Errorf("%w: chunk[%q] declares %d bytes but produced %d", ErrSizeMismatch, cc.ChunkID(), want, n)
			return
		}
	default:
		err = unsupportedChunkTypeError(c)
	}
	return
}

func unsupportedChunkTypeError(c Chunk) error {
	return fmt.Errorf("%w: %T is neither a GroupedChunk nor a SubChunk", ErrUnsupportedChunkType, c)
}

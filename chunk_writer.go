package riffbin

import (
	"encoding/binary"
	"fmt"
	"io"
)

// ChunkWriter is the interface shared by the chunk writers: it writes a whole
// RIFF chunk tree to an underlying data stream.
type ChunkWriter interface {
	// WriteChunk writes the RIFF chunk tree to the underlying data stream.
	// It returns the number of bytes written and any error encountered that caused the write to stop early.
	WriteChunk(*RIFFChunk) (int64, error)
}

// Writer writes chunk trees whose sizes are all known up front,
// in a single forward pass. A tree holding a StreamingSubChunk is rejected
// with ErrUnexpectedStreamingChunk; use StreamingWriter for those.
type Writer struct {
	w io.Writer
}

var _ ChunkWriter = (*Writer)(nil)

// NewWriter returns a writer that writes chunk trees to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// WriteChunk writes the RIFF chunk tree to the underlying data stream.
// It returns the number of bytes written and any error encountered that caused the write to stop early.
func (w *Writer) WriteChunk(c *RIFFChunk) (int64, error) {
	if err := validateTree(c, false); err != nil {
		return 0, err
	}
	return writeChunk(w.w, c, c.ByteOrder.binary(), false)
}

// StreamingWriter writes chunk trees that may hold StreamingSubChunk
// values, whose sizes are unknown until their body streams are drained. It
// writes the tree with placeholder sizes first, then seeks back and re-writes
// the size fields — which is why it needs an io.WriteSeeker. It uses io.WriterAt
// instead for the fix-up when w provides it.
type StreamingWriter struct {
	w io.WriteSeeker
}

var _ ChunkWriter = (*StreamingWriter)(nil)

// NewStreamingWriter returns a writer that writes chunk trees to w.
// A w that cannot actually seek is rejected here, before anything is written.
func NewStreamingWriter(w io.WriteSeeker) (*StreamingWriter, error) {
	if _, err := w.Seek(0, io.SeekCurrent); err != nil {
		return nil, fmt.Errorf("seek: %w", err)
	}

	return &StreamingWriter{w: w}, nil
}

// WriteChunk writes the RIFF chunk tree to the underlying data stream, then seeks back
// and re-writes every chunk size field once the streaming bodies have been consumed.
// It returns the number of bytes written and any error encountered that caused the write to stop early.
func (w *StreamingWriter) WriteChunk(c *RIFFChunk) (n int64, err error) {
	if err = validateTree(c, true); err != nil {
		return 0, err
	}

	// the size backfill is relative to wherever this chunk starts, so a writer can be
	// used for several consecutive chunks
	start, err := w.w.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, fmt.Errorf("seek: %w", err)
	}

	order := c.ByteOrder.binary()

	n, err = writeChunk(w.w, c, order, true)
	if err != nil {
		err = fmt.Errorf("writeChunk at first: %w", err)
		return
	}

	// XXX: shared state for absolute seek position
	posState := start

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
		// revert seek position without masking an error from the backfill below
		defer func() {
			if _, seekErr := w.w.Seek(start+n, io.SeekStart); seekErr != nil && err == nil {
				err = fmt.Errorf("seek: %w", seekErr)
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
		for _, p := range cc.Children() {
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

// validateTree checks, before a single byte is written, that the tree can be written as
// a RIFF file the readers accept. The readers dispatch on chunk IDs, so a sub-chunk using
// a structural ID or a nested RIFF chunk would be read back as a different structure; they
// also refuse chunks nested deeper than maxGroupDepth, so such a tree is rejected here
// with an error instead of exhausting the stack.
func validateTree(c *RIFFChunk, allowStreaming bool) error {
	return validateChunk(c, true, allowStreaming, 0, map[SubChunk]struct{}{})
}

// validateChunk validates one chunk and its subtree; streamed collects every
// streaming sub-chunk seen so far, so one placed twice in the tree is caught
// here — the write of its second occurrence would find the stream drained.
func validateChunk(c Chunk, root, allowStreaming bool, depth int, streamed map[SubChunk]struct{}) error {
	id := c.ChunkID()
	if !id.Valid() {
		return fmt.Errorf("%w: chunk ID %q is not printable ASCII", ErrUnwritableChunk, id[:])
	}

	switch cc := c.(type) {
	case GroupedChunk:
		if depth >= maxGroupDepth {
			return fmt.Errorf("%w: chunks are nested deeper than %d levels", ErrUnwritableChunk, maxGroupDepth)
		}
		if groupType := cc.GroupType(); !groupType.Valid() {
			return fmt.Errorf("%w: group type %q of the %s chunk is not printable ASCII", ErrUnwritableChunk, groupType[:], id)
		}
		if root {
			if id != riffID && id != rifxID {
				return fmt.Errorf("%w: root chunk ID is %q, want %q or %q", ErrUnwritableChunk, id, riffID, rifxID)
			}
		} else if id != listID {
			return fmt.Errorf("%w: a %s chunk must not be nested", ErrUnwritableChunk, id)
		}
		for _, p := range cc.Children() {
			if err := validateChunk(p, false, allowStreaming, depth+1, streamed); err != nil {
				return err
			}
		}
	case SubChunk:
		switch id {
		case riffID, rifxID, listID:
			return fmt.Errorf("%w: %s is a grouped-chunk ID but the chunk is a sub-chunk", ErrUnwritableChunk, id)
		}
		if cc.Streaming() {
			if !allowStreaming {
				return ErrUnexpectedStreamingChunk
			}
			if _, dup := streamed[cc]; dup {
				return fmt.Errorf("%w: chunk[%q] is placed more than once in the tree; its stream would already be drained at the second occurrence", ErrConsumedStreamingChunk, id)
			}
			streamed[cc] = struct{}{}
			if b := cc.BodySize(); b != 0 {
				return fmt.Errorf("%w: chunk[%q] reports %d byte(s) before being written", ErrConsumedStreamingChunk, id, b)
			}
		}
	default:
		return unsupportedChunkTypeError(c)
	}

	// the size check runs after the subtree is validated: computing a grouped
	// chunk's size recurses through it, which is only safe once the depth check
	// above has bounded the nesting
	if _, err := chunkBodySize(c); err != nil {
		return err
	}
	return nil
}

var paddingByte = [1]byte{0x00}

func writeChunk(w io.Writer, c Chunk, order binary.ByteOrder, allowStreaming bool) (n int64, err error) {
	n, err = writeChunkHeader(w, c, order)
	if err != nil {
		err = fmt.Errorf("chunk[%q] header: %w", c.ChunkID(), err)
		return
	}

	var nn int64
	nn, err = writeChunkBody(w, c, order, allowStreaming)
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

func writeChunkBody(w io.Writer, c Chunk, order binary.ByteOrder, allowStreaming bool) (n int64, err error) {
	switch cc := c.(type) {
	case GroupedChunk:
		var nn int64
		for i, p := range cc.Children() {
			nn, err = writeChunk(w, p, order, allowStreaming)
			n += nn
			if err != nil {
				err = fmt.Errorf("payload[%d]: %w", i, err)
				return
			}
		}
	case SubChunk:
		if cc.Streaming() {
			if !allowStreaming {
				err = ErrUnexpectedStreamingChunk
				return
			}

			// the body size of a streaming chunk is only known once it has been
			// read; right after the copy it is known, and must equal the bytes
			// the copy produced — it does not when the stream was consumed by an
			// earlier occurrence of the same chunk in this tree, or when a custom
			// implementation misreports. writeComplete backfills whatever
			// BodySize reports, so a divergence here would corrupt the output.
			n, err = io.Copy(w, cc.Body())
			if err != nil {
				return
			}
			if b := cc.BodySize(); b != n {
				err = fmt.Errorf("%w: chunk[%q] produced %d byte(s) but reports %d after draining", ErrSizeMismatch, cc.ChunkID(), n, b)
			}
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

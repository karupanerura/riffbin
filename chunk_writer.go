package riffbin

import (
	"encoding/binary"
	"errors"
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
	plan, order, err := buildPlan(c, false)
	if err != nil {
		return 0, err
	}
	cnt := &countingWriter{w: w.w}
	err = writePlan(cnt, &plan, order, cnt, nil)
	return cnt.n, err
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
	plan, order, err := buildPlan(c, true)
	if err != nil {
		return 0, err
	}

	// the size backfill is relative to wherever this chunk starts, so a writer can be
	// used for several consecutive chunks
	start, err := w.w.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, fmt.Errorf("seek: %w", err)
	}

	// the first pass writes the tree with zero placeholders for the streaming
	// sizes, recording for every chunk where its size field sits and how many
	// bytes its body actually encoded to. It is capped at the largest file a
	// RIFF chunk can be — the header plus a full 32-bit body: a streaming body
	// has no declared size to bound its copy, but past this bound failure is
	// inevitable, so the write stops right there instead of draining the
	// rest of the stream.
	cnt := &countingWriter{w: w.w}
	var fixes []sizeFix
	err = writePlan(&cappedWriter{w: cnt, remaining: HeaderBytes + MaxBodySize, sentinel: errFileBoundExceeded}, &plan, order, cnt, &fixes)
	n = cnt.n
	if err != nil {
		if errors.Is(err, errFileBoundExceeded) {
			err = fmt.Errorf("%w: %w", ErrChunkTooLarge, err)
			return
		}
		err = fmt.Errorf("writeChunk at first: %w", err)
		return
	}

	// the backfill rewrites the recorded facts and consults the tree no
	// further, so nothing a Chunk implementation reports after the first pass
	// can push a size field away from the bytes actually written
	if ww, ok := w.w.(io.WriterAt); ok {
		for _, fix := range fixes {
			if _, err = writeChunkBodySizeAt(ww, order, fix.bodySize, start+fix.fieldOff); err != nil {
				err = fmt.Errorf("writeChunkBodySizeAt: %w", err)
				return
			}
		}
		return
	}

	// revert the seek position without masking an error from the backfill below
	defer func() {
		if _, seekErr := w.w.Seek(start+n, io.SeekStart); seekErr != nil && err == nil {
			err = fmt.Errorf("seek: %w", seekErr)
		}
	}()
	for _, fix := range fixes {
		if _, err = w.w.Seek(start+fix.fieldOff, io.SeekStart); err != nil {
			err = fmt.Errorf("seek: %w", err)
			return
		}
		if _, err = writeChunkBodySize(w.w, order, fix.bodySize); err != nil {
			err = fmt.Errorf("writeChunkBodySize: %w", err)
			return
		}
	}
	return
}

// sizeFix records where a chunk's four-byte size field sits — relative to the
// start of the tree being written — and the size its body actually encoded to.
// The backfill rewrites these facts; it re-derives nothing from the tree.
type sizeFix struct {
	fieldOff int64
	bodySize uint32
}

// planChunk is the writers' snapshot of one chunk: every fact a write needs,
// read from the tree exactly once and owned by the library. The write pass
// consults only the plan, so ids, sizes, structure and recursion depth cannot
// change under it, whatever the tree's methods return once planning is over.
// The only calls back into caller code after planning are Body() on a
// non-streaming leaf — bounded by the planned size — and the drain of a
// captured streaming body.
type planChunk struct {
	id        FourCC
	isGroup   bool
	groupType FourCC      // groups only
	children  []planChunk // groups only

	// declared is the size the header carries: a leaf's BodySize read once, a
	// zero placeholder for a streaming leaf until the backfill, and for a
	// group the sum of what its planned children will occupy — a group's size
	// is derived, never asked of the tree, so a group header always agrees
	// with the bytes the write pass emits below it.
	declared uint32

	src    SubChunk            // non-streaming leaves: the Body() source
	stream *streamingChunkBody // streaming leaves: the stream captured at plan time
}

// buildPlan checks, before a single byte is written, that the tree can be
// written as a RIFF file the readers accept, and snapshots it into the plan
// the write pass trusts. The readers dispatch on chunk IDs, so a sub-chunk
// using a structural ID or a nested RIFF chunk would be read back as a
// different structure; they also refuse chunks nested deeper than
// maxGroupDepth, so such a tree is rejected here with an error instead of
// exhausting the stack.
func buildPlan(c *RIFFChunk, allowStreaming bool) (planChunk, binary.ByteOrder, error) {
	// the byte order is read once and decides both the root chunk ID and the
	// order of every size field, so the two cannot disagree
	bo := c.ByteOrder
	var p planChunk
	if err := planTree(c, true, allowStreaming, 0, map[*streamingChunkBody]struct{}{}, &p); err != nil {
		return planChunk{}, nil, err
	}
	if bo == BigEndian {
		p.id = rifxID
	} else {
		p.id = riffID
	}
	return p, bo.binary(), nil
}

// planTree validates one chunk and snapshots its subtree into p, which the
// caller has already placed in its parent's plan. streamed collects the body
// of every streaming sub-chunk seen so far, so one placed twice in the tree is
// caught here — the write of its second occurrence would find the stream
// drained. The bodies are library-owned pointers, so nothing is assumed about
// the chunk values themselves: a custom SubChunk does not have to be
// comparable.
func planTree(c Chunk, root, allowStreaming bool, depth int, streamed map[*streamingChunkBody]struct{}, p *planChunk) (err error) {
	p.id = c.ChunkID()
	if !p.id.Valid() {
		return fmt.Errorf("%w: chunk ID %q is not printable ASCII", ErrUnwritableChunk, p.id[:])
	}

	switch cc := c.(type) {
	case GroupedChunk:
		p.isGroup = true
		if depth >= maxGroupDepth {
			return fmt.Errorf("%w: chunks are nested deeper than %d levels", ErrUnwritableChunk, maxGroupDepth)
		}
		p.groupType = cc.GroupType()
		if !p.groupType.Valid() {
			return fmt.Errorf("%w: group type %q of the %s chunk is not printable ASCII", ErrUnwritableChunk, p.groupType[:], p.id)
		}
		if root {
			if p.id != riffID && p.id != rifxID {
				return fmt.Errorf("%w: root chunk ID is %q, want %q or %q", ErrUnwritableChunk, p.id, riffID, rifxID)
			}
		} else if p.id != listID {
			return fmt.Errorf("%w: a %s chunk must not be nested", ErrUnwritableChunk, p.id)
		}
		children := cc.Children()
		p.children = make([]planChunk, len(children))
		// a group's size is derived from its planned children — the tree is
		// never asked for it, so a custom implementation cannot misreport it.
		// The sum is checked against the size field's bound at every step, and
		// each planned size fits in 32 bits, so it stays far from overflowing
		declared := int64(TypeBytes)
		for i, child := range children {
			cp := &p.children[i]
			if cerr := planTree(child, false, allowStreaming, depth+1, streamed, cp); cerr != nil {
				return cerr
			}
			// what the child will occupy on disk: its header, its planned body
			// and its word-alignment pad byte
			declared += HeaderBytes + int64(cp.declared) + int64(cp.declared&1)
			if declared > MaxBodySize {
				return fmt.Errorf("%w: chunk[%q] body is %d bytes", ErrChunkTooLarge, p.id, declared)
			}
		}
		p.declared = uint32(declared)
	case SubChunk:
		switch p.id {
		case riffID, rifxID, listID:
			return fmt.Errorf("%w: %s is a grouped-chunk ID but the chunk is a sub-chunk", ErrUnwritableChunk, p.id)
		}
		if sc, ok := cc.(streamer); ok {
			if !allowStreaming {
				return ErrUnexpectedStreamingChunk
			}
			body := sc.streamingBody()
			if body.consumed {
				return fmt.Errorf("%w: chunk[%q] stream was already consumed after producing %d byte(s)", ErrConsumedStreamingChunk, p.id, body.readLength)
			}
			if _, dup := streamed[body]; dup {
				return fmt.Errorf("%w: chunk[%q] is placed more than once in the tree; its stream would already be drained at the second occurrence", ErrConsumedStreamingChunk, p.id)
			}
			streamed[body] = struct{}{}
			p.stream = body
			// the header goes out with the zero placeholder in p.declared and
			// the backfill sizes the chunk from the bytes its stream produces
			return nil
		}
		p.src = cc
		p.declared, err = clampBodySize(p.id, cc.BodySize())
		if err != nil {
			return err
		}
	default:
		return unsupportedChunkTypeError(c)
	}
	return nil
}

var paddingByte = [1]byte{0x00}

// writePlan writes one planned chunk through w. cnt is the measurement every
// size and offset is derived from: it counts the bytes the destination
// accepted since the start of the tree being written, so nothing a body's
// WriteTo claims can move them. With rec non-nil it appends a sizeFix for
// this chunk and every chunk below it, recording the body sizes actually
// written.
func writePlan(w io.Writer, p *planChunk, order binary.ByteOrder, cnt *countingWriter, rec *[]sizeFix) (err error) {
	fieldOff := cnt.n + IDBytes
	if err = writePlanHeader(w, p, order); err != nil {
		return fmt.Errorf("chunk[%q] header: %w", p.id, err)
	}

	bodyStart := cnt.n
	switch {
	case p.isGroup:
		for i := range p.children {
			if err = writePlan(w, &p.children[i], order, cnt, rec); err != nil {
				return fmt.Errorf("chunk[%q] body: payload[%d]: %w", p.id, i, err)
			}
		}
	case p.stream != nil:
		// the stream is drained through the library-owned body captured at
		// plan time — not the overridable Body — and the backfill writes the
		// size fields from the bytes this copy delivers, so nothing the chunk
		// reports can desynchronize them from the output
		if _, err = io.Copy(w, p.stream); err != nil {
			return fmt.Errorf("chunk[%q] body: %w", p.id, err)
		}
	default:
		if err = writePlanLeafBody(w, p); err != nil {
			return fmt.Errorf("chunk[%q] body: %w", p.id, err)
		}
	}
	body := cnt.n - bodyStart

	// RIFF word alignment: an odd-sized chunk body is followed by a padding byte.
	// The padding is not counted in the chunk's own size, but is counted in the parent's size.
	// Grouped chunks are always even-sized because their children are padded.
	if body%2 == 1 {
		if _, err = w.Write(paddingByte[:]); err != nil {
			return fmt.Errorf("chunk[%q] padding: %w", p.id, err)
		}
	}

	if rec != nil {
		if p.isGroup {
			// the group type went out with the header but counts into the size field
			body += TypeBytes
		}
		if body > MaxBodySize {
			return fmt.Errorf("%w: chunk[%q] body is %d bytes", ErrChunkTooLarge, p.id, body)
		}
		*rec = append(*rec, sizeFix{fieldOff: fieldOff, bodySize: uint32(body)})
	}
	return nil
}

func writePlanHeader(w io.Writer, p *planChunk, order binary.ByteOrder) error {
	if _, err := w.Write(p.id[:]); err != nil {
		return fmt.Errorf("id: %w", err)
	}
	if _, err := writeChunkBodySize(w, order, p.declared); err != nil {
		return fmt.Errorf("size: %w", err)
	}
	if p.isGroup {
		if _, err := w.Write(p.groupType[:]); err != nil {
			return fmt.Errorf("type: %w", err)
		}
	}
	return nil
}

// clampBodySize converts a reported body size to the on-disk 32-bit size field,
// rejecting sizes that the RIFF format cannot express instead of silently wrapping around.
func clampBodySize(id FourCC, b int64) (uint32, error) {
	if b < 0 {
		return 0, fmt.Errorf("%w: chunk[%q] reports a negative body size %d", ErrSizeMismatch, id, b)
	}
	if b > MaxBodySize {
		return 0, fmt.Errorf("%w: chunk[%q] body is %d bytes", ErrChunkTooLarge, id, b)
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

// writePlanLeafBody copies a non-streaming leaf body, verified against the
// very size its header carried: the header already went out with the planned
// size, so a byte past it is a defect no matter what follows — the copy is
// capped right there, and an endless body cannot flood the output. The copy
// is judged by the bytes the cap actually let through, never by io.Copy's
// count, which is whatever the body's own WriteTo returned: a short body is
// caught just after however it is misreported, and either way the write
// stops rather than emit a corrupt file.
func writePlanLeafBody(w io.Writer, p *planChunk) error {
	want := int64(p.declared)
	cw := &cappedWriter{w: w, remaining: want, sentinel: errDeclaredSizeExceeded}
	if _, err := io.Copy(cw, p.src.Body()); err != nil {
		if errors.Is(err, errDeclaredSizeExceeded) {
			return fmt.Errorf("%w: chunk[%q] declares %d byte(s) but its body produced more", ErrSizeMismatch, p.id, want)
		}
		return err
	}
	if n := want - cw.remaining; n != want {
		return fmt.Errorf("%w: chunk[%q] declares %d bytes but produced %d", ErrSizeMismatch, p.id, want, n)
	}
	return nil
}

// errDeclaredSizeExceeded and errFileBoundExceeded are the sentinels the two
// write caps fail with. They are distinct so a leaf overrunning its declared
// size is told apart from a tree outgrowing the RIFF file bound — the caps
// nest, and the copy loop cannot tell otherwise.
var (
	errDeclaredSizeExceeded = errors.New("declared body size exceeded")
	errFileBoundExceeded    = errors.New("output crossed the RIFF file size bound")
)

// countingWriter counts the bytes its underlying writer accepted. It is the
// measurement the writers trust: io.Copy hands the copy to the source's own
// WriteTo when it has one, and the count that call returns is the source's
// claim — the bytes that actually passed through here are not.
type countingWriter struct {
	w io.Writer
	n int64
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.n += int64(n)
	return n, err
}

// cappedWriter passes writes through until remaining bytes have gone out,
// then stops accepting: the write that would cross the cap is truncated to
// it and fails with the sentinel its creator chose. A source staying within
// the cap never notices — io.Copy keeps its WriteTo fast path — and one
// producing more is cut off at the boundary instead of being drained.
type cappedWriter struct {
	w         io.Writer
	remaining int64
	sentinel  error
}

func (cw *cappedWriter) Write(p []byte) (int, error) {
	over := int64(len(p)) > cw.remaining
	if over {
		p = p[:cw.remaining]
	}
	var n int
	var err error
	if len(p) > 0 {
		n, err = cw.w.Write(p)
		cw.remaining -= int64(n)
	}
	if err == nil && over {
		// an error of the underlying writer takes precedence: the sentinel
		// only reports that the source outgrew the cap
		err = cw.sentinel
	}
	return n, err
}

func unsupportedChunkTypeError(c Chunk) error {
	return fmt.Errorf("%w: %T is neither a GroupedChunk nor a SubChunk", ErrUnsupportedChunkType, c)
}

package riffbin

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"iter"
	"math"
	"slices"
	"strings"
)

// maxGroupDepth bounds how deeply grouped chunks may nest.
// Real RIFF trees are shallow; the limit keeps hostile input from exhausting the stack.
const maxGroupDepth = 100

// ReadSeekerAt is the input required by ReadSections: Seek skips over the
// sub-chunk bodies while parsing, and ReadAt serves them on demand afterwards.
type ReadSeekerAt interface {
	io.ReadSeeker
	io.ReaderAt
}

type readerConfig struct {
	allowPaddingViolations bool
	allowTrailingData      bool
}

// ReaderOption relaxes a rule of the RIFF specification for the readers.
// Without any option they are strict.
type ReaderOption interface {
	apply(*readerConfig)
}

type readerOptionFunc func(*readerConfig)

func (f readerOptionFunc) apply(c *readerConfig) { f(c) }

// AllowPaddingViolations accepts files whose odd-sized chunk bodies are not followed by a
// well-formed pad byte. The byte where the pad byte belongs is then read by its value:
// 0x00 is the pad byte; printable ASCII is taken as the first byte of the next chunk
// header, for files that omit pad bytes entirely (riffbin up to v0.0.6 wrote such files);
// any other value is a pad byte holding garbage and is skipped, as most RIFF
// implementations never inspect the pad value.
func AllowPaddingViolations() ReaderOption {
	return readerOptionFunc(func(c *readerConfig) { c.allowPaddingViolations = true })
}

// AllowTrailingData ignores any bytes that follow the RIFF chunk instead of rejecting
// them: the call consumes exactly the root chunk and leaves the input right after it,
// which is how a stream of concatenated RIFF chunks is read. A single 0x00 before the
// root chunk header is skipped — the pad byte of a previous chunk whose writer did not
// count it in the RIFF chunk size, which no chunk header can start with — so such
// streams read on. Without the option that byte is still tolerated at the very end of
// the input, after an odd-sized final chunk.
func AllowTrailingData() ReaderOption {
	return readerOptionFunc(func(c *readerConfig) { c.allowTrailingData = true })
}

// ChunkInfo describes one chunk encountered by Chunks.
type ChunkInfo struct {
	// Depth is the nesting depth; the root RIFF chunk is 0.
	Depth int

	// ID is the four-character chunk ID.
	ID FourCC

	// GroupType is the form type or list type of a RIFF or LIST chunk.
	// It is the zero value for a leaf chunk, which Grouped reports.
	GroupType FourCC

	// BodySize is the byte length of the chunk body, as declared by its header.
	BodySize int64

	// BodyOffset is the offset of the chunk body — the group type of a grouped
	// chunk, the payload of a leaf — counted from the position the reader was
	// at when the iteration started, like SyntaxError.Offset.
	BodyOffset int64

	// Body reads the payload of a leaf chunk; it is nil for a grouped chunk.
	// It is only valid until the iteration advances: whatever is left unread
	// by then is skipped, and later reads report ErrRevokedBody.
	Body io.Reader
}

// Grouped reports whether the chunk is a RIFF or LIST chunk, whose body is a
// group type followed by other chunks.
func (c ChunkInfo) Grouped() bool { return c.GroupType != (FourCC{}) }

// Chunks returns an iterator over the chunks of one RIFF chunk read from r, in
// depth-first document order — the order they appear in the input. It is the
// streaming counterpart of ReadAll and ReadSections: no tree is built, memory
// stays proportional to the nesting depth, and breaking out of the loop stops
// reading, so a search can end at the first chunk of interest.
//
// A grouped chunk is yielded once, before the chunks it contains; where it ends
// is implied by the Depth of the chunks that follow. When r also implements
// ReadSeekerAt, bodies left unread are skipped by seeking rather than read
// through, and BodyOffset addresses them for later reads. A value whose stream
// cannot actually seek — os.Stdin on a pipe or a terminal — is read through.
//
// The iteration yields at most one error, as its final pair: io.EOF when the
// input ends before the first byte of the root chunk header, a SyntaxError
// wrapping ErrInvalidFormat for malformed input, or the underlying reader's own
// error, returned as is. Like the other readers, with AllowTrailingData the
// iteration consumes exactly one root chunk, so calling Chunks again reads the
// next chunk of a concatenated stream — as does ranging the same iterator
// again, which reads onward from wherever the input then stands.
func Chunks(r io.Reader, opts ...ReaderOption) iter.Seq2[ChunkInfo, error] {
	return func(yield func(ChunkInfo, error) bool) {
		limit := int64(-1)
		pr, _ := r.(ReadSeekerAt)
		if pr != nil {
			if _, err := pr.Seek(0, io.SeekCurrent); err != nil {
				// the type can seek but the stream cannot: read bodies through
				pr = nil
			} else if _, limit, err = measure(pr); err != nil {
				yield(ChunkInfo{}, err)
				return
			}
		}
		scan(r, pr, limit, opts, yield)
	}
}

// ReadAll reads one RIFF chunk from r, materializing every sub-chunk body in
// memory as an *InMemorySubChunk.
//
// An input that ends before the first byte of the root chunk header yields
// io.EOF. With AllowTrailingData the call consumes exactly the root chunk, so a
// stream of concatenated RIFF chunks — the layout AVI 2.0 uses to grow past the
// 32-bit size field by appending RIFF("AVIX") chunks — is read by calling
// ReadAll repeatedly until io.EOF; Concatenated wraps that loop.
func ReadAll(r io.Reader, opts ...ReaderOption) (*RIFFChunk, error) {
	return buildTree(func(yield func(ChunkInfo, error) bool) {
		scan(r, nil, -1, opts, yield)
	}, nil, 0)
}

// ReadSections reads one RIFF chunk from r, skipping over the sub-chunk bodies
// and returning *SectionSubChunk values that read them from r on demand; use
// it for files whose payloads are too large to hold in memory. Its tree still
// grows with the number of chunks — for files with too many chunks for that,
// use Chunks. All boundaries are relative to the position r is at when the
// call is made, so a RIFF chunk embedded mid-stream can be read in place.
//
// Like ReadAll, it yields io.EOF when the input ends before the root chunk
// header, and with AllowTrailingData it leaves r right after the root chunk,
// so repeated calls read a stream of concatenated RIFF chunks.
func ReadSections(r ReadSeekerAt, opts ...ReaderOption) (*RIFFChunk, error) {
	origin, limit, err := measure(r)
	if err != nil {
		return nil, err
	}
	return buildTree(func(yield func(ChunkInfo, error) bool) {
		scan(r, r, limit, opts, yield)
	}, r, origin)
}

// Concatenated returns an iterator over the RIFF chunks of a concatenated
// stream — the layout AVI 2.0 uses to grow past the 32-bit size field by
// appending RIFF("AVIX") chunks — reading each one fully into memory like
// ReadAll. The iteration ends at the end of the input; an error ends it after
// being yielded with a nil chunk.
func Concatenated(r io.Reader, opts ...ReaderOption) iter.Seq2[*RIFFChunk, error] {
	opts = append(append([]ReaderOption{}, opts...), AllowTrailingData())
	return func(yield func(*RIFFChunk, error) bool) {
		for {
			c, err := ReadAll(r, opts...)
			if errors.Is(err, io.EOF) {
				return
			}
			if !yield(c, err) || err != nil {
				return
			}
		}
	}
}

// measure records where r stands and how many bytes remain to its end. The
// parser needs the input measured up front when sub-chunk bodies are skipped
// by seeking, because seeking past the end of a file succeeds silently.
func measure(r io.Seeker) (origin, limit int64, err error) {
	origin, err = r.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, 0, fmt.Errorf("riffbin: get seek position: %w", err)
	}
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, 0, fmt.Errorf("riffbin: seek to end: %w", err)
	}
	if _, err = r.Seek(origin, io.SeekStart); err != nil {
		return 0, 0, fmt.Errorf("riffbin: seek: %w", err)
	}
	return origin, size - origin, nil
}

// buildTree assembles the chunk tree from the events of a scan. With sec
// non-nil the leaf bodies stay in the stream, referenced as SectionSubChunk
// values at origin-relative offsets; otherwise every body is read into memory.
// The stack indexing leans on scan's contract — the first event is a grouped
// chunk at depth 0, and depths never skip a level — so a scan bug panics here
// rather than assembling a wrong tree.
func buildTree(seq iter.Seq2[ChunkInfo, error], sec io.ReaderAt, origin int64) (*RIFFChunk, error) {
	var root *RIFFChunk
	var stack []*[]Chunk // the children of every open grouped chunk, outermost first
	for info, err := range seq {
		if err != nil {
			return nil, err
		}
		stack = stack[:info.Depth]

		if info.Grouped() {
			if info.Depth == 0 {
				byteOrder := LittleEndian
				if info.ID == rifxID {
					byteOrder = BigEndian
				}
				root = &RIFFChunk{ByteOrder: byteOrder, FormType: info.GroupType, Payload: []Chunk{}}
				stack = append(stack, &root.Payload)
			} else {
				list := &ListChunk{ListType: info.GroupType, Payload: []Chunk{}}
				parent := stack[info.Depth-1]
				*parent = append(*parent, list)
				stack = append(stack, &list.Payload)
			}
			continue
		}

		var leaf Chunk
		if sec != nil {
			leaf = &SectionSubChunk{ID: info.ID, SectionReader: io.NewSectionReader(sec, origin+info.BodyOffset, info.BodySize)}
		} else {
			payload, err := readLeafBody(info.ID, info.Body, info.BodySize)
			if err != nil {
				return nil, err
			}
			leaf = &InMemorySubChunk{ID: info.ID, Payload: payload}
		}
		parent := stack[info.Depth-1]
		*parent = append(*parent, leaf)
	}
	if root == nil {
		// unreachable: a scan always yields the root chunk or an error first
		return nil, io.ErrUnexpectedEOF
	}
	return root, nil
}

// readLeafBody materializes a leaf body of the declared size. The size is
// untrusted even after the parser's bounds checks, because the enclosing sizes
// it was checked against are untrusted too: the buffer grows as bytes actually
// arrive, so a bogus size field cannot force a huge allocation.
func readLeafBody(id FourCC, r io.Reader, size int64) ([]byte, error) {
	// int is 32 bits on 32-bit platforms, where a body beyond what a []byte can
	// hold must fail cleanly instead of dying in the allocator mid-read
	if size > math.MaxInt {
		return nil, fmt.Errorf("riffbin: the %s chunk declares a %d byte body, more than this platform holds in memory", id, size)
	}
	payload := []byte{}
	for int64(len(payload)) < size {
		step := int(min(size-int64(len(payload)), 64<<10))
		start := len(payload)
		payload = slices.Grow(payload, step)[:start+step]
		if _, err := io.ReadFull(r, payload[start:]); err != nil {
			return nil, err
		}
	}
	return payload, nil
}

// offsetReader is the single source of truth for how many bytes have been consumed.
// Every chunk boundary is an absolute offset into this stream, so a sub-chunk that is
// skipped by seeking only has to advance one counter for every enclosing chunk to stay correct.
type offsetReader struct {
	r   io.Reader
	off int64
}

func (o *offsetReader) Read(p []byte) (int, error) {
	n, err := o.r.Read(p)
	o.off += int64(n)
	return n, err
}

type parser struct {
	src  *offsetReader
	pr   ReadSeekerAt // non-nil when unread bodies are skipped by seeking
	conf readerConfig

	// limit is the byte length of the input, or -1 when it is unknown.
	limit int64

	order binary.ByteOrder
	path  []string

	// body is the reader handed out with the leaf chunk yielded last. It is
	// revoked when the iteration advances, so a reader kept across iterations
	// fails instead of mis-reading the stream. Its state lives here to keep
	// the per-chunk allocation at a bare pointer.
	body          *bodyReader
	bodyID        FourCC
	bodyOff       int64 // the body offset, where a truncation is reported
	bodyRemaining int64

	// pending holds the byte probed where a pad byte was expected but a chunk header
	// was found instead. It is only ever set with AllowPaddingViolations.
	pending     bool
	pendingByte byte

	// tolerateTrailingPad is true when the chunk read last has an odd-sized body whose
	// pad byte is not counted in its parent's size, so a single 0x00 may follow the
	// root chunk. See verifyEnd.
	tolerateTrailingPad bool
}

// scan is the parser: it reads one RIFF chunk from r and reports every chunk to
// yield in document order. ReadAll, ReadSections and Chunks all consume it, so
// each rule of the format lives here exactly once. It stops when yield reports
// false; any failure is the final yield, and none may follow it.
func scan(r io.Reader, pr ReadSeekerAt, limit int64, opts []ReaderOption, yield func(ChunkInfo, error) bool) {
	p := &parser{src: &offsetReader{r: r}, pr: pr, limit: limit}
	for _, o := range opts {
		o.apply(&p.conf)
	}

	// read the root header. a clean end of input before its first byte is io.EOF,
	// not a syntax error, so that a stream of concatenated RIFF chunks can be read
	// until it runs dry. with AllowTrailingData a single 0x00 before the header is
	// skipped first: it is the pad byte of a previous root chunk whose writer did
	// not count it in the RIFF size — the byte verifyEnd tolerates after a single
	// chunk — and no chunk header can start with 0x00, so the byte is unambiguous.
	var buf [HeaderBytes]byte
	headerOff, read := int64(0), 0
	if p.conf.allowTrailingData {
		for {
			if _, err := io.ReadFull(p.src, buf[:1]); err != nil {
				if errors.Is(err, io.EOF) {
					yield(ChunkInfo{}, io.EOF)
				} else {
					yield(ChunkInfo{}, err)
				}
				return
			}
			if buf[0] != 0x00 || headerOff > 0 {
				break
			}
			headerOff = 1
		}
		read = 1
	}
	if _, err := io.ReadFull(p.src, buf[read:]); err != nil {
		switch {
		case errors.Is(err, io.EOF) && read == 0:
			yield(ChunkInfo{}, io.EOF)
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			yield(ChunkInfo{}, p.syntaxError(headerOff, "unexpected end of input while reading the root chunk header"))
		default:
			yield(ChunkInfo{}, err)
		}
		return
	}

	// verify id
	var id FourCC
	copy(id[:], buf[:IDBytes])
	switch id {
	case riffID:
		p.order = binary.LittleEndian
	case rifxID:
		p.order = binary.BigEndian
	case rf64ID, bw64ID, ffirID, xfirID:
		yield(ChunkInfo{}, fmt.Errorf("%w: %s containers are not supported", ErrUnsupportedFormat, id))
		return
	default:
		yield(ChunkInfo{}, p.syntaxError(headerOff, "root chunk ID is %q, want %q or %q", id, riffID, rifxID))
		return
	}

	bodyLen := int64(p.order.Uint32(buf[IDBytes:]))
	end := p.src.off + bodyLen
	if p.limit >= 0 && end > p.limit {
		yield(ChunkInfo{}, p.syntaxError(headerOff+IDBytes, "root chunk declares a %d byte body but the input holds only %d byte(s)", bodyLen, p.limit-p.src.off))
		return
	}

	if !p.scanGroup(id, bodyLen, end, 0, yield) {
		return
	}

	if err := p.verifyEnd(); err != nil {
		yield(ChunkInfo{}, err)
	}
}

// scanGroup scans the body of a grouped chunk whose header has been read: the
// group type, then every chunk it contains. end is the absolute offset at which
// the body ends. It reports whether the scan may continue.
func (p *parser) scanGroup(id FourCC, bodyLen, end int64, depth int, yield func(ChunkInfo, error) bool) bool {
	if len(p.path) >= maxGroupDepth {
		yield(ChunkInfo{}, p.syntaxError(p.src.off, "chunks are nested deeper than %d levels", maxGroupDepth))
		return false
	}
	p.path = append(p.path, id.String())
	defer func() { p.path = p.path[:len(p.path)-1] }()

	// read type
	bodyOff := p.src.off
	if remain := end - p.src.off; remain < TypeBytes {
		yield(ChunkInfo{}, p.syntaxError(p.src.off, "%s chunk holds %d byte(s), too few for a group type", id, remain))
		return false
	}
	var groupType FourCC
	if err := p.readFull(groupType[:], "a group type"); err != nil {
		yield(ChunkInfo{}, err)
		return false
	}
	if !groupType.Valid() {
		yield(ChunkInfo{}, p.syntaxError(p.src.off-TypeBytes, "group type %q is not printable ASCII", groupType[:]))
		return false
	}
	p.path[len(p.path)-1] = id.String() + "(" + groupType.String() + ")"

	if !yield(ChunkInfo{Depth: depth, ID: id, GroupType: groupType, BodySize: bodyLen, BodyOffset: bodyOff}, nil) {
		return false
	}

	// scan sub-chunks
	for p.src.off < end || p.pending {
		if !p.scanChunk(end, depth+1, yield) {
			return false
		}
	}
	return true
}

// scanChunk scans one chunk: its header, its body or nested group, and the pad
// byte after an odd-sized body. It reports whether the scan may continue.
func (p *parser) scanChunk(end int64, depth int, yield func(ChunkInfo, error) bool) bool {
	// read header
	var buf [HeaderBytes]byte
	headerOff, read := p.src.off, 0
	if p.pending {
		buf[0], p.pending = p.pendingByte, false
		headerOff, read = headerOff-1, 1
	}
	if remain := end - p.src.off; remain < int64(HeaderBytes-read) {
		yield(ChunkInfo{}, p.syntaxError(headerOff, "%d trailing byte(s) are too few for a chunk header", remain+int64(read)))
		return false
	}
	if err := p.readFull(buf[read:], "a chunk header"); err != nil {
		yield(ChunkInfo{}, err)
		return false
	}

	var id FourCC
	copy(id[:], buf[:IDBytes])
	if !id.Valid() {
		yield(ChunkInfo{}, p.syntaxError(headerOff, "chunk ID %q is not printable ASCII", id[:]))
		return false
	}

	bodyLen := int64(p.order.Uint32(buf[IDBytes:]))
	if remain := end - p.src.off; bodyLen > remain {
		yield(ChunkInfo{}, p.syntaxError(headerOff, "%s chunk declares a %d byte body but only %d byte(s) remain", id, bodyLen, remain))
		return false
	}

	switch id {
	case riffID, rifxID:
		// the specification allows a RIFF chunk only as the root chunk
		yield(ChunkInfo{}, p.syntaxError(headerOff, "a %s chunk must not be nested", id))
		return false
	case listID:
		if !p.scanGroup(id, bodyLen, p.src.off+bodyLen, depth, yield) {
			return false
		}
	default:
		bodyOff := p.src.off
		body := &bodyReader{p: p}
		p.body, p.bodyID, p.bodyOff, p.bodyRemaining = body, id, bodyOff, bodyLen
		cont := yield(ChunkInfo{Depth: depth, ID: id, BodySize: bodyLen, BodyOffset: bodyOff, Body: body}, nil)
		p.body = nil
		if !cont {
			return false
		}
		if err := p.skipBody(id, bodyOff, p.bodyRemaining); err != nil {
			yield(ChunkInfo{}, err)
			return false
		}
	}

	if err := p.skipPadding(id, bodyLen, end); err != nil {
		yield(ChunkInfo{}, err)
		return false
	}
	return true
}

// bodyReader hands a leaf chunk's payload to the consumer of a scan. It reads
// straight from the parser's source, so it is revoked when the scan advances.
type bodyReader struct{ p *parser }

func (b *bodyReader) Read(q []byte) (int, error) {
	p := b.p
	if p.body != b {
		return 0, ErrRevokedBody
	}
	if p.bodyRemaining == 0 {
		return 0, io.EOF
	}
	if int64(len(q)) > p.bodyRemaining {
		q = q[:p.bodyRemaining]
	}
	n, err := p.src.Read(q)
	p.bodyRemaining -= int64(n)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		if p.bodyRemaining > 0 {
			return n, p.syntaxError(p.bodyOff, "unexpected end of input while reading the %s chunk body", p.bodyID)
		}
		err = nil // exactly drained; io.EOF follows on the next call
	}
	return n, err
}

// skipBody discards what the consumer left unread of a leaf body: by seeking
// when the source supports it, by reading it through otherwise.
func (p *parser) skipBody(id FourCC, bodyOff, remaining int64) error {
	if remaining == 0 {
		return nil
	}
	if p.pr != nil {
		if _, err := p.pr.Seek(remaining, io.SeekCurrent); err != nil {
			return fmt.Errorf("riffbin: seek: %w", err)
		}
		p.src.off += remaining
		return nil
	}
	if _, err := io.CopyN(io.Discard, p.src, remaining); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return p.syntaxError(bodyOff, "unexpected end of input while reading the %s chunk body", id)
		}
		return fmt.Errorf("riffbin: read the %s chunk body: %w", id, err)
	}
	return nil
}

// verifyEnd rejects data beyond the root chunk. When the final chunk has an odd-sized
// body whose pad byte is not counted in the RIFF size, a single 0x00 is tolerated,
// because writers commonly append that pad byte anyway.
func (p *parser) verifyEnd() error {
	if p.conf.allowTrailingData {
		return nil
	}

	var buf [1]byte
	switch _, err := io.ReadFull(p.src, buf[:]); {
	case errors.Is(err, io.EOF):
		return nil
	case err != nil:
		return err
	case buf[0] != 0x00 || !p.tolerateTrailingPad:
		return p.syntaxError(p.src.off-1, "unexpected data after the root chunk")
	}

	if _, err := io.ReadFull(p.src, buf[:]); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return p.syntaxError(p.src.off-1, "unexpected data after the root chunk")
}

// skipPadding consumes the word-alignment pad byte that follows an odd-sized chunk body.
// The pad byte is not counted in the chunk's own size but is counted in its parent's size,
// so it is absent when the parent size stops right at the end of the body.
func (p *parser) skipPadding(id FourCC, bodyLen, end int64) error {
	p.tolerateTrailingPad = false
	if bodyLen%2 == 0 {
		return nil
	}
	if p.src.off >= end {
		// the pad byte would lie outside the parent's declared size; for the final
		// chunk many writers append it anyway, which verifyEnd tolerates
		p.tolerateTrailingPad = true
		return nil
	}

	off := p.src.off
	var buf [1]byte
	if err := p.readFull(buf[:], "an alignment pad byte"); err != nil {
		return err
	}
	if buf[0] == 0x00 {
		return nil
	}
	if p.conf.allowPaddingViolations {
		// chunk IDs are printable ASCII, so a printable byte here can head the next
		// header of an unpadded file, while anything else can only be a pad byte
		// holding garbage
		if printableASCII(buf[0]) {
			p.pending, p.pendingByte = true, buf[0]
		}
		return nil
	}
	return p.syntaxError(off, "pad byte after the odd-sized %s chunk is %#02x, want 0x00", id, buf[0])
}

func (p *parser) readFull(buf []byte, what string) error {
	off := p.src.off
	if _, err := io.ReadFull(p.src, buf); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return p.syntaxError(off, "unexpected end of input while reading %s", what)
		}
		return err
	}
	return nil
}

func (p *parser) syntaxError(offset int64, format string, args ...any) error {
	return &SyntaxError{
		Offset: offset,
		Path:   strings.Join(p.path, "/"),
		Reason: fmt.Sprintf(format, args...),
	}
}

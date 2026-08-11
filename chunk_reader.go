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

// skipSeekThreshold is the smallest unread body the parser skips by seeking.
// A seek is a syscall; skips at or below this size are cheaper to read through.
const skipSeekThreshold = 4096

// ReadSeekerAt is the input required by ReadSections: Seek skips over the
// sub-chunk bodies while parsing, and ReadAt serves them on demand afterwards.
type ReadSeekerAt interface {
	io.ReadSeeker
	io.ReaderAt
}

// PaddingPolicy decides what the byte at a pad position means. The
// specification requires an odd-sized chunk body to be followed by one 0x00
// pad byte; real-world writers deviate in two mutually exclusive ways, so the
// policy is a single three-valued choice rather than independent flags — a
// conflicting combination is unrepresentable.
//
// A PaddingPolicy is itself a ReaderOption: pass PadOmitted or PadGarbage to
// a reader directly. The zero value PadStrict is the default.
type PaddingPolicy uint8

const (
	// PadStrict is the specification: an odd-sized chunk body is followed by
	// exactly one 0x00 pad byte. It is the default.
	PadStrict PaddingPolicy = iota

	// PadOmitted reads files whose writers omit the pad byte after an
	// odd-sized chunk body entirely — riffbin up to v0.0.6 wrote such files,
	// and e.g. Apple CoreAudio still does. Where the enclosing size leaves
	// room for a pad byte, 0x00 is read as the pad byte and printable ASCII
	// as the first byte of the next chunk header; any other value stays an
	// error. A padded file whose pad byte holds printable garbage is
	// indistinguishable from an unpadded file by construction and reads as
	// one — declare only the deviation the input actually has.
	PadOmitted

	// PadGarbage reads files whose pad bytes hold garbage instead of the zero
	// the specification requires: the byte at a pad position is skipped
	// without inspecting its value, as the reference readers do
	// (x/image/riff, ffmpeg, libwebp). On a file that omits pad bytes the
	// skip eats the first byte of the next chunk header instead — usually an
	// error, but the shifted bytes can also read as a different, complete
	// tree — declare only the deviation the input actually has.
	PadGarbage
)

var _ ReaderOption = PadStrict

func (p PaddingPolicy) apply(c *readerConfig) { c.padding = p }

// String names the policy: "strict", "omitted" or "garbage".
func (p PaddingPolicy) String() string {
	switch p {
	case PadOmitted:
		return "omitted"
	case PadGarbage:
		return "garbage"
	}
	return "strict"
}

type readerConfig struct {
	padding           PaddingPolicy
	allowTrailingData bool
}

// ReaderOption relaxes a rule of the RIFF specification for the readers.
// Without any option they are strict. The options are the PaddingPolicy
// values and AllowTrailingData.
type ReaderOption interface {
	apply(*readerConfig)
}

type readerOptionFunc func(*readerConfig)

func (f readerOptionFunc) apply(c *readerConfig) { f(c) }

// AllowTrailingData ignores any bytes that follow the RIFF chunk instead of rejecting
// them: the call consumes exactly the root chunk and leaves the input right after it,
// which is how a stream of concatenated RIFF chunks is read. A single 0x00 before the
// root chunk header is skipped — the pad byte of a previous chunk whose writer did not
// count it in the RIFF chunk size, which no chunk header can start with, so such
// streams read on. (The skip has to happen at the start of the next read: on a
// forward-only reader an uncounted pad byte can only be told from the next header by
// reading it, and a byte read past the chunk could not be handed back between calls.)
// Without the option that byte is still tolerated at the very end of the input, after
// an odd-sized final chunk.
func AllowTrailingData() ReaderOption {
	return readerOptionFunc(func(c *readerConfig) { c.allowTrailingData = true })
}

func resolveOptions(opts []ReaderOption) readerConfig {
	var c readerConfig
	for _, o := range opts {
		o.apply(&c)
	}
	return c
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
	// It is only valid while the iteration stands at this chunk: once the
	// iteration moves on — to the next chunk, or out of the loop entirely,
	// a break included — reads report ErrRevokedBody. Whatever advancing
	// leaves unread is skipped; to read a body after the loop, use
	// BodyOffset with a seekable source.
	Body io.Reader
}

// Grouped reports whether the chunk is a RIFF or LIST chunk, whose body is a
// group type followed by other chunks.
func (c ChunkInfo) Grouped() bool { return c.GroupType != (FourCC{}) }

// Chunks returns an iterator over the chunks of one RIFF chunk read from r, in
// depth-first document order — the order they appear in the input. It is the
// streaming counterpart of ReadAll and ReadSections: no tree is built, memory
// stays proportional to the nesting depth, and breaking out of the loop stops
// reading, so a search can end at the first chunk of interest. Read a leaf's
// Body inside the loop: ending the iteration revokes it like advancing does.
//
// A grouped chunk is yielded once, before the chunks it contains; where it ends
// is implied by the Depth of the chunks that follow. When r also implements
// ReadSeekerAt, large bodies left unread are skipped by seeking rather than
// read through, and BodyOffset addresses them for later reads. Seeking is only
// an optimization: a value whose stream cannot actually seek, cannot be
// measured, or reports a size its stream contradicts — os.Stdin on a pipe, a
// character device, a procfs file — is read through, and every rule of the
// format is applied the same way on both paths.
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
		src, err := chunksSource(r)
		if err != nil {
			yield(ChunkInfo{}, err)
			return
		}
		scan(src, resolveOptions(opts), yield)
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
		scan(newPlainSource(r), resolveOptions(opts), yield)
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
	origin, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, fmt.Errorf("riffbin: get seek position: %w", err)
	}
	src, err := measuredSource(r, origin)
	if err != nil {
		return nil, err
	}
	return buildTree(func(yield func(ChunkInfo, error) bool) {
		scan(src, resolveOptions(opts), yield)
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

// source is the parser's only view of the input: it owns the logical offset,
// can hold one byte of lookahead, and skips unread bytes by seeking when that
// is provably safe. The parser never learns which strategy is active, so a
// rule of the format cannot apply on one path and not the other.
type source struct {
	r       io.Reader
	seeker  io.Seeker // non-nil only when limit is known, so a seek cannot cross the end
	limit   int64     // bytes from the start position to the end, or -1 when unknown
	off     int64     // bytes delivered to the parser; a peeked byte is not yet counted
	peeked  int16     // -1 when empty, else the byte waiting at off
	scratch []byte    // the skip buffer, allocated once on first use
}

func newPlainSource(r io.Reader) *source {
	return &source{r: r, limit: -1, peeked: -1}
}

// chunksSource probes r for the seek-skip optimization. Every obstacle —
// not a ReadSeekerAt, a stream that cannot seek or be measured, a reported
// size its stream contradicts (character devices and procfs files report 0) —
// falls back to reading through. The only error is a stream left at an
// unknowable position by a failed probe, which could not be parsed correctly
// by either strategy.
func chunksSource(r io.Reader) (*source, error) {
	pr, ok := r.(ReadSeekerAt)
	if !ok {
		return newPlainSource(r), nil
	}
	origin, err := pr.Seek(0, io.SeekCurrent)
	if err != nil {
		// the type can seek but the stream cannot: read through
		return newPlainSource(r), nil
	}
	return measuredSource(pr, origin)
}

// measuredSource measures how many bytes remain to the end of r and returns a
// seeking source over them. The measurement exists because seeking past the
// end of a file succeeds silently; when it fails or reports a size the stream
// contradicts, the source reads through instead — seeking is only an
// optimization, and must never change what the parser accepts.
func measuredSource(r io.ReadSeeker, origin int64) (*source, error) {
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		// the stream seeks but cannot be measured — no SeekEnd, say
		if _, rerr := r.Seek(origin, io.SeekStart); rerr != nil {
			// the position is unknowable now; reading on would misparse
			return nil, fmt.Errorf("riffbin: seek: %w", rerr)
		}
		return newPlainSource(r), nil
	}
	if _, err := r.Seek(origin, io.SeekStart); err != nil {
		return nil, fmt.Errorf("riffbin: seek: %w", err)
	}
	if size <= origin {
		return newPlainSource(r), nil
	}
	return &source{r: r, seeker: r, limit: size - origin, peeked: -1}, nil
}

func (s *source) Read(p []byte) (int, error) {
	if s.peeked >= 0 && len(p) > 0 {
		p[0] = byte(s.peeked)
		s.peeked = -1
		s.off++
		return 1, nil
	}
	n, err := s.r.Read(p)
	s.off += int64(n)
	return n, err
}

// peek returns the byte at the current offset without consuming it. The byte
// is delivered by the next Read; discardPeeked consumes it instead.
func (s *source) peek() (byte, error) {
	if s.peeked >= 0 {
		return byte(s.peeked), nil
	}
	var b [1]byte
	if _, err := io.ReadFull(s.r, b[:]); err != nil {
		return 0, err
	}
	s.peeked = int16(b[0])
	return b[0], nil
}

func (s *source) discardPeeked() {
	s.peeked = -1
	s.off++
}

// skip discards n bytes: by seeking when the skip is large and provably inside
// the input, by reading through otherwise — so a skip past the end of the
// input fails exactly like the read-through path, at the same offset. The
// read-through runs over a stack buffer: a skip costs no allocation.
func (s *source) skip(n int64) error {
	if s.peeked >= 0 && n > 0 {
		s.discardPeeked()
		n--
	}
	if n == 0 {
		return nil
	}
	if s.seeker != nil && n > skipSeekThreshold && s.off+n <= s.limit {
		if _, err := s.seeker.Seek(n, io.SeekCurrent); err != nil {
			return fmt.Errorf("riffbin: seek: %w", err)
		}
		s.off += n
		return nil
	}
	if s.scratch == nil {
		s.scratch = make([]byte, 4096)
	}
	for n > 0 {
		step := min(n, int64(len(s.scratch)))
		if _, err := io.ReadFull(s, s.scratch[:step]); err != nil {
			return err
		}
		n -= step
	}
	return nil
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

type parser struct {
	src  *source
	conf readerConfig

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

	// tolerateTrailingPad is true when the chunk read last has an odd-sized body whose
	// pad byte is not counted in its parent's size, so a single 0x00 may follow the
	// root chunk. See verifyEnd.
	tolerateTrailingPad bool
}

// isEOFFamily reports whether err says the input ended: io.EOF or
// io.ErrUnexpectedEOF, however wrapped. What that means depends on where the
// parser stands — see scan.
func isEOFFamily(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// scan is the parser: it reads one RIFF chunk from src and reports every chunk
// to yield in document order. ReadAll, ReadSections and Chunks all consume it,
// so each rule of the format lives here exactly once. It stops when yield
// reports false; any failure is the final yield, and none may follow it.
//
// An EOF-family error from the underlying reader means the input ended there:
// before the first byte of the root chunk header it is a clean io.EOF, inside
// the structure it is a SyntaxError for the truncation, and while probing for
// data after the root chunk it is a clean end. Any other error surfaces as the
// reader's own.
func scan(src *source, conf readerConfig, yield func(ChunkInfo, error) bool) {
	p := &parser{src: src, conf: conf}

	// with AllowTrailingData a single 0x00 before the header is skipped: it is
	// the pad byte of a previous root chunk whose writer did not count it in
	// the RIFF size — the byte the end-of-chunk probe tolerates after a single
	// chunk — and no chunk header can start with 0x00, so it is unambiguous.
	if conf.allowTrailingData {
		switch b, err := src.peek(); {
		case err == nil:
			if b == 0x00 {
				src.discardPeeked()
			}
		case isEOFFamily(err):
			yield(ChunkInfo{}, io.EOF)
			return
		default:
			yield(ChunkInfo{}, err)
			return
		}
	}

	// read the root header. a clean end of input before its first byte is io.EOF,
	// not a syntax error, so that a stream of concatenated RIFF chunks can be read
	// until it runs dry.
	var buf [HeaderBytes]byte
	headerOff := src.off
	if _, err := io.ReadFull(src, buf[:]); err != nil {
		switch {
		case errors.Is(err, io.EOF):
			yield(ChunkInfo{}, io.EOF)
		case errors.Is(err, io.ErrUnexpectedEOF):
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
	if !p.scanGroup(id, bodyLen, src.off+bodyLen, 0, yield) {
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

	// scan sub-chunks; a byte left peeked by the padding policy sits at the
	// current offset, so the loop condition needs no special case for it
	for p.src.off < end {
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
	headerOff := p.src.off
	if remain := end - p.src.off; remain < HeaderBytes {
		yield(ChunkInfo{}, p.syntaxError(headerOff, "%d trailing byte(s) are too few for a chunk header", remain))
		return false
	}
	if err := p.readFull(buf[:], "a chunk header"); err != nil {
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
	if isEOFFamily(err) {
		if p.bodyRemaining > 0 {
			return n, p.syntaxError(p.bodyOff, "unexpected end of input while reading the %s chunk body", p.bodyID)
		}
		err = nil // exactly drained; io.EOF follows on the next call
	}
	return n, err
}

// skipBody discards what the consumer left unread of a leaf body. The source
// seeks over a large body when that is provably safe and reads through
// otherwise, so a truncated body fails identically on both paths.
func (p *parser) skipBody(id FourCC, bodyOff, remaining int64) error {
	if remaining == 0 {
		return nil
	}
	if err := p.src.skip(remaining); err != nil {
		if isEOFFamily(err) {
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
	case isEOFFamily(err):
		return nil
	case err != nil:
		return err
	case buf[0] != 0x00 || !p.tolerateTrailingPad:
		return p.syntaxError(p.src.off-1, "unexpected data after the root chunk")
	}

	if _, err := io.ReadFull(p.src, buf[:]); isEOFFamily(err) {
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
	b, err := p.src.peek()
	if err != nil {
		if isEOFFamily(err) {
			return p.syntaxError(off, "unexpected end of input while reading an alignment pad byte")
		}
		return err
	}
	switch {
	case b == 0x00:
		p.src.discardPeeked()
	case p.conf.padding == PadGarbage:
		// a pad byte holding garbage; the reference readers never inspect the value
		p.src.discardPeeked()
	case p.conf.padding == PadOmitted && printableASCII(b):
		// chunk IDs are printable ASCII, so in a file whose writer omitted the
		// pad byte, this byte heads the next chunk header: leave it in place
	default:
		p.src.discardPeeked()
		return p.syntaxError(off, "pad byte after the odd-sized %s chunk is %#02x, want 0x00", id, b)
	}
	return nil
}

func (p *parser) readFull(buf []byte, what string) error {
	off := p.src.off
	if _, err := io.ReadFull(p.src, buf); err != nil {
		if isEOFFamily(err) {
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

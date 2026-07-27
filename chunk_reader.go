package riffbin

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
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

// ReaderOption relaxes a rule of the RIFF specification for ReadAll and ReadSections.
// Without any option both readers are strict.
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
// which is how a stream of concatenated RIFF chunks is read. Without it a single 0x00
// is still tolerated after an odd-sized final chunk, because writers commonly append
// its pad byte without counting it in the RIFF chunk size.
func AllowTrailingData() ReaderOption {
	return readerOptionFunc(func(c *readerConfig) { c.allowTrailingData = true })
}

// ReadAll reads one RIFF chunk from r, materializing every sub-chunk body in
// memory as an *InMemorySubChunk.
//
// An input that ends before the first byte of the root chunk header yields
// io.EOF. With AllowTrailingData the call consumes exactly the root chunk, so a
// stream of concatenated RIFF chunks — the layout AVI 2.0 uses to grow past the
// 32-bit size field by appending RIFF("AVIX") chunks — is read by calling
// ReadAll repeatedly until io.EOF.
func ReadAll(r io.Reader, opts ...ReaderOption) (*RIFFChunk, error) {
	return read(r, nil, -1, opts)
}

// ReadSections reads one RIFF chunk from r, skipping over the sub-chunk bodies
// and returning *SectionSubChunk values that read them from r on demand; use
// it for files too large to hold in memory. All boundaries are relative to the
// position r is at when the call is made, so a RIFF chunk embedded mid-stream
// can be read in place.
//
// Like ReadAll, it yields io.EOF when the input ends before the root chunk
// header, and with AllowTrailingData it leaves r right after the root chunk,
// so repeated calls read a stream of concatenated RIFF chunks.
func ReadSections(r ReadSeekerAt, opts ...ReaderOption) (*RIFFChunk, error) {
	// the sub-chunk bodies are skipped by seeking rather than read, so the size of the
	// input has to be known up front to notice that it is shorter than its headers claim.
	origin, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, fmt.Errorf("riffbin: get seek position: %w", err)
	}
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, fmt.Errorf("riffbin: seek to end: %w", err)
	}
	if _, err = r.Seek(origin, io.SeekStart); err != nil {
		return nil, fmt.Errorf("riffbin: seek: %w", err)
	}

	return read(r, r, size-origin, opts)
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
	pr   ReadSeekerAt // non-nil for ReadSections
	conf readerConfig

	// limit is the byte length of the input, or -1 when it is unknown.
	limit int64

	order binary.ByteOrder
	path  []string

	// pending holds the byte probed where a pad byte was expected but a chunk header
	// was found instead. It is only ever set with AllowPaddingViolations.
	pending     bool
	pendingByte byte

	// tolerateTrailingPad is true when the chunk read last has an odd-sized body whose
	// pad byte is not counted in its parent's size, so a single 0x00 may follow the
	// root chunk. See verifyEnd.
	tolerateTrailingPad bool
}

func read(r io.Reader, pr ReadSeekerAt, limit int64, opts []ReaderOption) (*RIFFChunk, error) {
	p := &parser{src: &offsetReader{r: r}, pr: pr, limit: limit}
	for _, o := range opts {
		o.apply(&p.conf)
	}

	// read header. a clean end of input before its first byte is io.EOF, not a syntax
	// error, so that a stream of concatenated RIFF chunks can be read until it runs dry.
	var buf [HeaderBytes]byte
	if _, err := io.ReadFull(p.src, buf[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, p.syntaxError(0, "unexpected end of input while reading the root chunk header")
		}
		return nil, err
	}

	// verify id
	var id FourCC
	copy(id[:], buf[:IDBytes])
	var byteOrder ByteOrder
	switch id {
	case riffID:
		byteOrder = LittleEndian
	case rifxID:
		byteOrder = BigEndian
	case rf64ID, bw64ID, ffirID, xfirID:
		return nil, fmt.Errorf("%w: %s containers are not supported", ErrUnsupportedFormat, id)
	default:
		return nil, p.syntaxError(0, "root chunk ID is %q, want %q or %q", id, riffID, rifxID)
	}
	p.order = byteOrder.binary()

	bodyLen := int64(p.order.Uint32(buf[IDBytes:]))
	end := p.src.off + bodyLen
	if p.limit >= 0 && end > p.limit {
		return nil, p.syntaxError(IDBytes, "root chunk declares a %d byte body but the input holds only %d byte(s)", bodyLen, p.limit-p.src.off)
	}

	formType, payload, err := p.readGroupBody(id, end)
	if err != nil {
		return nil, err
	}

	if err = p.verifyEnd(); err != nil {
		return nil, err
	}

	return &RIFFChunk{ByteOrder: byteOrder, FormType: formType, Payload: payload}, nil
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

// readGroupBody reads the body of a grouped chunk: its group type followed by its sub-chunks.
// end is the absolute offset at which the body ends.
func (p *parser) readGroupBody(id FourCC, end int64) (FourCC, []Chunk, error) {
	if len(p.path) >= maxGroupDepth {
		return FourCC{}, nil, p.syntaxError(p.src.off, "chunks are nested deeper than %d levels", maxGroupDepth)
	}
	p.path = append(p.path, id.String())
	defer func() { p.path = p.path[:len(p.path)-1] }()

	// read type
	if remain := end - p.src.off; remain < TypeBytes {
		return FourCC{}, nil, p.syntaxError(p.src.off, "%s chunk holds %d byte(s), too few for a group type", id, remain)
	}
	var groupType FourCC
	if err := p.readFull(groupType[:], "a group type"); err != nil {
		return FourCC{}, nil, err
	}
	if !groupType.Valid() {
		return FourCC{}, nil, p.syntaxError(p.src.off-TypeBytes, "group type %q is not printable ASCII", groupType[:])
	}
	p.path[len(p.path)-1] = id.String() + "(" + groupType.String() + ")"

	// read sub-chunks
	payload := []Chunk{}
	for p.src.off < end || p.pending {
		chunk, err := p.readChunk(end)
		if err != nil {
			return FourCC{}, nil, err
		}
		payload = append(payload, chunk)
	}

	return groupType, payload, nil
}

func (p *parser) readChunk(end int64) (Chunk, error) {
	// read header
	var buf [HeaderBytes]byte
	headerOff, read := p.src.off, 0
	if p.pending {
		buf[0], p.pending = p.pendingByte, false
		headerOff, read = headerOff-1, 1
	}
	if remain := end - p.src.off; remain < int64(HeaderBytes-read) {
		return nil, p.syntaxError(headerOff, "%d trailing byte(s) are too few for a chunk header", remain+int64(read))
	}
	if err := p.readFull(buf[read:], "a chunk header"); err != nil {
		return nil, err
	}

	var id FourCC
	copy(id[:], buf[:IDBytes])
	if !id.Valid() {
		return nil, p.syntaxError(headerOff, "chunk ID %q is not printable ASCII", id[:])
	}

	bodyLen := int64(p.order.Uint32(buf[IDBytes:]))
	if remain := end - p.src.off; bodyLen > remain {
		return nil, p.syntaxError(headerOff, "%s chunk declares a %d byte body but only %d byte(s) remain", id, bodyLen, remain)
	}

	var chunk Chunk
	switch id {
	case riffID, rifxID:
		// the specification allows a RIFF chunk only as the root chunk
		return nil, p.syntaxError(headerOff, "a %s chunk must not be nested", id)
	case listID:
		listType, payload, err := p.readGroupBody(id, p.src.off+bodyLen)
		if err != nil {
			return nil, err
		}
		chunk = &ListChunk{ListType: listType, Payload: payload}
	default:
		subChunk, err := p.readSubChunk(id, bodyLen)
		if err != nil {
			return nil, err
		}
		chunk = subChunk
	}

	if err := p.skipPadding(id, bodyLen, end); err != nil {
		return nil, err
	}
	return chunk, nil
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

func (p *parser) readSubChunk(id FourCC, bodyLen int64) (SubChunk, error) {
	if p.pr != nil {
		return p.createSectionSubChunk(id, bodyLen)
	}
	return p.createInMemorySubChunk(id, bodyLen)
}

func (p *parser) createInMemorySubChunk(id FourCC, bodyLen int64) (SubChunk, error) {
	chunk := &InMemorySubChunk{ID: id, Payload: []byte{}}
	if bodyLen == 0 {
		return chunk, nil
	}

	// read body payload. the declared size is untrusted even after the bounds check above,
	// because the enclosing chunk size it was checked against is untrusted too. growing the
	// buffer as bytes arrive keeps a bogus size field from forcing a huge allocation.
	off := p.src.off
	var buf bytes.Buffer
	buf.Grow(int(min(bodyLen, 64*1024)))
	if _, err := io.CopyN(&buf, p.src, bodyLen); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, p.syntaxError(off, "unexpected end of input while reading the %s chunk body", id)
		}
		return nil, fmt.Errorf("riffbin: read the %s chunk body: %w", id, err)
	}

	chunk.Payload = buf.Bytes()
	return chunk, nil
}

func (p *parser) createSectionSubChunk(id FourCC, bodyLen int64) (SubChunk, error) {
	// get seek position
	pos, err := p.pr.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, fmt.Errorf("riffbin: get seek position: %w", err)
	}

	// skip sub-chunk body
	if _, err = p.pr.Seek(bodyLen, io.SeekCurrent); err != nil {
		return nil, fmt.Errorf("riffbin: seek: %w", err)
	}
	p.src.off += bodyLen

	return &SectionSubChunk{ID: id, SectionReader: io.NewSectionReader(p.pr, pos, bodyLen)}, nil
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

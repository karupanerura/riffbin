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

// PartialReader is the input required by ReadSections.
type PartialReader interface {
	io.ReadSeeker
	io.ReaderAt
}

type readerConfig struct {
	allowUnpaddedChunks bool
	allowTrailingData   bool
}

// ReaderOption relaxes a rule of the RIFF specification for ReadFull and ReadSections.
// Without any option both readers are strict.
type ReaderOption interface {
	apply(*readerConfig)
}

type readerOptionFunc func(*readerConfig)

func (f readerOptionFunc) apply(c *readerConfig) { f(c) }

// AllowUnpaddedChunks accepts files that omit the pad byte after an odd-sized chunk body.
// The pad byte is then consumed only when it is 0x00; any other value is taken as the first
// byte of the next chunk header. riffbin up to v0.0.6 wrote such files.
func AllowUnpaddedChunks() ReaderOption {
	return readerOptionFunc(func(c *readerConfig) { c.allowUnpaddedChunks = true })
}

// AllowTrailingData ignores any bytes that follow the RIFF chunk instead of rejecting them.
// Without it a single 0x00 is still tolerated, because writers commonly append the pad byte
// of an odd-sized final chunk without counting it in the RIFF chunk size.
func AllowTrailingData() ReaderOption {
	return readerOptionFunc(func(c *readerConfig) { c.allowTrailingData = true })
}

// ReadFull reads RIFF binary from io.Reader.
// It creates *RIFFChunk with *OnMemorySubChunk for sub-chunks.
func ReadFull(r io.Reader, opts ...ReaderOption) (*RIFFChunk, error) {
	return read(r, nil, -1, opts)
}

// ReadSections reads RIFF binary from io.ReadSeeker to use less memory than ReadFull.
// It creates *RIFFChunk with *InStreamSubChunk for sub-chunks.
func ReadSections(r PartialReader, opts ...ReaderOption) (*RIFFChunk, error) {
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
	pr   PartialReader // non-nil for ReadSections
	conf readerConfig

	// limit is the byte length of the input, or -1 when it is unknown.
	limit int64

	order binary.ByteOrder
	path  []string

	// pending holds the byte probed where a pad byte was expected but a chunk header
	// was found instead. It is only ever set with AllowUnpaddedChunks.
	pending     bool
	pendingByte byte
}

func read(r io.Reader, pr PartialReader, limit int64, opts []ReaderOption) (*RIFFChunk, error) {
	p := &parser{src: &offsetReader{r: r}, pr: pr, limit: limit}
	for _, o := range opts {
		o.apply(&p.conf)
	}

	// read header
	var buf [HeaderBytes]byte
	if err := p.readFull(buf[:], "the root chunk header"); err != nil {
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

// verifyEnd rejects data beyond the root chunk. A single 0x00 is tolerated because the
// pad byte of an odd-sized final chunk is often written without being counted in the RIFF size.
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
	case buf[0] != 0x00:
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
	if bodyLen%2 == 0 || p.src.off >= end {
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
	if p.conf.allowUnpaddedChunks {
		// chunk IDs are printable ASCII, so a non-zero byte here is the head of the next header
		p.pending, p.pendingByte = true, buf[0]
		return nil
	}
	return p.syntaxError(off, "pad byte after the odd-sized %s chunk is %#02x, want 0x00", id, buf[0])
}

func (p *parser) readSubChunk(id FourCC, bodyLen int64) (SubChunk, error) {
	if p.pr != nil {
		return p.createInStreamSubChunk(id, bodyLen)
	}
	return p.createOnMemorySubChunk(id, bodyLen)
}

func (p *parser) createOnMemorySubChunk(id FourCC, bodyLen int64) (SubChunk, error) {
	chunk := &OnMemorySubChunk{ID: id, Payload: []byte{}}
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

func (p *parser) createInStreamSubChunk(id FourCC, bodyLen int64) (SubChunk, error) {
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

	return &InStreamSubChunk{ID: id, SectionReader: io.NewSectionReader(p.pr, pos, bodyLen)}, nil
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

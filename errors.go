package riffbin

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidFormat is the sentinel error for a malformed RIFF binary.
	// Parse errors are *SyntaxError values wrapping it, so use errors.Is to detect them.
	ErrInvalidFormat = errors.New("riffbin: invalid format")

	// ErrUnsupportedFormat is returned for a RIFF-like container that riffbin does not implement,
	// such as RF64/BW64 (64-bit sizes) or the word-swapped FFIR/XFIR variants.
	ErrUnsupportedFormat = errors.New("riffbin: unsupported format")

	// ErrRevokedBody is returned when a ChunkInfo.Body is read after the iteration
	// has moved on — advanced past the chunk, or ended, a break included. The
	// reader is revoked instead of misreading whatever comes next.
	ErrRevokedBody = errors.New("riffbin: chunk body read after the iteration moved on")

	// ErrChunkTooLarge is returned when a chunk body does not fit in the 32-bit RIFF
	// size field. A streaming body triggers it the moment its tree outgrows the
	// largest possible RIFF file, without draining the rest of the stream.
	ErrChunkTooLarge = errors.New("riffbin: chunk too large")

	// ErrSizeMismatch is returned when a sub-chunk's body disagrees with the
	// BodySize it declares — producing more or fewer bytes, in either case
	// judged by the bytes the destination accepted, whatever the body's own
	// WriteTo claims or swallows. A mismatch found mid-write stops there
	// rather than completing a corrupt file — a body is never copied past the
	// size its header declared — and the bytes already written remain in the
	// output. (A grouped chunk's size cannot mismatch: the writers derive it
	// from the children and never consult the group's BodySize.)
	ErrSizeMismatch = errors.New("riffbin: chunk body size mismatch")

	// ErrUnsupportedChunkType is returned when a Chunk implements neither GroupedChunk nor SubChunk.
	ErrUnsupportedChunkType = errors.New("riffbin: unsupported chunk type")

	// ErrUnexpectedStreamingChunk is returned when a Writer is given a streaming sub-chunk.
	ErrUnexpectedStreamingChunk = errors.New("riffbin: unexpected streaming chunk")

	// ErrUnwritableChunk is returned when a chunk tree cannot be written as a RIFF file that the
	// readers would accept: a FourCC that is not printable ASCII, a sub-chunk whose ID is a
	// structural ID such as "LIST", a grouped chunk below the root that is not a LIST,
	// chunks nested deeper than the readers read back, a sub-chunk whose Body is nil, or
	// a streaming sub-chunk built over a nil reader. Every case is caught while planning
	// the write, before a single byte reaches the output.
	ErrUnwritableChunk = errors.New("riffbin: unwritable chunk")

	// ErrConsumedStreamingChunk is returned when a streaming sub-chunk is written after its
	// body stream has already been consumed — or when its stream would be drained before
	// the write reaches it: the chunk is placed more than once in one tree, or another
	// chunk in the tree streams from the same reader. Either way the stream cannot
	// produce its payload again, and the write would silently emit an empty chunk.
	// Shared readers are recognized by pointer identity, which is what a reader with
	// a stream to consume is; a value that wraps one is not looked into.
	ErrConsumedStreamingChunk = errors.New("riffbin: streaming chunk already consumed")
)

// SyntaxError describes a malformed RIFF structure and where it was found.
// It wraps ErrInvalidFormat, so errors.Is(err, ErrInvalidFormat) reports true.
// It is reserved for defects of the input itself: an I/O failure of the
// underlying reader is never classified as a SyntaxError — it surfaces as the
// reader's own error, at most wrapped with context, so errors.Is matches it.
// The one reading the readers apply everywhere: an io.EOF or
// io.ErrUnexpectedEOF from the underlying reader means the input ended there —
// a clean io.EOF before the first byte of the root chunk header, a SyntaxError
// for the truncation inside the structure, and a clean end while probing for
// data after the root chunk.
type SyntaxError struct {
	// Offset is the byte offset at which the problem was detected, counted
	// from the position the reader was at when the read call was made.
	Offset int64

	// Path is the chunk path leading to the problem, e.g. `RIFF(WAVE)/LIST(INFO)`.
	// It is empty when the problem is in the root chunk header itself.
	Path string

	// Reason describes the problem.
	Reason string
}

var _ error = (*SyntaxError)(nil)

func (e *SyntaxError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("riffbin: invalid format: %s at offset %d", e.Reason, e.Offset)
	}
	return fmt.Sprintf("riffbin: invalid format: %s at offset %d in %s", e.Reason, e.Offset, e.Path)
}

func (e *SyntaxError) Unwrap() error { return ErrInvalidFormat }

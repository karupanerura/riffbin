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

	// ErrChunkTooLarge is returned when a chunk body does not fit in the 32-bit RIFF size field.
	ErrChunkTooLarge = errors.New("riffbin: chunk too large")

	// ErrSizeMismatch is returned when a sub-chunk produces a different number of bytes
	// than its BodySize reports — for a streaming sub-chunk, when BodySize disagrees
	// with the bytes just drained. The write stops where the mismatch is found rather
	// than completing a corrupt file; the bytes already written remain in the output.
	ErrSizeMismatch = errors.New("riffbin: chunk body size mismatch")

	// ErrUnsupportedChunkType is returned when a Chunk implements neither GroupedChunk nor SubChunk.
	ErrUnsupportedChunkType = errors.New("riffbin: unsupported chunk type")

	// ErrUnexpectedStreamingChunk is returned when a Writer is given a streaming sub-chunk.
	ErrUnexpectedStreamingChunk = errors.New("riffbin: unexpected streaming chunk")

	// ErrUnwritableChunk is returned when a chunk tree cannot be written as a RIFF file that the
	// readers would accept: a FourCC that is not printable ASCII, a sub-chunk whose ID is a
	// structural ID such as "LIST", a grouped chunk below the root that is not a LIST, or
	// chunks nested deeper than the readers read back.
	ErrUnwritableChunk = errors.New("riffbin: unwritable chunk")

	// ErrConsumedStreamingChunk is returned when a streaming sub-chunk is written after its
	// body stream has already been consumed, which would emit a header whose size counts bytes
	// that are no longer available.
	ErrConsumedStreamingChunk = errors.New("riffbin: streaming chunk already consumed")
)

// SyntaxError describes a malformed RIFF structure and where it was found.
// It wraps ErrInvalidFormat, so errors.Is(err, ErrInvalidFormat) reports true.
// It is reserved for defects of the input itself: an I/O failure of the
// underlying reader is never classified as a SyntaxError — it surfaces as the
// reader's own error, at most wrapped with context, so errors.Is matches it.
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

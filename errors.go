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

	// ErrChunkTooLarge is returned when a chunk body does not fit in the 32-bit RIFF size field.
	ErrChunkTooLarge = errors.New("riffbin: chunk too large")

	// ErrSizeMismatch is returned when a sub-chunk produces a different number of bytes than
	// its BodySize reports. Writing it would emit a corrupt file, so the write fails instead.
	ErrSizeMismatch = errors.New("riffbin: chunk body size mismatch")

	// ErrUnsupportedChunkType is returned when a Chunk implements neither GroupedChunk nor SubChunk.
	ErrUnsupportedChunkType = errors.New("riffbin: unsupported chunk type")

	// ErrUnexpectedIncompleteChunk is returned when a CompletedChunkWriter is given an incomplete sub-chunk.
	ErrUnexpectedIncompleteChunk = errors.New("riffbin: unexpected incomplete chunk")
)

// SyntaxError describes a malformed RIFF structure and where it was found.
// It wraps ErrInvalidFormat, so errors.Is(err, ErrInvalidFormat) reports true.
type SyntaxError struct {
	// Offset is the byte offset from the beginning of the input at which the problem was detected.
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

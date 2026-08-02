// Package riffbin reads and writes the Resource Interchange File Format (RIFF),
// the chunked container behind WAVE, AVI, WebP and many other formats.
//
// # Data model
//
// A RIFF file is a tree. The root is a [RIFFChunk], whose body is a form type
// followed by other chunks. A [ListChunk] nests further chunks under a list type;
// both implement [GroupedChunk], and they are the only two chunks the
// specification allows to contain other chunks. Every other chunk is a leaf
// carrying bytes and implements [SubChunk].
//
// Chunk IDs and group types are [FourCC] values, padded on the right with spaces,
// e.g. [MustParseFourCC]("fmt "). The specification defines them as ASCII alphanumeric;
// riffbin accepts any printable ASCII, matching real-world files.
//
// # Word alignment
//
// A chunk whose body has an odd length is followed by a single pad byte, which
// the specification requires to be zero. The pad byte is not counted in the
// chunk's own size field, but it is counted in the size of the chunk that
// contains it. Both writers emit it.
//
// The readers require the pad byte wherever the enclosing size says there is
// room for one. Two deviations common in real files are still read without an
// option: a final chunk whose parent size stops right at the odd body, and a
// single 0x00 after the root chunk left by writers that append the last pad
// byte without counting it. [AllowOmittedPadding] additionally reads files
// that omit pad bytes entirely (riffbin up to v0.0.6 wrote such files, and
// e.g. Apple CoreAudio still writes them); [AllowGarbagePadding] reads files
// whose pad bytes hold garbage instead of zero, skipping them without
// inspection like the reference readers do. The two are mutually exclusive
// ([ErrConflictingOptions]): a printable garbage pad is indistinguishable from
// the next header of an unpadded file, so each option declares which way that
// byte reads — declare the deviation the input actually has.
//
// # Reading
//
// [ReadAll] accepts any io.Reader and materializes every sub-chunk body in
// memory as an [InMemorySubChunk]. [ReadSections] needs a [ReadSeekerAt] and
// skips the bodies, returning a [SectionSubChunk] that reads from the original
// stream on demand; use it for files whose payloads are too large to hold in
// memory — its tree still grows with the number of chunks. [Chunks]
// builds no tree at all: it yields every chunk in document order as the input
// is scanned, keeping memory proportional to the nesting depth — for files
// with too many chunks to hold even their headers, such as an AVI file, which
// stores one chunk per video frame. [Walk] iterates the same way over an
// already-parsed tree. An input that ends before the first byte of the root
// chunk header yields io.EOF.
//
// Every reader is strict by default. Malformed input yields a [SyntaxError],
// which carries the byte offset and the chunk path and wraps [ErrInvalidFormat];
// an I/O failure of the underlying reader surfaces as the reader's own error,
// matched with errors.Is, and never as a format error:
//
//	chunk, err := riffbin.ReadAll(r)
//	if errors.Is(err, riffbin.ErrInvalidFormat) {
//		// ...
//	}
//
// [AllowOmittedPadding], [AllowGarbagePadding] and [AllowTrailingData] relax
// individual rules for files that do not follow the specification. With
// [AllowTrailingData] a call consumes
// exactly one root chunk and leaves the input right after it, so a stream of
// concatenated RIFF chunks — the layout AVI 2.0 uses to grow past the 32-bit
// size field by appending RIFF("AVIX") chunks — is read by calling [ReadAll]
// or [ReadSections] repeatedly until io.EOF; [Concatenated] wraps that loop
// as an iterator. A pad byte that a chunk's writer left uncounted in its RIFF
// size is skipped before the next chunk of such a stream.
//
// # Writing
//
// [Writer] writes a tree whose sizes are all known up front.
// [StreamingWriter] additionally accepts a [StreamingSubChunk], whose body
// comes from an io.Reader of unknown length; it writes placeholder sizes and
// seeks back to fix them once the stream has been consumed, so it needs an
// io.WriteSeeker.
//
// A non-streaming sub-chunk can be written repeatedly: [SubChunk.Body] hands out an
// independent reader on every call. A streaming sub-chunk is a [StreamingSubChunk] —
// or a type embedding one — and is sized from the bytes its stream actually
// produces, never from what it reports. Before the first byte is written, the tree is
// checked against what the readers accept: a non-ASCII FourCC, a sub-chunk using a
// structural ID such as "LIST", a nested RIFF chunk or nesting too deep to read back
// fails with [ErrUnwritableChunk], and a streaming sub-chunk whose stream was already
// consumed — or one placed twice in the tree, which would find it consumed — fails
// with [ErrConsumedStreamingChunk]. Sizes the tree misdeclares fail up front where
// they are checkable: a grouped chunk reporting anything but what its type and
// children encode to fails with [ErrSizeMismatch], one above 4 GiB with
// [ErrChunkTooLarge]. A body that produces a different number of bytes than it
// declares is only caught as it is copied, failing with [ErrSizeMismatch] where the
// write stops — the header and part of the body are already emitted; the copy never
// runs past the declared size, so even an endless body fails right at that boundary.
// A streaming body, which has no declared size, is capped where its tree outgrows
// the largest possible RIFF file, failing with [ErrChunkTooLarge] as it streams.
//
// Each WriteChunk call reads the tree exactly once: the check above snapshots
// every ChunkID, GroupType, Children and BodySize, and the write pass works
// from the snapshot alone, calling back into the tree only for [SubChunk.Body]
// on each non-streaming leaf — a copy bounded by the snapshotted size — and to
// drain the streaming bodies captured with it. Sizes and offsets are measured
// on the destination side of every copy. A method answering differently once
// planning is over, or a body's WriteTo misreporting its count, therefore
// cannot desynchronize the size fields from the bytes actually written or
// drive the writers into unbounded recursion; a panic raised inside the
// implementation's own methods still propagates.
//
// # Byte order
//
// Setting [RIFFChunk.ByteOrder] to [BigEndian] selects RIFX, the big-endian variant
// of RIFF: only the size fields change, four-character codes keep their order.
// The zero value, [LittleEndian], is ordinary RIFF. Readers detect the variant from
// the root chunk ID and record it, so a file round-trips byte for byte.
//
// RF64 and BW64 (64-bit sizes for files above 4 GiB) and the word-swapped FFIR and
// XFIR variants are recognized but not implemented; reading one reports
// [ErrUnsupportedFormat]. Only the root chunk is dispatched this way: a nested chunk
// with such an ID is treated as an opaque sub-chunk.
package riffbin

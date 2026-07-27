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
// e.g. [MustFourCC]("fmt "). The specification defines them as ASCII alphanumeric;
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
// byte without counting it. [AllowUnpaddedChunks] additionally reads files
// that omit pad bytes entirely (riffbin up to v0.0.6 wrote such files, and
// e.g. Apple CoreAudio still writes them) or whose pad bytes hold garbage
// instead of zero.
//
// # Reading
//
// [ReadFull] accepts any io.Reader and materializes every sub-chunk body in
// memory as an [OnMemorySubChunk]. [ReadSections] needs a [PartialReader] and
// skips the bodies, returning an [InStreamSubChunk] that reads from the original
// stream on demand; use it for files too large to hold in memory. An input that
// ends before the first byte of the root chunk header yields io.EOF.
//
// Both readers are strict by default. Malformed input yields a [SyntaxError],
// which carries the byte offset and the chunk path and wraps [ErrInvalidFormat];
// an I/O failure of the underlying reader is returned as is:
//
//	chunk, err := riffbin.ReadFull(r)
//	if errors.Is(err, riffbin.ErrInvalidFormat) {
//		// ...
//	}
//
// [AllowUnpaddedChunks] and [AllowTrailingData] relax individual rules for files
// that do not follow the specification. With [AllowTrailingData] a call consumes
// exactly one root chunk and leaves the input right after it, so a stream of
// concatenated RIFF chunks — the layout AVI 2.0 uses to grow past the 32-bit
// size field by appending RIFF("AVIX") chunks — is read by calling [ReadFull]
// or [ReadSections] repeatedly until io.EOF.
//
// # Writing
//
// [CompletedChunkWriter] writes a tree whose sizes are all known up front.
// [IncompleteChunkWriter] additionally accepts an [IncompleteSubChunk], whose body
// comes from an io.Reader of unknown length; it writes placeholder sizes and
// seeks back to fix them once the stream has been consumed, so it needs an
// io.WriteSeeker.
//
// A completed sub-chunk can be written repeatedly: [SubChunk.Body] hands out an
// independent reader on every call. Before the first byte is written, the tree is
// checked against what the readers accept: a non-ASCII FourCC, a sub-chunk using a
// structural ID such as "LIST", or a nested RIFF chunk fails with [ErrInvalidChunk],
// and an incomplete sub-chunk whose stream was already consumed fails with
// [ErrConsumedIncompleteChunk]. A write that would produce a file inconsistent
// with the declared sizes fails with [ErrSizeMismatch] or [ErrChunkTooLarge] rather
// than emitting corrupt bytes.
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

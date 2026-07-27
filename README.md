# github.com/karupanerura/riffbin ![](https://github.com/karupanerura/riffbin/workflows/test/badge.svg?branch=main) [![Go Reference](https://pkg.go.dev/badge/github.com/karupanerura/riffbin.svg)](https://pkg.go.dev/github.com/karupanerura/riffbin) [![codecov.io](https://codecov.io/github/karupanerura/riffbin/coverage.svg?branch=main)](https://codecov.io/github/karupanerura/riffbin?branch=main)

Go library for reading and writing the Resource Interchange File Format (RIFF),
the chunked container behind WAVE, AVI, WebP and many other formats.

# Features

* Builds, writes and parses RIFF chunk trees
* Writes a chunk body straight from an `io.Reader` of unknown length, fixing the
  size fields afterwards
* Parses in memory, or lazily from a seekable stream
* Reads streams of concatenated RIFF chunks (the AVI 2.0 layout)
* RIFX (big-endian RIFF) in both directions
* Strict about the specification, with opt-in leniency for files that are not
* Ships `cmd/riffdump` to print the chunk tree of a RIFF file

# Motivation

No RIFF library in Go could write a file without knowing every chunk size up
front. riffbin can, so e.g. a live recording streams straight into a WAVE file.

# Data model

A RIFF file is a tree. `*RIFFChunk` is the root, `*ListChunk` nests further chunks;
both implement `GroupedChunk`, and they are the only two chunks the specification
allows to contain other chunks. Everything else is a leaf implementing `SubChunk`:

| type | payload |
| --- | --- |
| `*OnMemorySubChunk` | a `[]byte` you own |
| `*InStreamSubChunk` | a section of a seekable stream, read on demand |
| `*IncompleteSubChunk` | an `io.Reader` whose length is not known in advance |

Chunk IDs and group types are `FourCC` values, padded on the right with spaces. The
specification defines them as ASCII alphanumeric; riffbin accepts any printable ASCII,
matching the identifiers found in real-world files. Use a literal
(`[4]byte{'f', 'm', 't', ' '}`) or `riffbin.MustFourCC("fmt")`.

## Word alignment

A chunk whose body has an odd length is followed by a single pad byte, which the
specification requires to be zero. The pad byte is not counted in the chunk's own
size field, but it is counted in the size of the chunk containing it. Both writers
emit it.

The readers require the pad byte whenever the enclosing size says there is room for
one. Two deviations common in real files are still read without an option: a final
chunk whose pad byte was left uncounted, and the single trailing `0x00` such a file
ends with. `AllowUnpaddedChunks` additionally reads files that omit pad bytes
entirely or whose pad bytes hold garbage instead of zero (see Example 4).

# Examples

## Example 1: write a WAVE file

```go
_, err := riffbin.NewCompletedChunkWriter(w).WriteChunk(&riffbin.RIFFChunk{
	FormType: riffbin.MustFourCC("WAVE"),
	Payload: []riffbin.Chunk{
		&riffbin.OnMemorySubChunk{
			ID: riffbin.MustFourCC("fmt"),
			Payload: []byte{
				0x01, 0x00, // Compression Code (Linear PCM)
				0x01, 0x00, // Number of channels (Monoral)
				0x44, 0xAC, 0x00, 0x00, // Sample rate (44.1kHz)
				0x44, 0xAC, 0x00, 0x00, // Average bytes per second (44.1kHz/Monoral)
				0x01, 0x00, // Block align (8bit/Monoral)
				0x08, 0x00, // Significant bits per sample (8bit)
			},
		},
		&riffbin.OnMemorySubChunk{
			ID:      riffbin.MustFourCC("data"),
			Payload: pcm, // []byte
		},
	},
})
```

## Example 2: write a WAVE file from an io.Reader

The body size of an `IncompleteSubChunk` is only known once its reader is drained, so
`IncompleteChunkWriter` writes placeholder sizes and seeks back to fix every affected
chunk header. It therefore needs an `io.WriteSeeker`.

```go
w, err := riffbin.NewIncompleteChunkWriter(f)
if err != nil {
	panic(err)
}

_, err = w.WriteChunk(&riffbin.RIFFChunk{
	FormType: riffbin.MustFourCC("WAVE"),
	Payload: []riffbin.Chunk{
		&riffbin.OnMemorySubChunk{
			ID:      riffbin.MustFourCC("fmt"),
			Payload: fmtChunkPayload,
		},
		riffbin.NewIncompleteSubChunk(riffbin.MustFourCC("data"), r),
	},
})
```

## Example 3: read a RIFF file

`ReadFull` takes any `io.Reader` and holds every body in memory. `ReadSections` takes
a `PartialReader` (`io.ReadSeeker` + `io.ReaderAt`) and only records where each body
lives, so it can open files far larger than memory.

```go
f, err := os.Open("sample.wav")
if err != nil {
	panic(err)
}
defer f.Close()

riffChunk, err := riffbin.ReadSections(f)
if err != nil {
	var syntaxErr *riffbin.SyntaxError
	if errors.As(err, &syntaxErr) {
		log.Fatalf("%s at offset %d in %s", syntaxErr.Reason, syntaxErr.Offset, syntaxErr.Path)
	}
	log.Fatal(err)
}

for _, chunk := range riffChunk.Payload {
	if sub, ok := chunk.(riffbin.SubChunk); ok && sub.ChunkID() == riffbin.MustFourCC("data") {
		io.Copy(os.Stdout, sub.Body())
	}
}
```

Malformed input produces a `*SyntaxError` wrapping `ErrInvalidFormat`, so
`errors.Is(err, riffbin.ErrInvalidFormat)` still classifies it. An I/O failure of the
underlying reader is returned as is, never classified as a format error.

## Example 4: read files that do not follow the specification

```go
// accept a missing pad byte after an odd-sized chunk (riffbin <= v0.0.6 wrote such
// files, and e.g. Apple CoreAudio still writes them), or a pad byte holding garbage
riffChunk, err := riffbin.ReadFull(r, riffbin.AllowUnpaddedChunks())

// ignore whatever follows the RIFF chunk
riffChunk, err = riffbin.ReadFull(r, riffbin.AllowTrailingData())
```

## Example 5: read concatenated RIFF chunks

With `AllowTrailingData` each call consumes exactly one root chunk, so a stream of
concatenated RIFF chunks — the layout AVI 2.0 uses to grow past the 32-bit size field
by appending `RIFF('AVIX')` chunks — is read by calling the reader repeatedly. An input
that ends before the first byte of a root chunk header yields `io.EOF`.

```go
for {
	riffChunk, err := riffbin.ReadFull(r, riffbin.AllowTrailingData())
	if errors.Is(err, io.EOF) {
		break // end of the stream
	}
	if err != nil {
		log.Fatal(err)
	}
	_ = riffChunk
}
```

## Example 6: RIFX (big-endian RIFF)

Only the size fields change; four-character codes keep their order. The variant is
detected when reading and recorded on the chunk, so a file round-trips byte for byte.

```go
_, err := riffbin.NewCompletedChunkWriter(w).WriteChunk(&riffbin.RIFFChunk{
	ByteOrder: riffbin.BigEndian,
	FormType:  riffbin.MustFourCC("TEST"),
	Payload:   payload,
})
```

# Limitations

* RF64 / BW64 (64-bit sizes for files above 4 GiB) are recognized but not implemented;
  reading one reports `ErrUnsupportedFormat`.
* The word-swapped FFIR / XFIR variants are likewise reported as unsupported.
* Chunks are nested at most 100 levels deep.

# Migrating from v0.1.0

v0.2.0 fixes a parser that could not read a `LIST` containing sub-chunks via
`ReadSections`, and reworks the API around the RIFF specification. The changes are
mechanical:

| v0.1.0 | v0.2.0 |
| --- | --- |
| `Chunk.ChunkID() []byte` | `Chunk.ChunkID() FourCC` — compare with `==`, print with `%s` |
| `Chunk.BodySize() uint32` | `Chunk.BodySize() int64` — sizes above 4 GiB now fail instead of wrapping |
| `SubChunk` embeds `io.Reader` | `SubChunk.Body() io.Reader` — a fresh reader on every call |
| `ChunkWriter.Write(*RIFFChunk)` | `ChunkWriter.WriteChunk(*RIFFChunk)` |
| unexported grouped-chunk interface | exported `GroupedChunk` |
| `err == riffbin.ErrInvalidFormat` | `errors.Is(err, riffbin.ErrInvalidFormat)` |
| an empty input was `ErrInvalidFormat` | it is `io.EOF`, the clean end of a chunk stream |
| unpadded files read silently | pass `riffbin.AllowUnpaddedChunks()` |

Input that used to be accepted silently — a nested `RIFF` chunk, a non-ASCII chunk ID, a
truncated body — is now rejected. The writers validate the tree before emitting anything:
a tree the readers would not accept fails with `ErrInvalidChunk`, one above 4 GiB with
`ErrChunkTooLarge`, and an already-consumed `IncompleteSubChunk` with
`ErrConsumedIncompleteChunk`.

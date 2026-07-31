# github.com/karupanerura/riffbin ![](https://github.com/karupanerura/riffbin/workflows/test/badge.svg?branch=main) [![Go Reference](https://pkg.go.dev/badge/github.com/karupanerura/riffbin.svg)](https://pkg.go.dev/github.com/karupanerura/riffbin) [![codecov.io](https://codecov.io/github/karupanerura/riffbin/coverage.svg?branch=main)](https://codecov.io/github/karupanerura/riffbin?branch=main)

Go library for reading and writing the Resource Interchange File Format (RIFF),
the chunked container behind WAVE, AVI, WebP and many other formats.

# Features

* Builds, writes and parses RIFF chunk trees
* Writes a chunk body straight from an `io.Reader` of unknown length, fixing the
  size fields afterwards
* Parses in memory, lazily from a seekable stream, or as an iterator that builds
  no tree at all — memory stays proportional to the nesting depth
* Iterates parsed trees (`Walk`) and streams of concatenated RIFF chunks
  (`Concatenated`, the AVI 2.0 layout)
* RIFX (big-endian RIFF) in both directions
* Strict about the specification, with opt-in leniency for files that are not
* Ships `cmd/riffdump` to print the chunk tree of a RIFF file

Requires Go 1.26.

# Motivation

No RIFF library in Go could write a file without knowing every chunk size up
front. riffbin can, so e.g. a live recording streams straight into a WAVE file.

# Data model

A RIFF file is a tree. `*RIFFChunk` is the root, `*ListChunk` nests further chunks;
both implement `GroupedChunk`, and they are the only two chunks the specification
allows to contain other chunks. Everything else is a leaf implementing `SubChunk`:

| type | payload |
| --- | --- |
| `*InMemorySubChunk` | a `[]byte` you own |
| `*SectionSubChunk` | a section of a seekable stream, read on demand |
| `*StreamingSubChunk` | an `io.Reader` whose length is not known in advance |

Chunk IDs and group types are `FourCC` values, padded on the right with spaces. The
specification defines them as ASCII alphanumeric; riffbin accepts any printable ASCII,
matching the identifiers found in real-world files. Use a literal
(`[4]byte{'f', 'm', 't', ' '}`) or `riffbin.MustParseFourCC("fmt")`.

## Word alignment

A chunk whose body has an odd length is followed by a single pad byte, which the
specification requires to be zero. The pad byte is not counted in the chunk's own
size field, but it is counted in the size of the chunk containing it. Both writers
emit it.

The readers require the pad byte whenever the enclosing size says there is room for
one. Two deviations common in real files are still read without an option: a final
chunk whose pad byte was left uncounted, and the single trailing `0x00` such a file
ends with. `AllowPaddingViolations` additionally reads files that omit pad bytes
entirely or whose pad bytes hold garbage instead of zero (see Example 4).

# Examples

## Example 1: write a WAVE file

```go
_, err := riffbin.NewWriter(w).WriteChunk(&riffbin.RIFFChunk{
	FormType: riffbin.MustParseFourCC("WAVE"),
	Payload: []riffbin.Chunk{
		&riffbin.InMemorySubChunk{
			ID: riffbin.MustParseFourCC("fmt"),
			Payload: []byte{
				0x01, 0x00, // Compression Code (Linear PCM)
				0x01, 0x00, // Number of channels (Monoral)
				0x44, 0xAC, 0x00, 0x00, // Sample rate (44.1kHz)
				0x44, 0xAC, 0x00, 0x00, // Average bytes per second (44.1kHz/Monoral)
				0x01, 0x00, // Block align (8bit/Monoral)
				0x08, 0x00, // Significant bits per sample (8bit)
			},
		},
		&riffbin.InMemorySubChunk{
			ID:      riffbin.MustParseFourCC("data"),
			Payload: pcm, // []byte
		},
	},
})
```

## Example 2: write a WAVE file from an io.Reader

The body size of an `StreamingSubChunk` is only known once its reader is drained, so
`StreamingWriter` writes placeholder sizes and seeks back to fix every affected
chunk header. It therefore needs an `io.WriteSeeker`.

```go
w, err := riffbin.NewStreamingWriter(f)
if err != nil {
	panic(err)
}

_, err = w.WriteChunk(&riffbin.RIFFChunk{
	FormType: riffbin.MustParseFourCC("WAVE"),
	Payload: []riffbin.Chunk{
		&riffbin.InMemorySubChunk{
			ID:      riffbin.MustParseFourCC("fmt"),
			Payload: fmtChunkPayload,
		},
		riffbin.NewStreamingSubChunk(riffbin.MustParseFourCC("data"), r),
	},
})
```

## Example 3: read a RIFF file

`ReadAll` takes any `io.Reader` and holds every body in memory. `ReadSections` takes
a `ReadSeekerAt` (`io.ReadSeeker` + `io.ReaderAt`) and only records where each body
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

// Walk iterates the tree in depth-first document order; break stops the walk.
for chunk := range riffbin.Walk(riffChunk) {
	if sub, ok := chunk.(riffbin.SubChunk); ok && sub.ChunkID() == riffbin.MustParseFourCC("data") {
		io.Copy(os.Stdout, sub.Body())
		break
	}
}
```

Malformed input produces a `*SyntaxError` wrapping `ErrInvalidFormat`, so
`errors.Is(err, riffbin.ErrInvalidFormat)` still classifies it. An I/O failure of the
underlying reader surfaces as the reader's own error — match it with `errors.Is` —
and is never classified as a format error.

## Example 4: read files that do not follow the specification

```go
// accept a missing pad byte after an odd-sized chunk (riffbin <= v0.0.6 wrote such
// files, and e.g. Apple CoreAudio still writes them), or a pad byte holding garbage
riffChunk, err := riffbin.ReadAll(r, riffbin.AllowPaddingViolations())

// ignore whatever follows the RIFF chunk
riffChunk, err = riffbin.ReadAll(r, riffbin.AllowTrailingData())
```

## Example 5: read concatenated RIFF chunks

A stream of concatenated RIFF chunks — the layout AVI 2.0 uses to grow past the
32-bit size field by appending `RIFF('AVIX')` chunks — is an iterator away:

```go
for riffChunk, err := range riffbin.Concatenated(r) {
	if err != nil {
		log.Fatal(err)
	}
	_ = riffChunk
}
```

(`Concatenated` wraps calling `ReadAll` with `AllowTrailingData` until `io.EOF`,
which remains available when each chunk needs different handling.)

## Example 6: stream chunks without building a tree

`Chunks` yields every chunk in document order as the input is scanned. No tree is
built, memory stays proportional to the nesting depth, and breaking out of the
loop stops reading — here neither the huge `data` body nor anything after `fmt `
is ever loaded:

```go
for info, err := range riffbin.Chunks(f) {
	if err != nil {
		log.Fatal(err)
	}
	if !info.Grouped() && info.ID == riffbin.MustParseFourCC("fmt") {
		fmtBody, err := io.ReadAll(info.Body) // read before break: Body dies with the iteration
		if err != nil {
			log.Fatal(err)
		}
		_ = fmtBody
		break
	}
}
```

## Example 7: RIFX (big-endian RIFF)

Only the size fields change; four-character codes keep their order. The variant is
detected when reading and recorded on the chunk, so a file round-trips byte for byte.

```go
_, err := riffbin.NewWriter(w).WriteChunk(&riffbin.RIFFChunk{
	ByteOrder: riffbin.BigEndian,
	FormType:  riffbin.MustParseFourCC("TEST"),
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
`ReadSections`, and reworks the API around the RIFF specification and the Go naming
conventions. The changes are mechanical:

| v0.1.0 | v0.2.0 |
| --- | --- |
| `ReadFull` | `ReadAll` — it reads everything into memory, like `io.ReadAll`; `io.ReadFull` means something else |
| `PartialReader` | `ReadSeekerAt`, named for its abilities like `io.ReadWriteSeeker` |
| `CompletedChunkWriter`, `NewCompletedChunkWriter` | `Writer`, `NewWriter` |
| `IncompleteChunkWriter`, `NewIncompleteChunkWriter` | `StreamingWriter`, `NewStreamingWriter` |
| `OnMemorySubChunk` | `InMemorySubChunk` |
| `InStreamSubChunk` | `SectionSubChunk`, matching `ReadSections` and `io.SectionReader` |
| `IncompleteSubChunk`, `NewIncompleteSubChunk` | `StreamingSubChunk`, `NewStreamingSubChunk` — the size is unknown, not the data broken |
| `SubChunk.Incomplete()` | `SubChunk.Streaming()` |
| `ErrUnexpectedIncompleteChunk` | `ErrUnexpectedStreamingChunk` |
| `Chunk.ChunkID() []byte` | `Chunk.ChunkID() FourCC` — compare with `==`, print with `%s` |
| `Chunk.BodySize() uint32` | `Chunk.BodySize() int64` — sizes above 4 GiB now fail instead of wrapping |
| `SubChunk` embeds `io.Reader` | `SubChunk.Body() io.Reader` — a fresh reader on every call |
| `ChunkWriter.Write(*RIFFChunk)` | `ChunkWriter.WriteChunk(*RIFFChunk)` |
| unexported grouped-chunk interface | exported `GroupedChunk`; its contained chunks are `Children()`, since the specification calls every nested chunk — a `LIST` included — a subchunk |
| `err == riffbin.ErrInvalidFormat` | `errors.Is(err, riffbin.ErrInvalidFormat)` |
| an empty input was `ErrInvalidFormat` | it is `io.EOF`, the clean end of a chunk stream |
| unpadded files read silently | pass `riffbin.AllowPaddingViolations()` |

Input that used to be accepted silently — a nested `RIFF` chunk, a non-ASCII chunk ID, a
truncated body — is now rejected. The writers validate the tree before emitting anything:
a tree the readers would not accept fails with `ErrUnwritableChunk`, one above 4 GiB with
`ErrChunkTooLarge`, and an already-consumed `StreamingSubChunk` with
`ErrConsumedStreamingChunk`.

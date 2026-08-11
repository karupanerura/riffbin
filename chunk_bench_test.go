package riffbin_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/karupanerura/riffbin"
)

// benchChunkCount shapes the benchmark input like an AVI file: the chunk count
// dwarfs the payload bytes, since AVI stores one chunk per video frame.
const benchChunkCount = 200_000

// aviShapedFile builds a RIFF chunk holding one LIST of n small leaf chunks —
// n+2 chunks in a file of roughly 12*n bytes.
func aviShapedFile(n int) []byte {
	var list []byte
	list = append(list, "movi"...)
	for i := 0; i < n; i++ {
		list = append(list, "00dc"...)
		list = binary.LittleEndian.AppendUint32(list, 4)
		list = append(list, "\xde\xad\xbe\xef"...)
	}

	var f []byte
	f = append(f, "RIFF"...)
	f = binary.LittleEndian.AppendUint32(f, uint32(4+riffbin.HeaderBytes+len(list)))
	f = append(f, "AVI "...)
	f = append(f, "LIST"...)
	f = binary.LittleEndian.AppendUint32(f, uint32(len(list)))
	f = append(f, list...)
	return f
}

func BenchmarkReadAll(b *testing.B) {
	f := aviShapedFile(benchChunkCount)
	b.SetBytes(int64(len(f)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := riffbin.ReadAll(bytes.NewReader(f)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadSections(b *testing.B) {
	f := aviShapedFile(benchChunkCount)
	b.SetBytes(int64(len(f)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := riffbin.ReadSections(bytes.NewReader(f)); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkChunks drains every body, so its MB/s covers the same bytes as
// BenchmarkReadAll and the two are comparable; the tree building is the
// difference between them.
func BenchmarkChunks(b *testing.B) {
	f := aviShapedFile(benchChunkCount)
	b.SetBytes(int64(len(f)))
	b.ReportAllocs()
	var scratch [8]byte
	for b.Loop() {
		n := 0
		for info, err := range riffbin.Chunks(bytes.NewReader(f)) {
			if err != nil {
				b.Fatal(err)
			}
			if info.Body != nil {
				if _, err := io.ReadFull(info.Body, scratch[:info.BodySize]); err != nil {
					b.Fatal(err)
				}
			}
			n++
		}
		if n != benchChunkCount+2 {
			b.Fatalf("scanned %d chunks", n)
		}
	}
}

// BenchmarkChunksSkipBodies leaves every body unread — the cost of walking
// the structure alone. No SetBytes: it does not process the file's bytes.
func BenchmarkChunksSkipBodies(b *testing.B) {
	f := aviShapedFile(benchChunkCount)
	b.ReportAllocs()
	for b.Loop() {
		n := 0
		for _, err := range riffbin.Chunks(bytes.NewReader(f)) {
			if err != nil {
				b.Fatal(err)
			}
			n++
		}
		if n != benchChunkCount+2 {
			b.Fatalf("scanned %d chunks", n)
		}
	}
}

func BenchmarkChunksEarlyBreak(b *testing.B) {
	f := aviShapedFile(benchChunkCount)
	b.ReportAllocs()
	for b.Loop() {
		n := 0
		for info, err := range riffbin.Chunks(bytes.NewReader(f)) {
			if err != nil {
				b.Fatal(err)
			}
			n++
			if !info.Grouped() {
				break // stop at the first leaf, as a search would
			}
		}
		// the break must fire at the first leaf — a parser yielding nothing
		// would otherwise report a spectacular non-result
		if n != 3 {
			b.Fatalf("saw %d chunks before the first leaf, want 3", n)
		}
	}
}

// BenchmarkWriteSections writes a tree of SectionSubChunk leaves — bodies
// without a WriteTo — through a destination with a ReadFrom, the shape that
// must not cost io.Copy a scratch buffer per leaf.
func BenchmarkWriteSections(b *testing.B) {
	f := aviShapedFile(benchChunkCount)
	tree, err := riffbin.ReadSections(bytes.NewReader(f))
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(f)))
	b.ReportAllocs()
	var buf bytes.Buffer
	buf.Grow(len(f))
	for b.Loop() {
		buf.Reset()
		if _, err := riffbin.NewWriter(&buf).WriteChunk(tree); err != nil {
			b.Fatal(err)
		}
	}
}

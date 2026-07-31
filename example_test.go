package riffbin_test

import (
	"bytes"
	"errors"
	"io"
	"log"
	"os"
	"strings"

	"github.com/karupanerura/riffbin"
)

// The examples below mirror the README. They have no output comments, so they are
// compile-checked (and vetted) but not executed.

// Example 1 of the README: write a WAVE file.
func Example_writeWaveFile() {
	var w bytes.Buffer
	pcm := []byte{0x80, 0x80, 0x80, 0x80}

	_, err := riffbin.NewWriter(&w).WriteChunk(&riffbin.RIFFChunk{
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
	if err != nil {
		panic(err)
	}
}

// Example 2 of the README: write a WAVE file from an io.Reader.
func Example_writeFromReader() {
	f, err := os.CreateTemp("", "riffbin")
	if err != nil {
		panic(err)
	}
	defer os.Remove(f.Name())
	fmtChunkPayload := []byte{0x01, 0x00, 0x01, 0x00, 0x44, 0xAC, 0x00, 0x00, 0x44, 0xAC, 0x00, 0x00, 0x01, 0x00, 0x08, 0x00}
	r := strings.NewReader("pcm bytes of unknown length")

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
	if err != nil {
		panic(err)
	}
}

// Example 3 of the README: read a RIFF file.
func Example_read() {
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
}

// Example 4 of the README: read files that do not follow the specification.
func Example_leniency() {
	r := bytes.NewReader(nil)

	// accept a missing pad byte after an odd-sized chunk (riffbin <= v0.0.6 wrote such
	// files, and e.g. Apple CoreAudio still writes them), or a pad byte holding garbage
	riffChunk, err := riffbin.ReadAll(r, riffbin.AllowPaddingViolations())

	// ignore whatever follows the RIFF chunk
	riffChunk, err = riffbin.ReadAll(r, riffbin.AllowTrailingData())

	_, _ = riffChunk, err
}

// Example 5 of the README: read concatenated RIFF chunks.
func Example_concatenated() {
	var r io.Reader = bytes.NewReader(nil)

	for riffChunk, err := range riffbin.Concatenated(r) {
		if err != nil {
			log.Fatal(err)
		}
		_ = riffChunk
	}
}

// Example 6 of the README: stream chunks without building a tree.
func Example_streamChunks() {
	f, err := os.Open("sample.wav")
	if err != nil {
		panic(err)
	}
	defer f.Close()

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
}

// Example 7 of the README: RIFX (big-endian RIFF).
func Example_rifx() {
	var w bytes.Buffer
	payload := []riffbin.Chunk{
		&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("ENT1"), Payload: []byte("abc")},
	}

	_, err := riffbin.NewWriter(&w).WriteChunk(&riffbin.RIFFChunk{
		ByteOrder: riffbin.BigEndian,
		FormType:  riffbin.MustParseFourCC("TEST"),
		Payload:   payload,
	})
	if err != nil {
		panic(err)
	}
}

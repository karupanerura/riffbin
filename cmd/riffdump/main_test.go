package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/karupanerura/riffbin"
)

// oneChunk is RIFF(TEST){ ENT1["abc"] } with the pad byte counted in the root size.
var oneChunk = []byte{
	'R', 'I', 'F', 'F', 0x10, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
	'E', 'N', 'T', '1', 0x03, 0x00, 0x00, 0x00, 'a', 'b', 'c', 0x00,
}

const oneChunkDump = "RIFF[TEST:16]:\n" +
	"  ENT1[3]\n" +
	"    00000000  61 62 63                                          |abc|\n" +
	"    \n"

func TestDumpSingleChunk(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := dump(&out, bytes.NewReader(oneChunk), nil, false); err != nil {
		t.Fatal(err)
	}
	if out.String() != oneChunkDump {
		t.Errorf("unexpected dump:\n%q\nwant:\n%q", out.String(), oneChunkDump)
	}
}

// A concatenated stream without -concat is an error after the first chunk —
// a truncated dump must not pass as a complete one.
func TestDumpRejectsConcatenatedStreamWithoutConcat(t *testing.T) {
	t.Parallel()

	stream := append(append([]byte{}, oneChunk...), oneChunk...)
	var out bytes.Buffer
	err := dump(&out, bytes.NewReader(stream), nil, false)
	if !errors.Is(err, riffbin.ErrInvalidFormat) {
		t.Fatalf("should reject the trailing chunk but got: %v", err)
	}
}

// With -concat every chunk of the stream is dumped, not only the first.
func TestDumpConcatenatedStream(t *testing.T) {
	t.Parallel()

	stream := append(append([]byte{}, oneChunk...), oneChunk...)
	var out bytes.Buffer
	if err := dump(&out, bytes.NewReader(stream), nil, true); err != nil {
		t.Fatal(err)
	}
	if want := oneChunkDump + oneChunkDump; out.String() != want {
		t.Errorf("unexpected dump:\n%q\nwant:\n%q", out.String(), want)
	}
	if got := strings.Count(out.String(), "RIFF[TEST:16]"); got != 2 {
		t.Errorf("should dump 2 root chunks but printed %d", got)
	}
}

// The padding policy is independent of -concat: an unpadded file needs only
// the policy, and must not silently accept trailing data.
func TestDumpPaddingPolicyDoesNotImplyTrailingData(t *testing.T) {
	t.Parallel()

	unpadded := []byte{
		'R', 'I', 'F', 'F', 0x0F, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
		'E', 'N', 'T', '1', 0x03, 0x00, 0x00, 0x00, 'a', 'b', 'c',
	}

	var out bytes.Buffer
	if err := dump(&out, bytes.NewReader(unpadded), []riffbin.ReaderOption{riffbin.PadOmitted}, false); err != nil {
		t.Fatalf("the padding policy should accept the file but got: %v", err)
	}

	withJunk := append(append([]byte{}, unpadded...), "junk"...)
	out.Reset()
	if err := dump(&out, bytes.NewReader(withJunk), []riffbin.ReaderOption{riffbin.PadOmitted}, false); !errors.Is(err, riffbin.ErrInvalidFormat) {
		t.Fatalf("trailing junk should stay an error under a padding policy but got: %v", err)
	}
}

func TestDumpEmptyInput(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := dump(&out, bytes.NewReader(nil), nil, false); !errors.Is(err, io.EOF) {
		t.Errorf("empty input should be io.EOF but got: %v", err)
	}
	out.Reset()
	if err := dump(&out, bytes.NewReader(nil), nil, true); !errors.Is(err, io.EOF) {
		t.Errorf("empty input should be io.EOF under -concat too but got: %v", err)
	}
}

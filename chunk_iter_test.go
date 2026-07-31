package riffbin_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/karupanerura/riffbin"
)

// onlyReader hides every ability of the wrapped reader but Read, forcing the
// streaming parser onto its read-through path.
type onlyReader struct{ io.Reader }

// chunkEvent is a ChunkInfo with the Body materialized, for comparison.
type chunkEvent struct {
	Depth      int
	ID         string
	GroupType  string
	BodySize   int64
	BodyOffset int64
	Body       string
}

func collectChunks(t *testing.T, r io.Reader, opts ...riffbin.ReaderOption) ([]chunkEvent, error) {
	t.Helper()

	var out []chunkEvent
	for info, err := range riffbin.Chunks(r, opts...) {
		if err != nil {
			return nil, err
		}
		ev := chunkEvent{
			Depth:      info.Depth,
			ID:         info.ID.String(),
			BodySize:   info.BodySize,
			BodyOffset: info.BodyOffset,
		}
		if info.Grouped() {
			if info.Body != nil {
				t.Errorf("%s: a grouped chunk must not carry a body reader", info.ID)
			}
			ev.GroupType = info.GroupType.String()
		} else {
			body, err := io.ReadAll(info.Body)
			if err != nil {
				return nil, err
			}
			if int64(len(body)) != info.BodySize {
				t.Errorf("%s: BodySize is %d but the body holds %d byte(s)", info.ID, info.BodySize, len(body))
			}
			ev.Body = string(body)
		}
		out = append(out, ev)
	}
	return out, nil
}

func TestChunks(t *testing.T) {
	t.Parallel()

	b := []byte{
		0x52, 0x49, 0x46, 0x46, // id (RIFF)
		0x1C, 0x00, 0x00, 0x00, // body size (4 + 8 + 16)
		0x54, 0x45, 0x53, 0x54, // type (TEST)
		0x4C, 0x49, 0x53, 0x54, // id (LIST)
		0x10, 0x00, 0x00, 0x00, // body size (4 + 8 + 3 + 1)
		0x4C, 0x53, 0x54, 0x31, // type (LST1)
		0x45, 0x4E, 0x54, 0x31, // id (ENT1)
		0x03, 0x00, 0x00, 0x00, // body size
		0x61, 0x62, 0x63, // "abc"
		0x00, // padding
	}
	expected := []chunkEvent{
		{Depth: 0, ID: "RIFF", GroupType: "TEST", BodySize: 28, BodyOffset: 8},
		{Depth: 1, ID: "LIST", GroupType: "LST1", BodySize: 16, BodyOffset: 20},
		{Depth: 2, ID: "ENT1", BodySize: 3, BodyOffset: 32, Body: "abc"},
	}

	t.Run("ReadThrough", func(t *testing.T) {
		t.Parallel()
		got, err := collectChunks(t, onlyReader{bytes.NewReader(b)})
		if err != nil {
			t.Fatal(err)
		}
		if df := cmp.Diff(expected, got); df != "" {
			t.Errorf("diff = %s", df)
		}
	})
	t.Run("Seeking", func(t *testing.T) {
		t.Parallel()
		got, err := collectChunks(t, bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if df := cmp.Diff(expected, got); df != "" {
			t.Errorf("diff = %s", df)
		}
	})
}

// pipeLikeReader satisfies ReadSeekerAt by type, but its stream cannot seek —
// like *os.File when it is a pipe or a terminal.
type pipeLikeReader struct{ *bytes.Reader }

func (pipeLikeReader) Seek(int64, int) (int64, error) {
	return 0, errors.New("illegal seek")
}

// A source that satisfies ReadSeekerAt by type but cannot actually seek must be
// read through, not fail before the first byte — Chunks(os.Stdin) on a pipe.
func TestChunksReadsThroughWhenSeekingFails(t *testing.T) {
	t.Parallel()

	expected, err := collectChunks(t, onlyReader{bytes.NewReader(paddedFileBytes)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := collectChunks(t, pipeLikeReader{bytes.NewReader(paddedFileBytes)})
	if err != nil {
		t.Fatal(err)
	}
	if df := cmp.Diff(expected, got); df != "" {
		t.Errorf("diff = %s", df)
	}
}

// A body left unread must be skipped, and BodyOffset must address it for later
// reads — the streaming equivalent of what ReadSections provides.
func TestChunksBodyOffsetAddressesUnreadBodies(t *testing.T) {
	t.Parallel()

	r := bytes.NewReader(paddedFileBytes)
	type ref struct {
		off, size int64
	}
	var refs []ref
	for info, err := range riffbin.Chunks(r) {
		if err != nil {
			t.Fatal(err)
		}
		if !info.Grouped() {
			refs = append(refs, ref{off: info.BodyOffset, size: info.BodySize})
		}
	}

	expected := []string{"abc", "wxyz"}
	if len(refs) != len(expected) {
		t.Fatalf("should have %d leaves but got: %d", len(expected), len(refs))
	}
	for i, rf := range refs {
		body, err := io.ReadAll(io.NewSectionReader(r, rf.off, rf.size))
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != expected[i] {
			t.Errorf("leaf %d should be %q but got: %q", i, expected[i], body)
		}
	}
}

// Breaking out of the loop stops reading; a Body kept across iterations is
// revoked rather than mis-reading the stream.
func TestChunksBodyIsRevokedWhenIterationAdvances(t *testing.T) {
	t.Parallel()

	var stale io.Reader
	for info, err := range riffbin.Chunks(onlyReader{bytes.NewReader(paddedFileBytes)}) {
		if err != nil {
			t.Fatal(err)
		}
		if info.Grouped() {
			continue
		}
		if stale == nil {
			stale = info.Body // keep the first leaf's body for later
			continue
		}
		if _, err := io.ReadAll(stale); !errors.Is(err, riffbin.ErrRevokedBody) {
			t.Errorf("reading a stale body should be ErrRevokedBody but got: %v", err)
		}
	}
}

// Ending the iteration revokes the body like advancing does: a break is not a
// way to keep the reader. BodyOffset addresses the body afterwards instead.
func TestChunksBodyIsRevokedWhenIterationEnds(t *testing.T) {
	t.Parallel()

	var kept io.Reader
	for info, err := range riffbin.Chunks(onlyReader{bytes.NewReader(paddedFileBytes)}) {
		if err != nil {
			t.Fatal(err)
		}
		if !info.Grouped() {
			kept = info.Body
			break
		}
	}
	if _, err := io.ReadAll(kept); !errors.Is(err, riffbin.ErrRevokedBody) {
		t.Errorf("reading the body after breaking out should be ErrRevokedBody but got: %v", err)
	}
}

func TestChunksEmptyInput(t *testing.T) {
	t.Parallel()

	for info, err := range riffbin.Chunks(bytes.NewReader(nil)) {
		if !errors.Is(err, io.EOF) {
			t.Errorf("should be io.EOF but got: %v (info: %+v)", err, info)
		}
	}
}

func TestWalk(t *testing.T) {
	t.Parallel()

	tree := nestedListTree()

	t.Run("DocumentOrder", func(t *testing.T) {
		t.Parallel()
		var ids []string
		for c := range riffbin.Walk(tree) {
			ids = append(ids, c.ChunkID().String())
		}
		expected := []string{"RIFF", "LIST", "ENT1", "LIST", "ENT2", "ENT3", "ENT4"}
		if df := cmp.Diff(expected, ids); df != "" {
			t.Errorf("diff = %s", df)
		}
	})

	t.Run("EarlyBreak", func(t *testing.T) {
		t.Parallel()
		var seen int
		for c := range riffbin.Walk(tree) {
			seen++
			if c.ChunkID() == riffbin.MustParseFourCC("ENT2") {
				break
			}
		}
		if seen != 5 {
			t.Errorf("should stop at the 5th chunk but saw %d", seen)
		}
	})
}

func TestConcatenated(t *testing.T) {
	t.Parallel()

	t.Run("TwoChunks", func(t *testing.T) {
		t.Parallel()
		b := append(append([]byte{}, paddedFileBytes...), paddedFileBytes...)
		expected := flattenTree(t, paddedFileChunk())

		var count int
		for chunk, err := range riffbin.Concatenated(bytes.NewReader(b)) {
			if err != nil {
				t.Fatal(err)
			}
			if df := cmp.Diff(expected, flattenTree(t, chunk)); df != "" {
				t.Errorf("chunk %d: diff = %s", count, df)
			}
			count++
		}
		if count != 2 {
			t.Errorf("should yield 2 chunks but got: %d", count)
		}
	})

	t.Run("EmptyInput", func(t *testing.T) {
		t.Parallel()
		for chunk, err := range riffbin.Concatenated(bytes.NewReader(nil)) {
			t.Errorf("should yield nothing but got: %v, %v", chunk, err)
		}
	})

	t.Run("ErrorEndsTheIteration", func(t *testing.T) {
		t.Parallel()
		b := append(append([]byte{}, paddedFileBytes...), "garbage!"...)
		var chunks, errs int
		for chunk, err := range riffbin.Concatenated(bytes.NewReader(b)) {
			if err != nil {
				errs++
				if chunk != nil {
					t.Error("the chunk should be nil alongside an error")
				}
				if !errors.Is(err, riffbin.ErrInvalidFormat) {
					t.Errorf("should be ErrInvalidFormat but got: %v", err)
				}
				continue
			}
			chunks++
		}
		if chunks != 1 || errs != 1 {
			t.Errorf("should yield 1 chunk and 1 error but got: %d and %d", chunks, errs)
		}
	})
}

// The streaming parser accepts a superset of nothing: exactly what the tree
// readers accept, under every combination of the reader options — the inputs
// are chosen so that each option flips some verdict. (The fuzz target extends
// this property to arbitrary inputs.)
func TestChunksAgreesWithTreeReaders(t *testing.T) {
	t.Parallel()

	inputs := map[string][]byte{
		"padded":  paddedFileBytes,
		"rifx":    rifxFileBytes,
		"garbage": []byte("RIFFgarbage!"),
		// an unpadded odd chunk with a sibling: strict fails on the pad byte
		"unpadded": {
			'R', 'I', 'F', 'F', 0x1B, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
			'E', 'N', 'T', '1', 0x03, 0x00, 0x00, 0x00, 'a', 'b', 'c',
			'E', 'N', 'T', '2', 0x04, 0x00, 0x00, 0x00, 'w', 'x', 'y', 'z',
		},
		// a pad byte holding garbage
		"garbagePad": {
			'R', 'I', 'F', 'F', 0x10, 0x00, 0x00, 0x00, 'T', 'E', 'S', 'T',
			'E', 'N', 'T', '1', 0x03, 0x00, 0x00, 0x00, 'a', 'b', 'c', 0xFF,
		},
		// data after the root chunk
		"trailingData": append(append([]byte{}, paddedFileBytes...), "junk"...),
		// the uncounted pad byte of a previous concatenated chunk
		"leadingPad": append([]byte{0x00}, paddedFileBytes...),
	}
	modes := map[string][]riffbin.ReaderOption{
		"strict":           nil,
		"paddingViolation": {riffbin.AllowPaddingViolations()},
		"trailingData":     {riffbin.AllowTrailingData()},
		"lenient":          {riffbin.AllowPaddingViolations(), riffbin.AllowTrailingData()},
	}
	for mode, opts := range modes {
		for name, b := range inputs {
			mode, opts, name, b := mode, opts, name, b
			t.Run(mode+"/"+name, func(t *testing.T) {
				t.Parallel()
				_, treeErr := riffbin.ReadAll(bytes.NewReader(b), opts...)
				_, chunksErr := collectChunks(t, onlyReader{bytes.NewReader(b)}, opts...)
				if (treeErr == nil) != (chunksErr == nil) {
					t.Errorf("the readers disagree: ReadAll=%v Chunks=%v", treeErr, chunksErr)
				}
			})
		}
	}
}

// A leaf body must be readable through any reader shape, including one byte at
// a time — Body reads straight from the source, so short reads must track
// BodySize exactly. (The 64 KiB materialization steps belong to ReadAll's
// tree building, which Chunks never runs.)
func TestChunksBodyByteAtATime(t *testing.T) {
	t.Parallel()

	for info, err := range riffbin.Chunks(onlyReader{bytes.NewReader(paddedFileBytes)}) {
		if err != nil {
			t.Fatal(err)
		}
		if info.Grouped() {
			continue
		}
		var body strings.Builder
		buf := make([]byte, 1)
		for {
			n, err := info.Body.Read(buf)
			body.Write(buf[:n])
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if int64(body.Len()) != info.BodySize {
			t.Errorf("%s: BodySize is %d but read %d byte(s)", info.ID, info.BodySize, body.Len())
		}
	}
}

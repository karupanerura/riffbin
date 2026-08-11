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

// seekerWithoutEnd seeks from the start and the current position but cannot
// be measured: SeekEnd fails, like a forward-only wrapper.
type seekerWithoutEnd struct{ *bytes.Reader }

func (r seekerWithoutEnd) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekEnd {
		return 0, errors.New("SeekEnd unsupported")
	}
	return r.Reader.Seek(offset, whence)
}

// Seeking is only an optimization for Chunks: a source that seeks but cannot
// be measured must be read through, not rejected — the same bytes parse when
// the value is handed over as a plain io.Reader.
func TestChunksReadsThroughWhenMeasureFails(t *testing.T) {
	t.Parallel()

	expected, err := collectChunks(t, onlyReader{bytes.NewReader(paddedFileBytes)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := collectChunks(t, seekerWithoutEnd{bytes.NewReader(paddedFileBytes)})
	if err != nil {
		t.Fatal(err)
	}
	if df := cmp.Diff(expected, got); df != "" {
		t.Errorf("diff = %s", df)
	}
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

// charDeviceReader mimics *os.File over a character device or a procfs file:
// a full ReadSeekerAt whose Seek succeeds but reports size 0 — /dev/zero,
// /dev/urandom and /dev/null all do — while Read and ReadAt serve real bytes.
type charDeviceReader struct{ r *bytes.Reader }

func (c *charDeviceReader) Read(p []byte) (int, error) { return c.r.Read(p) }
func (c *charDeviceReader) ReadAt(p []byte, off int64) (int, error) {
	return c.r.ReadAt(p, off)
}
func (c *charDeviceReader) Seek(off int64, whence int) (int64, error) {
	if whence == io.SeekEnd {
		return 0, nil // no size to report
	}
	return c.r.Seek(off, whence)
}

// A seekable source whose measurement contradicts its stream must be read
// through, not rejected: character devices and procfs files report size 0
// while their reads produce data, and seeking is only an optimization.
func TestChunksReadsThroughWhenMeasureLies(t *testing.T) {
	t.Parallel()

	expected, err := collectChunks(t, onlyReader{bytes.NewReader(paddedFileBytes)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := collectChunks(t, &charDeviceReader{r: bytes.NewReader(paddedFileBytes)})
	if err != nil {
		t.Fatal(err)
	}
	if df := cmp.Diff(expected, got); df != "" {
		t.Errorf("diff = %s", df)
	}

	if _, err := riffbin.ReadSections(&charDeviceReader{r: bytes.NewReader(paddedFileBytes)}); err != nil {
		t.Errorf("ReadSections should read through the lying measurement but got: %v", err)
	}
}

// On a truncated file the seeking path must yield exactly what the
// read-through path yields — the same intact chunks, then the same error at
// the same offset. Seeking is an optimization, not a different parser.
func TestChunksAgreeOnTruncatedInput(t *testing.T) {
	t.Parallel()

	full := append([]byte{}, paddedFileBytes...)
	for n := 0; n < len(full); n++ {
		truncated := full[:n]

		collect := func(r io.Reader) ([]chunkEvent, string) {
			var events []chunkEvent
			var errText string
			for info, err := range riffbin.Chunks(r) {
				if err != nil {
					errText = err.Error()
					break
				}
				ev := chunkEvent{Depth: info.Depth, ID: info.ID.String(), BodySize: info.BodySize, BodyOffset: info.BodyOffset}
				if info.Grouped() {
					ev.GroupType = info.GroupType.String()
				} else {
					body, err := io.ReadAll(info.Body)
					if err != nil {
						errText = err.Error()
						break
					}
					ev.Body = string(body)
				}
				events = append(events, ev)
			}
			return events, errText
		}

		plainEvents, plainErr := collect(onlyReader{bytes.NewReader(truncated)})
		seekEvents, seekErr := collect(bytes.NewReader(truncated))
		if df := cmp.Diff(plainEvents, seekEvents); df != "" {
			t.Errorf("%d bytes: the paths yield different chunks: %s", n, df)
		}
		if plainErr != seekErr {
			t.Errorf("%d bytes: the paths report different errors:\n  read-through: %s\n  seeking:      %s", n, plainErr, seekErr)
		}
	}
}

// seekCountingReader is a full ReadSeekerAt that records the forward seeks
// made over it, so a test can prove the parser took the seek shortcut rather
// than assume it.
type seekCountingReader struct {
	*bytes.Reader
	skips int
}

func (s *seekCountingReader) Seek(off int64, whence int) (int64, error) {
	if whence == io.SeekCurrent && off > 0 {
		s.skips++
	}
	return s.Reader.Seek(off, whence)
}

// bigBodyFile builds a RIFF file holding one leaf body of the given size,
// between two small ones — large enough that skipping it takes the seek
// shortcut rather than reading through.
func bigBodyFile(t *testing.T, size int) []byte {
	t.Helper()

	big := make([]byte, size)
	for i := range big {
		big[i] = byte('a' + i%26)
	}
	var buf bytes.Buffer
	if _, err := riffbin.NewWriter(&buf).WriteChunk(&riffbin.RIFFChunk{
		FormType: riffbin.MustParseFourCC("TEST"),
		Payload: []riffbin.Chunk{
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("HDR "), Payload: []byte("xy")},
			&riffbin.ListChunk{
				ListType: riffbin.MustParseFourCC("LST1"),
				Payload: []riffbin.Chunk{
					&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("BIG "), Payload: big},
				},
			},
			&riffbin.InMemorySubChunk{ID: riffbin.MustParseFourCC("TAIL"), Payload: []byte("z")},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// collectSkipped walks the chunks without reading a single body, so every
// body is skipped — the only way the seek shortcut is ever taken.
func collectSkipped(r io.Reader) ([]chunkEvent, string) {
	var events []chunkEvent
	var errText string
	for info, err := range riffbin.Chunks(r) {
		if err != nil {
			errText = err.Error()
			break
		}
		ev := chunkEvent{Depth: info.Depth, ID: info.ID.String(), BodySize: info.BodySize, BodyOffset: info.BodyOffset}
		if info.Grouped() {
			ev.GroupType = info.GroupType.String()
		}
		events = append(events, ev)
	}
	return events, errText
}

// Only a body larger than the skip threshold is skipped by seeking, and only
// when it is left unread — every other differential test in this package runs
// on bodies small enough that both paths execute the same read-through code.
// This one holds the seek shortcut to the read-through path it stands in for:
// the same chunks and the same error, on the whole file and on every prefix
// of it, with the seeks counted so the shortcut cannot quietly stop running.
func TestChunksAgreeOnSeekSkippedBody(t *testing.T) {
	t.Parallel()

	// odd, so the big chunk carries a pad byte the skip must not swallow
	full := bigBodyFile(t, 8193)

	counting := &seekCountingReader{Reader: bytes.NewReader(full)}
	seekEvents, seekErr := collectSkipped(counting)
	if counting.skips == 0 {
		t.Fatal("no forward seek was made: the seek shortcut never ran, so this test proves nothing")
	}
	plainEvents, plainErr := collectSkipped(onlyReader{bytes.NewReader(full)})
	if df := cmp.Diff(plainEvents, seekEvents); df != "" {
		t.Errorf("the parser paths yield different chunks: %s", df)
	}
	if plainErr != seekErr {
		t.Errorf("the parser paths report different errors:\n  read-through: %s\n  seeking:      %s", plainErr, seekErr)
	}

	// every prefix: a skip running past the end of the input must fail exactly
	// where reading through would, not succeed because seeking past EOF does
	for n := 0; n < len(full); n++ {
		truncated := full[:n]
		plainEvents, plainErr := collectSkipped(onlyReader{bytes.NewReader(truncated)})
		seekEvents, seekErr := collectSkipped(bytes.NewReader(truncated))
		if df := cmp.Diff(plainEvents, seekEvents); df != "" {
			t.Fatalf("%d bytes: the parser paths yield different chunks: %s", n, df)
		}
		if plainErr != seekErr {
			t.Fatalf("%d bytes: the parser paths report different errors:\n  read-through: %s\n  seeking:      %s", n, plainErr, seekErr)
		}
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

	// exactly one yield, carrying io.EOF: an iterator yielding nothing at all
	// would leave this loop body unexecuted, so the count is asserted too
	var yields int
	for info, err := range riffbin.Chunks(bytes.NewReader(nil)) {
		yields++
		if !errors.Is(err, io.EOF) {
			t.Errorf("should be io.EOF but got: %v (info: %+v)", err, info)
		}
	}
	if yields != 1 {
		t.Errorf("should yield exactly one io.EOF pair but yielded %d time(s)", yields)
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

	t.Run("CyclicTreePanics", func(t *testing.T) {
		// a self-referential hand-built tree would otherwise iterate forever;
		// the depth bound turns the hang into a loud programmer error
		t.Parallel()
		cyclic := &riffbin.ListChunk{ListType: riffbin.MustParseFourCC("LOOP")}
		cyclic.Payload = []riffbin.Chunk{cyclic}

		defer func() {
			if recover() == nil {
				t.Error("walking a cyclic tree should panic at the depth bound")
			}
		}()
		count := 0
		for range riffbin.Walk(cyclic) {
			if count++; count > 1_000_000 {
				t.Fatal("the walk neither ended nor panicked")
			}
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

// The streaming parser accepts exactly what the tree readers accept, and
// yields the very chunks the tree holds, under every combination of the
// reader options — the inputs are chosen so that each option flips some
// verdict, and the acceptance matrix below pins which. A mode that stops
// changing verdicts, or a parser that yields the right verdict over the
// wrong chunks, fails here. (The fuzz target extends the agreement to
// arbitrary inputs.)
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
		// a stray 0x00 heading the stream: only the trailing-data modes skip
		// it, as the uncounted pad byte of a previous concatenated chunk
		"leadingPad": append([]byte{0x00}, paddedFileBytes...),
	}
	// the acceptance matrix, pinned per mode: a policy that silently stops
	// working would flip cells here, not just both readers at once
	accepts := map[string]map[string]bool{
		"strict":              {"padded": true, "rifx": true, "garbage": false, "unpadded": false, "garbagePad": false, "trailingData": false, "leadingPad": false},
		"padOmitted":          {"padded": true, "rifx": true, "garbage": false, "unpadded": true, "garbagePad": false, "trailingData": false, "leadingPad": false},
		"padGarbage":          {"padded": true, "rifx": true, "garbage": false, "unpadded": false, "garbagePad": true, "trailingData": false, "leadingPad": false},
		"trailing":            {"padded": true, "rifx": true, "garbage": false, "unpadded": false, "garbagePad": false, "trailingData": true, "leadingPad": true},
		"padOmitted+trailing": {"padded": true, "rifx": true, "garbage": false, "unpadded": true, "garbagePad": false, "trailingData": true, "leadingPad": true},
		"padGarbage+trailing": {"padded": true, "rifx": true, "garbage": false, "unpadded": false, "garbagePad": true, "trailingData": true, "leadingPad": true},
	}

	for _, mode := range readerModes {
		expected, ok := accepts[mode.name]
		if !ok {
			t.Fatalf("no acceptance row for mode %q — extend the matrix with the new mode", mode.name)
		}
		for name, b := range inputs {
			mode, name, b := mode, name, b
			t.Run(mode.name+"/"+name, func(t *testing.T) {
				t.Parallel()
				tree, treeErr := riffbin.ReadAll(bytes.NewReader(b), mode.opts...)
				events, chunksErr := chunksFlatten(t, onlyReader{bytes.NewReader(b)}, mode.opts...)
				seekEvents, seekErr := chunksFlatten(t, bytes.NewReader(b), mode.opts...)

				if want := expected[name]; (treeErr == nil) != want {
					t.Errorf("ReadAll should report accepted=%v but got: %v", want, treeErr)
				}
				if (treeErr == nil) != (chunksErr == nil) {
					t.Errorf("the readers disagree: ReadAll=%v Chunks=%v", treeErr, chunksErr)
				}
				if df := cmp.Diff(events, seekEvents); df != "" {
					t.Errorf("the parser paths yield different chunks: %s", df)
				}
				if errText(chunksErr) != errText(seekErr) {
					t.Errorf("the parser paths report different errors:\n  read-through: %v\n  seeking:      %v", chunksErr, seekErr)
				}
				if treeErr == nil {
					if df := cmp.Diff(flattenTree(t, tree), events); df != "" {
						t.Errorf("Chunks disagrees on the chunks: %s", df)
					}
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

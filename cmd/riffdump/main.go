package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/karupanerura/riffbin"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) != 2 {
		log.Fatalf("Usage: %s RIFF-file", os.Args[0])
	}

	f, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatalf("%s: %s", os.Args[1], err)
	}
	defer f.Close()

	riffChunk, err := riffbin.ReadSections(f)
	if err != nil {
		var syntaxErr *riffbin.SyntaxError
		if errors.As(err, &syntaxErr) {
			log.Fatalf("%s: %s at %d in %s", os.Args[1], syntaxErr.Reason, syntaxErr.Offset, syntaxErr.Path)
		}
		log.Fatalf("%s: %s", os.Args[1], err)
	}

	dumpChunk(riffChunk, 0)
}

func dumpChunk(chunk riffbin.Chunk, level int) {
	indent := strings.Repeat("  ", level)
	io.WriteString(os.Stdout, indent)
	switch c := chunk.(type) {
	case riffbin.GroupedChunk:
		fmt.Printf("%s[%s:%d]:\n", c.ChunkID(), c.GroupType(), c.BodySize())
		for _, cc := range c.SubChunks() {
			dumpChunk(cc, level+1)
		}
		return
	case riffbin.SubChunk:
		fmt.Printf("%s[%d]\n", c.ChunkID(), c.BodySize())
		io.WriteString(os.Stdout, indent)
		io.WriteString(os.Stdout, indent)
		replacer := strings.NewReplacer("\n", "\n"+indent+indent)
		dumper := hex.Dumper(&replacerWriter{w: os.Stdout, replacer: replacer})
		_, _ = io.Copy(dumper, c.Body())
		dumper.Close()
		os.Stdout.Write([]byte{'\n'})
		return
	}
}

type replacerWriter struct {
	w        io.Writer
	replacer *strings.Replacer
}

func (w *replacerWriter) Write(p []byte) (int, error) {
	return w.replacer.WriteString(w.w, string(p))
}

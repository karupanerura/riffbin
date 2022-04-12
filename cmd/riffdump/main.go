package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/karupanerura/riffbin"
)

func main() {
	if len(os.Args) != 2 {
		log.Printf("Usage: %s RIFF-file", os.Args[0])
	}

	f, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatalf("%s: %s", err.Error(), os.Args[1])
	}
	defer f.Close()

	riffChunk, err := riffbin.ReadSections(f)
	if err != nil {
		pos, _ := f.Seek(0, io.SeekCurrent)
		log.Fatalf("%s: %s at %d", err.Error(), os.Args[1], pos)
	}

	dumpChunk(riffChunk, 0)
}

func dumpChunk(chunk riffbin.Chunk, level int) {
	indent := strings.Repeat("  ", level)
	io.WriteString(os.Stdout, indent)
	switch c := chunk.(type) {
	case *riffbin.RIFFChunk:
		fmt.Printf("RIFF[%s:%d]:\n", c.FormType, c.BodySize())
		for _, cc := range c.Payload {
			dumpChunk(cc, level+1)
		}
		return
	case *riffbin.ListChunk:
		fmt.Printf("LIST[%s:%d]:\n", c.ListType, c.BodySize())
		for _, cc := range c.Payload {
			dumpChunk(cc, level+1)
		}
		return
	case riffbin.SubChunk:
		fmt.Printf("%s[%d]\n", c.ChunkID(), c.BodySize())
		io.WriteString(os.Stdout, indent)
		io.WriteString(os.Stdout, indent)
		replacer := strings.NewReplacer("\n", "\n"+indent+indent)
		dumper := hex.Dumper(&replacerWriter{w: os.Stdout, replacer: replacer})
		_, _ = io.Copy(dumper, c)
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

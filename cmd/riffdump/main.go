package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/karupanerura/riffbin"
)

func main() {
	log.SetFlags(0)
	lenient := flag.Bool("lenient", false, "accept files that omit pad bytes after odd-sized chunks or carry data after the RIFF chunk")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [-lenient] RIFF-file\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	name := flag.Arg(0)

	f, err := os.Open(name)
	if err != nil {
		log.Fatalf("%s: %s", name, err)
	}
	defer f.Close()

	var opts []riffbin.ReaderOption
	if *lenient {
		opts = append(opts, riffbin.AllowUnpaddedChunks(), riffbin.AllowTrailingData())
	}
	riffChunk, err := riffbin.ReadSections(f, opts...)
	if err != nil {
		var syntaxErr *riffbin.SyntaxError
		if errors.As(err, &syntaxErr) {
			log.Fatalf("%s: %s at %d in %s", name, syntaxErr.Reason, syntaxErr.Offset, syntaxErr.Path)
		}
		log.Fatalf("%s: %s", name, err)
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
	default:
		fmt.Printf("%s[%d] (unsupported chunk type %T)\n", chunk.ChunkID(), chunk.BodySize(), chunk)
		return
	}
}

type replacerWriter struct {
	w        io.Writer
	replacer *strings.Replacer
}

func (w *replacerWriter) Write(p []byte) (int, error) {
	// the replacement may write more bytes than it was given, but an io.Writer
	// must not report more than len(p)
	if _, err := w.replacer.WriteString(w.w, string(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

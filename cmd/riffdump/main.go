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
	padding := flag.String("padding", "strict", "padding policy: strict, omitted (pad bytes omitted after odd-sized chunks) or garbage (pad bytes hold garbage)")
	concat := flag.Bool("concat", false, "read a stream of concatenated RIFF chunks, dumping every chunk")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [-padding strict|omitted|garbage] [-concat] RIFF-file\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	var opts []riffbin.ReaderOption
	switch *padding {
	case "strict":
	case "omitted":
		opts = append(opts, riffbin.PadOmitted)
	case "garbage":
		opts = append(opts, riffbin.PadGarbage)
	default:
		flag.Usage()
		os.Exit(2)
	}
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

	if err := dump(os.Stdout, f, opts, *concat); err != nil {
		log.Fatalf("%s: %s", name, err)
	}
}

// dump streams the chunks of one RIFF chunk — or, with concat, of every chunk
// of a concatenated stream — to w without building a tree, so a file with very
// many chunks dumps in memory proportional to the nesting depth, and the sizes
// printed are the ones declared in the file: a tree recomputes canonical
// sizes, which differ on a leniently read file. Without concat, anything
// following the root chunk is an error, so a truncated dump cannot pass as a
// complete one.
func dump(w io.Writer, r io.Reader, opts []riffbin.ReaderOption, concat bool) error {
	if !concat {
		return dumpOne(w, r, opts)
	}
	opts = append(append([]riffbin.ReaderOption{}, opts...), riffbin.AllowTrailingData())
	for i := 0; ; i++ {
		switch err := dumpOne(w, r, opts); {
		case errors.Is(err, io.EOF):
			if i == 0 {
				return err // an empty input is an error, not an empty stream
			}
			return nil
		case err != nil:
			return err
		}
	}
}

func dumpOne(w io.Writer, r io.Reader, opts []riffbin.ReaderOption) error {
	for info, err := range riffbin.Chunks(r, opts...) {
		if err != nil {
			return err
		}
		indent := strings.Repeat("  ", info.Depth)
		if info.Grouped() {
			fmt.Fprintf(w, "%s%s[%s:%d]:\n", indent, info.ID, info.GroupType, info.BodySize)
			continue
		}
		fmt.Fprintf(w, "%s%s[%d]\n", indent, info.ID, info.BodySize)
		hexIndent := strings.Repeat("  ", info.Depth*2)
		io.WriteString(w, hexIndent)
		replacer := strings.NewReplacer("\n", "\n"+hexIndent)
		dumper := hex.Dumper(&replacerWriter{w: w, replacer: replacer})
		if _, err := io.Copy(dumper, info.Body); err != nil {
			dumper.Close()
			return err
		}
		if err := dumper.Close(); err != nil {
			return err
		}
		io.WriteString(w, "\n")
	}
	return nil
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

package main

import (
	"encoding/hex"
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
		opts = append(opts, riffbin.AllowPaddingViolations(), riffbin.AllowTrailingData())
	}
	if err := dump(f, opts); err != nil {
		log.Fatalf("%s: %s", name, err)
	}
}

// dump streams the chunks of one RIFF chunk to stdout without building a tree,
// so a file with very many chunks dumps in memory proportional to the nesting
// depth, and the sizes printed are the ones declared in the file — a tree
// recomputes canonical sizes, which differ on a leniently read file.
func dump(r io.Reader, opts []riffbin.ReaderOption) error {
	for info, err := range riffbin.Chunks(r, opts...) {
		if err != nil {
			return err
		}
		indent := strings.Repeat("  ", info.Depth)
		if info.Grouped() {
			fmt.Printf("%s%s[%s:%d]:\n", indent, info.ID, info.GroupType, info.BodySize)
			continue
		}
		fmt.Printf("%s%s[%d]\n", indent, info.ID, info.BodySize)
		hexIndent := strings.Repeat("  ", info.Depth*2)
		io.WriteString(os.Stdout, hexIndent)
		replacer := strings.NewReplacer("\n", "\n"+hexIndent)
		dumper := hex.Dumper(&replacerWriter{w: os.Stdout, replacer: replacer})
		if _, err := io.Copy(dumper, info.Body); err != nil {
			return err
		}
		if err := dumper.Close(); err != nil {
			return err
		}
		os.Stdout.Write([]byte{'\n'})
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

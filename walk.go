package riffbin

import "iter"

// Walk returns an iterator over c and every chunk below it, in depth-first
// document order — the order the chunks appear in a RIFF file. Breaking out of
// the loop stops the walk, so a search over a parsed tree can end at the first
// chunk of interest.
func Walk(c Chunk) iter.Seq[Chunk] {
	return func(yield func(Chunk) bool) {
		walk(c, yield)
	}
}

func walk(c Chunk, yield func(Chunk) bool) bool {
	if !yield(c) {
		return false
	}
	if g, ok := c.(GroupedChunk); ok {
		for _, child := range g.Children() {
			if !walk(child, yield) {
				return false
			}
		}
	}
	return true
}

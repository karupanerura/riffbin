package riffbin

import "iter"

// Walk returns an iterator over c and every chunk below it, in depth-first
// document order — the order the chunks appear in a RIFF file. Breaking out of
// the loop stops the walk, so a search over a parsed tree can end at the first
// chunk of interest.
func Walk(c Chunk) iter.Seq[Chunk] {
	return func(yield func(Chunk) bool) {
		// an explicit stack instead of recursion: a hand-built tree can nest
		// deeper than the call stack, and the walk must not crash on it
		stack := []Chunk{c}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if !yield(cur) {
				return
			}
			if g, ok := cur.(GroupedChunk); ok {
				children := g.Children()
				for i := len(children) - 1; i >= 0; i-- {
					stack = append(stack, children[i])
				}
			}
		}
	}
}

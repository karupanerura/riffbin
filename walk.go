package riffbin

import (
	"fmt"
	"iter"
)

// Walk returns an iterator over c and every chunk below it, in depth-first
// document order — the order the chunks appear in a RIFF file. Breaking out of
// the loop stops the walk, so a search over a parsed tree can end at the first
// chunk of interest.
//
// Like the writers, Walk refuses chunks nested deeper than the readers read
// back: a hand-built tree that exceeds the bound — a cyclic tree included,
// which would otherwise iterate forever — panics rather than hang. Trees the
// readers produce are always within it.
func Walk(c Chunk) iter.Seq[Chunk] {
	return func(yield func(Chunk) bool) {
		// an explicit stack instead of recursion: a hand-built tree can nest
		// deeper than the call stack, and the walk must not crash on it
		type frame struct {
			c     Chunk
			depth int
		}
		stack := []frame{{c: c}}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if !yield(cur.c) {
				return
			}
			if g, ok := cur.c.(GroupedChunk); ok {
				if cur.depth >= maxGroupDepth {
					panic(fmt.Sprintf("riffbin: Walk: chunks are nested deeper than %d levels — the tree cycles or cannot be written", maxGroupDepth))
				}
				children := g.Children()
				for i := len(children) - 1; i >= 0; i-- {
					stack = append(stack, frame{c: children[i], depth: cur.depth + 1})
				}
			}
		}
	}
}

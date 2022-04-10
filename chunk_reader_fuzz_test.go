//go:build go1.18
// +build go1.18

package riffbin_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/karupanerura/riffbin"
)

func FuzzReadFull(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("RIF"))
	f.Add([]byte("LIFF"))
	f.Add([]byte{'R', 'I', 'F', 'F', 0x04, 0x00, 0x00})
	f.Add([]byte{'R', 'I', 'F', 'F', 0x04, 0x00, 0x00, 0x00, 'X', 'X', 'X'})
	f.Add([]byte{'R', 'I', 'F', 'F', 0x01, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D'})
	f.Add([]byte{'R', 'I', 'F', 'F', 0x07, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x00, 0x00, 0x00, 0x00})
	f.Add([]byte{'R', 'I', 'F', 'F', 0x09, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x00, 0x00, 0x00, 0x00})
	f.Add([]byte{'R', 'I', 'F', 'F', 0x08, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x01, 0x00, 0x00, 0x00})
	f.Add([]byte{'R', 'I', 'F', 'F', 0x09, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x02, 0x00, 0x00, 0x00, 'A', 'B'})
	f.Add([]byte{'R', 'I', 'F', 'F', 0x0A, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x01, 0x00, 0x00, 0x00, 'A', 'B'})
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := riffbin.ReadFull(bytes.NewReader(b))
		if (c == nil && err == nil) || (c != nil && err != nil) {
			t.Log(hex.Dump(b))
			t.Fatal("invalid result")
		}
	})
}

func FuzzReadSections(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("RIF"))
	f.Add([]byte("LIFF"))
	f.Add([]byte{'R', 'I', 'F', 'F', 0x04, 0x00, 0x00})
	f.Add([]byte{'R', 'I', 'F', 'F', 0x04, 0x00, 0x00, 0x00, 'X', 'X', 'X'})
	f.Add([]byte{'R', 'I', 'F', 'F', 0x01, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D'})
	f.Add([]byte{'R', 'I', 'F', 'F', 0x07, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x00, 0x00, 0x00, 0x00})
	f.Add([]byte{'R', 'I', 'F', 'F', 0x09, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x00, 0x00, 0x00, 0x00})
	f.Add([]byte{'R', 'I', 'F', 'F', 0x08, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x01, 0x00, 0x00, 0x00})
	f.Add([]byte{'R', 'I', 'F', 'F', 0x09, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x02, 0x00, 0x00, 0x00, 'A', 'B'})
	f.Add([]byte{'R', 'I', 'F', 'F', 0x0A, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x01, 0x00, 0x00, 0x00, 'A', 'B'})
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := riffbin.ReadSections(bytes.NewReader(b))
		if (c == nil && err == nil) || (c != nil && err != nil) {
			t.Log(hex.Dump(b))
			t.Fatal("invalid result")
		}
	})
}

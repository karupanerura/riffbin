package riffbin_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/karupanerura/riffbin"
)

func TestReadFull(t *testing.T) {
	t.Parallel()
	t.Run("Valid", func(t *testing.T) {
		t.Parallel()
		t.Run("Wave", func(t *testing.T) {
			t.Parallel()
			const binary = "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"
			decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(binary))
			riffChunk, err := riffbin.ReadFull(decoder)
			if err != nil {
				t.Fatal(err)
			}

			expected := &riffbin.RIFFChunk{
				FormType: [4]byte{'W', 'A', 'V', 'E'},
				Payload: []riffbin.Chunk{
					&riffbin.OnMemorySubChunk{
						ID: [4]byte{'f', 'm', 't', ' '},
						Payload: []byte{
							0x01, 0x00, // Compression Code (Linear PCM)
							0x01, 0x00, // Number of channels (Monoral)
							0x44, 0xAC, 0x00, 0x00, // Sample rate (44.1Hz)
							0x44, 0xAC, 0x00, 0x00, // Average bytes per second (44.1Hz/Monoral)
							0x01, 0x00, // Block align (8bit/Monoral)
							0x08, 0x00, // Significant bits per sample (8bit)
						},
					},
					// very short sin wave
					&riffbin.OnMemorySubChunk{
						ID:      [4]byte{'d', 'a', 't', 'a'},
						Payload: []byte{0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77},
					},
				},
			}

			if df := cmp.Diff(riffChunk, expected, cmpopts.IgnoreUnexported(riffbin.OnMemorySubChunk{})); df != "" {
				t.Errorf("diff = %s", df)
			}
		})

		t.Run("EmptyPayload", func(t *testing.T) {
			t.Parallel()
			riffChunk, err := riffbin.ReadFull(bytes.NewReader([]byte{'R', 'I', 'F', 'F', 0x04, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D'}))
			if err != nil {
				t.Fatal(err)
			}

			expected := &riffbin.RIFFChunk{
				FormType: [4]byte{'A', 'B', 'C', 'D'},
				Payload:  []riffbin.Chunk{},
			}

			if df := cmp.Diff(riffChunk, expected, cmpopts.IgnoreUnexported(riffbin.OnMemorySubChunk{})); df != "" {
				t.Errorf("diff = %s", df)
			}
		})

		t.Run("EmptyListChunk", func(t *testing.T) {
			t.Parallel()
			riffChunk, err := riffbin.ReadFull(bytes.NewReader([]byte{'R', 'I', 'F', 'F', 0x10, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'L', 'I', 'S', 'T', 0x04, 0x00, 0x00, 0x00, 'E', 'F', 'G', 'H'}))
			if err != nil {
				t.Fatal(err)
			}

			expected := &riffbin.RIFFChunk{
				FormType: [4]byte{'A', 'B', 'C', 'D'},
				Payload: []riffbin.Chunk{
					&riffbin.ListChunk{
						ListType: [4]byte{'E', 'F', 'G', 'H'},
						Payload:  []riffbin.Chunk{},
					},
				},
			}

			if df := cmp.Diff(riffChunk, expected, cmpopts.IgnoreUnexported(riffbin.OnMemorySubChunk{})); df != "" {
				t.Errorf("diff = %s", df)
			}
		})

		t.Run("EmptySubChunk", func(t *testing.T) {
			t.Parallel()
			riffChunk, err := riffbin.ReadFull(bytes.NewReader([]byte{'R', 'I', 'F', 'F', 0x0C, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x00, 0x00, 0x00, 0x00}))
			if err != nil {
				t.Fatal(err)
			}

			expected := &riffbin.RIFFChunk{
				FormType: [4]byte{'A', 'B', 'C', 'D'},
				Payload: []riffbin.Chunk{
					&riffbin.OnMemorySubChunk{
						ID:      [4]byte{'E', 'F', 'G', 'H'},
						Payload: []byte{},
					},
				},
			}

			if df := cmp.Diff(riffChunk, expected, cmpopts.IgnoreUnexported(riffbin.OnMemorySubChunk{})); df != "" {
				t.Errorf("diff = %s", df)
			}
		})
	})

	t.Run("InvalidFormat", func(t *testing.T) {
		t.Parallel()
		for _, tt := range []struct {
			Name  string
			Bytes []byte
		}{
			{"EmptyInput", []byte{}},
			{"TooShortRIFFID", []byte("RIF")},
			{"InvalidRIFFID", []byte("LIFF")},
			{"TooShortSize", []byte{'R', 'I', 'F', 'F', 0x04, 0x00, 0x00}},
			{"TooShortType", []byte{'R', 'I', 'F', 'F', 0x04, 0x00, 0x00, 0x00, 'X', 'X', 'X'}},
			{"TooLargeTotalSize", []byte{'R', 'I', 'F', 'F', 0x05, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D'}},
			{"TooSmallTotalSize", []byte{'R', 'I', 'F', 'F', 0x07, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x00, 0x00, 0x00, 0x00}},
			{"TooShortSubChunkPayloadByTotalSize", []byte{'R', 'I', 'F', 'F', 0x09, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x00, 0x00, 0x00, 0x00}},
			{"TooShortSubChunkPayloadBySubChunkSize", []byte{'R', 'I', 'F', 'F', 0x08, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x01, 0x00, 0x00, 0x00}},
			{"TooLongSubChunkPayloadByTotalSize", []byte{'R', 'I', 'F', 'F', 0x09, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x02, 0x00, 0x00, 0x00, 'A', 'B'}},
			{"TooLongSubChunkPayloadBySubChunkSize", []byte{'R', 'I', 'F', 'F', 0x0A, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 0x01, 0x00, 0x00, 0x00, 'A', 'B'}},
		} {
			tt := tt
			t.Run(tt.Name, func(t *testing.T) {
				t.Parallel()
				c, err := riffbin.ReadFull(bytes.NewReader(tt.Bytes))
				if err != riffbin.ErrInvalidFormat {
					t.Errorf("unexpected error: %v", err)
				}
				if c != nil {
					t.Error("riff chunk should be nil")
				}
			})
		}
	})
}

func ExampleReadFull() {
	const binary = "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"
	decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(binary))
	riffChunk, err := riffbin.ReadFull(decoder)
	if err != nil {
		return
		panic(err)
	}

	fmt.Printf("ID = %q\n", string(riffChunk.ChunkID()))
	fmt.Printf("Size = %d\n", riffChunk.BodySize())
	for i, p := range riffChunk.Payload {
		fmt.Printf("[%d]ID = %q\n", i, string(p.ChunkID()))
		fmt.Printf("[%d]Size = %d\n", i, p.BodySize())
	}

	// Output:
	// ID = "RIFF"
	// Size = 2036
	// [0]ID = "fmt "
	// [0]Size = 16
	// [1]ID = "data"
	// [1]Size = 2000
}

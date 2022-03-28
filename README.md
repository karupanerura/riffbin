# github.com/karupanerura/riffbin ![](https://github.com/karupanerura/riffbin/workflows/test/badge.svg?branch=main) [![Go Reference](https://pkg.go.dev/badge/github.com/karupanerura/riffbin.svg)](https://pkg.go.dev/github.com/karupanerura/riffbin) [![codecov.io](https://codecov.io/github/karupanerura/riffbin/coverage.svg?branch=main)](https://codecov.io/github/karupanerura/riffbin?branch=main)

Go module implementing for RIFF binary format.

# Features

* Construct RIFF data structure
* Write RIFF data structure
  * Can write RIFF data from io.Reader
* Parse RIFF binary to data structure

# Motivation

There has never been a library that can be used without pre-determining the binary size in Go.

# Examples

## Example1: write WAVE file

```go
_, err := riffbin.NewCompletedChunkWriter(w).Write(&riffbin.RIFFChunk{
	FormType: [4]byte{'W', 'A', 'V', 'E'},
	Payload: []riffbin.Chunk{
		&riffbin.CompletedSubChunk{
			ID: [4]byte{'f', 'm', 't', ' '},
			Payload: []byte{
				0x01, 0x00, // Compression Code (Linear PCM)
				0x01, 0x00, // Number of channels (Monoral)
				0x44, 0xAC, 0x00, 0x00, // Sample rate (44.1Hz)
				0x10, 0xB1, 0x02, 0x00, // Average bytes per second (44.1Hz/Monoral)
				0x01, 0x00, // Block align (8bit/Monoral)
				0x08, 0x00, // Significant bits per sample (8bit)
			},
		},
		// very short sin wave
		&riffbin.CompletedSubChunk{
			ID:		 [4]byte{'d', 'a', 't', 'a'},
			Payload: []byte{0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77, 0x7f, 0x87, 0x8f, 0x97, 0x9f, 0xa6, 0xae, 0xb5, 0xbc, 0xc3, 0xca, 0xd0, 0xd6, 0xdc, 0xe1, 0xe6, 0xeb, 0xef, 0xf2, 0xf6, 0xf8, 0xfa, 0xfc, 0xfd, 0xfe, 0xff, 0xfe, 0xfd, 0xfc, 0xfa, 0xf8, 0xf6, 0xf2, 0xef, 0xeb, 0xe6, 0xe1, 0xdc, 0xd6, 0xd0, 0xca, 0xc3, 0xbc, 0xb5, 0xae, 0xa6, 0x9f, 0x97, 0x8f, 0x87, 0x7f, 0x77, 0x6f, 0x67, 0x5f, 0x58, 0x50, 0x49, 0x42, 0x3b, 0x34, 0x2e, 0x28, 0x22, 0x1d, 0x18, 0x13, 0x0f, 0x0c, 0x08, 0x06, 0x04, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0x04, 0x06, 0x08, 0x0c, 0x0f, 0x13, 0x18, 0x1d, 0x22, 0x28, 0x2e, 0x34, 0x3b, 0x42, 0x49, 0x50, 0x58, 0x5f, 0x67, 0x6f, 0x77},
		},
	},
})
```

## Example2: write WAVE file from io.Reader

```go
w, err := riffbin.NewIncompleteChunkWriter(f)
if err != nil {
	panic(err)
}

_, err = w.Write(&riffbin.RIFFChunk{
	FormType: [4]byte{'W', 'A', 'V', 'E'},
	Payload: []riffbin.Chunk{
		&riffbin.CompletedSubChunk{
			ID: [4]byte{'f', 'm', 't', ' '},
			Payload: []byte{
				0x01, 0x00, // Compression Code (Linear PCM)
				0x01, 0x00, // Number of channels (Monoral)
				0x44, 0xAC, 0x00, 0x00, // Sample rate (44.1Hz)
				0x10, 0xB1, 0x02, 0x00, // Average bytes per second (44.1Hz/Monoral)
				0x01, 0x00, // Block align (8bit/Monoral)
				0x08, 0x00, // Significant bits per sample (8bit)
			},
		},
		riffbin.NewIncompleteSubChunk([4]byte{'d', 'a', 't', 'a'}, r),
	},
})
```

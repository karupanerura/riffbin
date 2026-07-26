package riffbin

import "fmt"

// FourCC is a four-character code, used by RIFF for chunk IDs and group types.
//
// The specification requires it to be printable ASCII, padded on the right with
// spaces when the meaningful name is shorter than four characters (e.g. "fmt ").
type FourCC [4]byte

var (
	riffID = FourCC{'R', 'I', 'F', 'F'}
	rifxID = FourCC{'R', 'I', 'F', 'X'}
	listID = FourCC{'L', 'I', 'S', 'T'}

	// containers riffbin recognizes but does not implement
	rf64ID = FourCC{'R', 'F', '6', '4'}
	bw64ID = FourCC{'B', 'W', '6', '4'}
	ffirID = FourCC{'F', 'F', 'I', 'R'}
	xfirID = FourCC{'X', 'F', 'I', 'R'}
)

func (f FourCC) String() string { return string(f[:]) }

// Valid reports whether f consists solely of printable ASCII, as the RIFF specification requires.
func (f FourCC) Valid() bool {
	for _, b := range f {
		if b < 0x20 || b > 0x7E {
			return false
		}
	}
	return true
}

// ParseFourCC converts s to a FourCC, padding it on the right with spaces.
// It reports an error when s is longer than four bytes or is not printable ASCII.
func ParseFourCC(s string) (FourCC, error) {
	f := FourCC{' ', ' ', ' ', ' '}
	if len(s) > len(f) {
		return FourCC{}, fmt.Errorf("riffbin: %q is longer than %d bytes", s, len(f))
	}

	copy(f[:], s)
	if !f.Valid() {
		return FourCC{}, fmt.Errorf("riffbin: %q is not printable ASCII", s)
	}
	return f, nil
}

// MustFourCC is like ParseFourCC but panics on error.
// It is intended for four-character codes known at compile time.
func MustFourCC(s string) FourCC {
	f, err := ParseFourCC(s)
	if err != nil {
		panic(err)
	}
	return f
}

package media

import "encoding/binary"

// webpFormStart is where a WebP file's chunks begin, after "RIFF", the form's
// length and "WEBP".
const webpFormStart = 12

// refuseLosslessWebP walks raw's RIFF chunks as x/image/webp does, as far as
// the frame chunk it decodes, and answers ErrLosslessWebP when that decode
// would run the lossless decoder: for a VP8L frame, or for an ALPH chunk
// compressed losslessly ahead of the frame. A chunk list it cannot follow to a
// frame inside the file is refused as ErrNotAnImage rather than guessed at.
func refuseLosslessWebP(raw []byte) error {
	if len(raw) < webpFormStart {
		return ErrNotAnImage
	}
	// The form's length counts from just past itself.
	end := min(8+int64(binary.LittleEndian.Uint32(raw[4:8])), int64(len(raw)))
	for i := int64(webpFormStart); ; {
		if i+8 > end {
			return ErrNotAnImage
		}
		kind := string(raw[i : i+4])
		size := int64(binary.LittleEndian.Uint32(raw[i+4 : i+8]))
		data := i + 8
		if size > end-data {
			return ErrNotAnImage
		}
		switch kind {
		case "VP8 ":
			return nil
		case "VP8L":
			return ErrLosslessWebP
		case "ALPH":
			if err := refuseLosslessAlpha(raw[data : data+size]); err != nil {
				return err
			}
		}
		// A chunk of odd length is followed by a padding byte.
		i = data + size + size&1
	}
}

// alphaCompression is an ALPH chunk's compression method, the low two bits of
// its first byte. x/image refuses any method but these two.
type alphaCompression byte

const (
	alphaCompressionBits = 0x03

	alphaRaw      alphaCompression = 0
	alphaLossless alphaCompression = 1
)

// refuseLosslessAlpha reads an ALPH chunk's compression method and refuses a
// lossless one, which x/image decodes with its lossless decoder.
func refuseLosslessAlpha(chunk []byte) error {
	if len(chunk) == 0 {
		return ErrNotAnImage
	}
	switch alphaCompression(chunk[0] & alphaCompressionBits) {
	case alphaRaw:
		return nil
	case alphaLossless:
		return ErrLosslessWebP
	default:
		return ErrNotAnImage
	}
}

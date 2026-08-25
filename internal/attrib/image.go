package attrib

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"strings"
)

// An image costs what its pixels cost, and nothing like what its encoding costs.
//
// This is here because getting it wrong was expensive. A tool result carrying a
// screenshot stores it as base64, and running the character-density estimator
// over that charged 120,000 tokens for a 1400×1000 PNG whose real cost is about
// 1,900 — sixty-five times over, six times in one session. That did not merely
// make one row wrong: it pushed the session's total estimate past the real
// context, which put the fitted scale below the floor where a fit is believed, so
// no correction was applied at all and the overshoot was absorbed by shrinking
// every other category to fit. One misread row had corrupted the whole report.
const (
	// pixelsPerToken is the API's own arithmetic: tokens ≈ (width × height) / 750.
	pixelsPerToken = 750
	// longEdge is the size an image is resized down to before it is charged for.
	longEdge = 1568
	// maxPixels caps the area after resizing, and is derived rather than chosen:
	// the API documents the ceiling as about 1,600 tokens per image, and at 750
	// pixels a token that is 1,200,000 pixels. Expressing it this way means the
	// two constants cannot drift apart.
	maxPixels = maxImageTokens * pixelsPerToken
	// maxImageTokens is the documented ceiling. No image costs more than this,
	// however large it arrives.
	maxImageTokens = 1600
	// unknownImageTokens is charged for an image whose dimensions cannot be read.
	// The ceiling rather than a guess: an image is bounded, so being wrong high by
	// a few hundred tokens is containable, and it can never again be wrong by the
	// length of a base64 string.
	unknownImageTokens = maxImageTokens
	// headerBytes is how much of an image is needed to find its dimensions. A PNG
	// declares them 24 bytes in; a JPEG needs its segments walked, but not far.
	headerBytes = 1024
)

// ImageTokens is what an image in a transcript costs the context window.
//
// The payload is decoded only as far as the header: dimensions are what is
// wanted, and decoding half a megabyte of base64 to count pixels nobody asked
// about would be paying the cost this function exists to avoid.
func ImageTokens(data string) int {
	raw := decodeHead(data)
	w, h, ok := dimensions(raw)
	if !ok || w <= 0 || h <= 0 {
		return unknownImageTokens
	}

	// Resized the way the API resizes: down to the long edge, then down again if
	// the area is still over. Only ever down — a small image is charged for the
	// pixels it has.
	fw, fh := float64(w), float64(h)
	if long := math.Max(fw, fh); long > longEdge {
		scale := longEdge / long
		fw, fh = fw*scale, fh*scale
	}
	if area := fw * fh; area > maxPixels {
		scale := math.Sqrt(maxPixels / area)
		fw, fh = fw*scale, fh*scale
	}

	tokens := int(fw*fh) / pixelsPerToken
	if tokens < 1 {
		return 1
	}
	return tokens
}

// decodeHead decodes just the start of a base64 payload.
//
// Standard encoding, and a length rounded down to a multiple of four so the
// decoder is handed whole groups: a partial group is an error, and an error here
// would mean charging the fallback for an image whose header was perfectly
// readable.
func decodeHead(data string) []byte {
	data = strings.TrimSpace(data)
	n := headerBytes
	if len(data) < n {
		n = len(data)
	}
	n -= n % 4
	if n == 0 {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(data[:n])
	if err != nil && len(raw) == 0 {
		return nil
	}
	// A truncated payload decodes as far as it got, which for a header is far
	// enough — so the error is deliberately ignored when there are bytes.
	return raw
}

var pngMagic = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

// dimensions reads an image's size from its header.
//
// PNG and JPEG only. Between them they are what a screenshot tool and a paste
// produce, and an unrecognised format falls back to the ceiling rather than to
// the length of its encoding, which is the mistake being fixed.
func dimensions(raw []byte) (w, h int, ok bool) {
	switch {
	case len(raw) >= 24 && string(raw[:8]) == string(pngMagic):
		// IHDR is the first chunk and always first: 8 bytes of signature, 4 of
		// length, 4 of type, then width and height.
		return int(binary.BigEndian.Uint32(raw[16:20])),
			int(binary.BigEndian.Uint32(raw[20:24])), true
	case len(raw) >= 4 && raw[0] == 0xFF && raw[1] == 0xD8:
		return jpegDimensions(raw)
	}
	return 0, 0, false
}

// jpegDimensions walks the segment headers to the frame that declares the size.
func jpegDimensions(raw []byte) (w, h int, ok bool) {
	for i := 2; i+9 < len(raw); {
		if raw[i] != 0xFF {
			i++
			continue
		}
		marker := raw[i+1]
		// Standalone markers carry no length to skip past.
		if marker == 0xFF || (marker >= 0xD0 && marker <= 0xD9) {
			i += 2
			continue
		}
		length := int(binary.BigEndian.Uint16(raw[i+2 : i+4]))
		// A start-of-frame marker: precision, then height and width.
		if isSOF(marker) {
			return int(binary.BigEndian.Uint16(raw[i+7 : i+9])),
				int(binary.BigEndian.Uint16(raw[i+5 : i+7])), true
		}
		if length < 2 {
			return 0, 0, false
		}
		i += 2 + length
	}
	return 0, 0, false
}

// isSOF reports a start-of-frame marker. The range excludes the four markers
// inside it that mean something else — a Huffman table, an arithmetic coding
// table, and the two restart markers.
func isSOF(m byte) bool {
	switch m {
	case 0xC4, 0xC8, 0xCC:
		return false
	}
	return m >= 0xC0 && m <= 0xCF
}

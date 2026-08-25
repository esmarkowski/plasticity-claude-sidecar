package attrib

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/esmarkowski/plasticity-claude-sidecar/internal/transcript"
)

// pngHeader is a PNG signature and IHDR chunk of the given size, which is all
// ImageTokens reads.
func pngHeader(w, h int) string {
	var b bytes.Buffer
	b.Write(pngMagic)
	b.Write([]byte{0, 0, 0, 13})
	b.WriteString("IHDR")
	binary.Write(&b, binary.BigEndian, uint32(w))
	binary.Write(&b, binary.BigEndian, uint32(h))
	b.Write([]byte{8, 6, 0, 0, 0})
	// Padded to the size of a real screenshot — a few hundred kilobytes — so the
	// test measures the gap it exists to close rather than a toy version of it.
	b.Write(bytes.Repeat([]byte("PADDING!"), 32_000))
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func jpegHeader(w, h int) string {
	var b bytes.Buffer
	b.Write([]byte{0xFF, 0xD8})
	// An APP0 segment first, so the segment walk has something to skip.
	b.Write([]byte{0xFF, 0xE0, 0x00, 0x10})
	b.Write(bytes.Repeat([]byte{0}, 14))
	// SOF0: length, precision, height, width, components.
	b.Write([]byte{0xFF, 0xC0, 0x00, 0x11, 0x08})
	binary.Write(&b, binary.BigEndian, uint16(h))
	binary.Write(&b, binary.BigEndian, uint16(w))
	b.Write(bytes.Repeat([]byte{0}, 6))
	b.Write(bytes.Repeat([]byte("PADDING!"), 400))
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

// The bug this exists to prevent: a screenshot charged for the length of its
// base64 rather than for its pixels.
//
// The real numbers, from the session that exposed it: six 1400×1000 PNGs, each
// about 350KB of base64, each charged 110,000 to 135,000 tokens against a true
// cost near 1,900.
func TestImageTokensChargesPixelsNotEncoding(t *testing.T) {
	data := pngHeader(1400, 1000)
	got := ImageTokens(data)

	// 1.4 megapixels is over the area cap, so it is charged the ceiling.
	if got != maxImageTokens {
		t.Errorf("1400x1000 = %d tokens, want the %d ceiling", got, maxImageTokens)
	}
	// The point of the whole exercise: two orders of magnitude between what an
	// image costs and what its encoding would have been charged as prose.
	naive := Estimate(data)
	if naive < 50_000 {
		t.Fatalf("the fixture is too small to be a fair test: naive estimate %d", naive)
	}
	if got*50 > naive {
		t.Errorf("charged %d against a naive %d — not far enough apart to be the fix", got, naive)
	}
}

// The API resizes before charging, so a large image costs what the resized one
// costs and not what the original would.
func TestImageTokensResizesLikeTheAPI(t *testing.T) {
	// 4000x3000: long edge scales to 1568, giving 1568x1176 = 1,844,000 px, which
	// is over the area cap, so it scales again.
	big := ImageTokens(pngHeader(4000, 3000))
	// At the ceiling, give or take the rounding of two successive scalings.
	if big > maxImageTokens || big < maxImageTokens-2 {
		t.Errorf("4000x3000 = %d tokens, want the %d ceiling", big, maxImageTokens)
	}
	// A very wide image is bounded by the long edge, and ends up well under the
	// ceiling because the area is what is left after scaling.
	wide := ImageTokens(pngHeader(8000, 400))
	if wide > maxImageTokens || wide < 50 {
		t.Errorf("8000x400 = %d tokens", wide)
	}
	// Small images are charged for the pixels they have; nothing scales up.
	if small := ImageTokens(pngHeader(100, 100)); small != 100*100/750 {
		t.Errorf("100x100 = %d tokens", small)
	}
	// And nothing is free.
	if tiny := ImageTokens(pngHeader(4, 4)); tiny < 1 {
		t.Errorf("a 4x4 image cost %d", tiny)
	}
}

func TestImageTokensReadsJPEG(t *testing.T) {
	if got, want := ImageTokens(jpegHeader(800, 600)), 800*600/750; got != want {
		t.Errorf("800x600 JPEG = %d tokens, want %d", got, want)
	}
}

// An image whose header cannot be read falls back to the ceiling, never to the
// length of its encoding. Being wrong high by a few hundred tokens is
// containable; being wrong by the length of a base64 string is what broke the
// calibration.
func TestImageTokensFallsBackToTheCeiling(t *testing.T) {
	for name, data := range map[string]string{
		"empty":       "",
		"not base64":  "!!!!not base64 at all!!!!",
		"unknown gif": base64.StdEncoding.EncodeToString([]byte("GIF89a" + string(make([]byte, 400)))),
		"truncated":   pngHeader(1400, 1000)[:6],
	} {
		if got := ImageTokens(data); got != maxImageTokens {
			t.Errorf("%s = %d tokens, want the %d ceiling", name, got, maxImageTokens)
		}
	}
}

// A tool result carrying a screenshot is priced as its text plus its pixels, and
// the base64 never reaches the text estimator.
func TestResultTokensPricesImagesAndTextSeparately(t *testing.T) {
	content, err := json.Marshal([]any{
		map[string]any{"type": "text", "text": "Here is the page:"},
		map[string]any{"type": "image", "source": map[string]any{
			"type": "base64", "media_type": "image/png", "data": pngHeader(1400, 1000)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	blk := transcript.Block{Type: "tool_result", Content: content}

	got := resultTokens(blk)
	image := maxImageTokens
	if got < image || got > image+40 {
		t.Errorf("result = %d tokens, want about %d (the image) plus a short line of text", got, image)
	}
	// The base64 must not appear in what the text estimator is given.
	if text := resultText(blk); len(text) > 200 {
		t.Errorf("resultText returned %d chars — the payload is still in there", len(text))
	}
	// Two images cost twice as much: a result is often several screenshots.
	two, _ := json.Marshal([]any{
		map[string]any{"type": "image", "source": map[string]any{"data": pngHeader(1400, 1000)}},
		map[string]any{"type": "image", "source": map[string]any{"data": pngHeader(1400, 1000)}},
	})
	if got := resultTokens(transcript.Block{Content: two}); got != 2*image {
		t.Errorf("two images = %d tokens, want %d", got, 2*image)
	}
}

// A plain text result is unaffected, which is nearly all of them.
func TestResultTokensLeavesTextAlone(t *testing.T) {
	plain, _ := json.Marshal("the quick brown fox jumped over the lazy dog")
	blk := transcript.Block{Content: plain}
	if got, want := resultTokens(blk), Estimate("the quick brown fox jumped over the lazy dog"); got != want {
		t.Errorf("text result = %d, want %d", got, want)
	}
}

// A payload hiding under an unexpected key is still not prose.
func TestWithoutImagesDropsPayloadsByKeyToo(t *testing.T) {
	v := map[string]any{
		"type": "something_new",
		"data": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"note": "keep me",
	}
	got := Strings(withoutImages(v))
	if len(got) > 40 {
		t.Errorf("payload survived: %q", got)
	}
	if !bytes.Contains([]byte(got), []byte("keep me")) {
		t.Errorf("the text was dropped along with it: %q", got)
	}
}

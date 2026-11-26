package daemon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func testPNG(t *testing.T) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 7, 5))
	for y := 0; y < 5; y++ {
		for x := 0; x < 7; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 30), uint8(y * 50), 200, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestDIBRoundTrip(t *testing.T) {
	src := testPNG(t)
	dib, err := pngToDIB(src)
	if err != nil {
		t.Fatal(err)
	}
	back, err := dibToPNG(dib)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := png.Decode(bytes.NewReader(src))
	b, _ := png.Decode(bytes.NewReader(back))
	for y := 0; y < 5; y++ {
		for x := 0; x < 7; x++ {
			if color.NRGBAModel.Convert(a.At(x, y)) != color.NRGBAModel.Convert(b.At(x, y)) {
				t.Fatalf("pixel %d,%d differs", x, y)
			}
		}
	}
	// 24-bit bottom-up DIB as written by many Windows apps, and zero alpha 32-bit
	d24 := make([]byte, 40+8*2) // 2x2, stride 8
	binary.LittleEndian.PutUint32(d24[0:], 40)
	binary.LittleEndian.PutUint32(d24[4:], 2)
	binary.LittleEndian.PutUint32(d24[8:], 2)
	binary.LittleEndian.PutUint16(d24[14:], 24)
	copy(d24[40:], []byte{255, 0, 0, 0, 255, 0, 0, 0}) // bottom row: blue, green
	copy(d24[48:], []byte{0, 0, 255, 9, 9, 9, 0, 0})   // top row: red, grey
	out, err := dibToPNG(d24)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := png.Decode(bytes.NewReader(out))
	if c := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA); c != (color.NRGBA{255, 0, 0, 255}) {
		t.Fatalf("top-left should be red, got %v", c)
	}
	if c := color.NRGBAModel.Convert(img.At(0, 1)).(color.NRGBA); c != (color.NRGBA{0, 0, 255, 255}) {
		t.Fatalf("bottom-left should be blue, got %v", c)
	}
	if _, err := dibToPNG([]byte{1, 2, 3}); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestTrimPNG(t *testing.T) {
	src := testPNG(t)
	if !bytes.Equal(trimPNG(append(append([]byte{}, src...), 0, 0, 0, 0)), src) {
		t.Fatal("padding not trimmed")
	}
}

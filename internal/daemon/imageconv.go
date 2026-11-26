package daemon

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"  // decoder for toPNG
	_ "image/jpeg" // decoder for toPNG
	"image/png"
)

// Image helpers shared by the clipboard backends. Windows stores clipboard
// images as a "DIB" (a BMP file without its 14-byte file header).

// toPNG re-encodes a PNG/JPEG/GIF image as PNG.
func toPNG(data []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const (
	biRGB       = 0
	biBitfields = 3
)

// dibToPNG converts a CF_DIB / CF_DIBV5 block (24 or 32 bits per pixel) to PNG.
func dibToPNG(dib []byte) ([]byte, error) {
	if len(dib) < 40 {
		return nil, fmt.Errorf("DIB too short")
	}
	le := binary.LittleEndian
	hdrSize := int(le.Uint32(dib[0:]))
	w := int(int32(le.Uint32(dib[4:])))
	h := int(int32(le.Uint32(dib[8:])))
	bpp := int(le.Uint16(dib[14:]))
	comp := le.Uint32(dib[16:])
	if hdrSize < 40 || hdrSize > len(dib) || w <= 0 || h == 0 || w > 30000 || h > 30000 || h < -30000 {
		return nil, fmt.Errorf("unsupported DIB header")
	}
	if bpp != 24 && bpp != 32 {
		return nil, fmt.Errorf("unsupported DIB depth %d", bpp)
	}
	if comp != biRGB && comp != biBitfields {
		return nil, fmt.Errorf("unsupported DIB compression %d", comp)
	}
	off := hdrSize
	if comp == biBitfields && hdrSize == 40 {
		off += 12 // three color masks follow a plain BITMAPINFOHEADER
	}
	if colors := int(le.Uint32(dib[32:])); colors > 0 && colors < 1<<16 {
		off += colors * 4
	}
	topDown := h < 0
	if topDown {
		h = -h
	}
	stride := (w*bpp/8 + 3) &^ 3
	if off+stride*h > len(dib) {
		return nil, fmt.Errorf("DIB data truncated")
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	anyAlpha := false
	for y := 0; y < h; y++ {
		row := dib[off+stride*y:]
		dy := h - 1 - y
		if topDown {
			dy = y
		}
		for x := 0; x < w; x++ {
			px := row[x*bpp/8:]
			a := uint8(255)
			if bpp == 32 {
				a = px[3]
				if a != 0 {
					anyAlpha = true
				}
			}
			img.SetNRGBA(x, dy, color.NRGBA{R: px[2], G: px[1], B: px[0], A: a})
		}
	}
	if bpp == 32 && !anyAlpha { // most apps leave alpha at 0 meaning "opaque"
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 255
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// pngToDIB converts an image to a 32-bit bottom-up CF_DIB block.
func pngToDIB(data []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]byte, 40+w*h*4)
	le := binary.LittleEndian
	le.PutUint32(out[0:], 40)
	le.PutUint32(out[4:], uint32(w))
	le.PutUint32(out[8:], uint32(h)) // positive height = bottom-up
	le.PutUint16(out[12:], 1)
	le.PutUint16(out[14:], 32)
	le.PutUint32(out[16:], biRGB)
	le.PutUint32(out[20:], uint32(w*h*4))
	for y := 0; y < h; y++ {
		row := out[40+(h-1-y)*w*4:]
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = c.B, c.G, c.R, c.A
		}
	}
	return out, nil
}

// trimPNG drops bytes after the IEND chunk (Windows pads clipboard blocks).
func trimPNG(b []byte) []byte {
	if i := bytes.LastIndex(b, []byte("IEND")); i >= 4 && i+8 <= len(b) {
		return b[:i+8]
	}
	return b
}

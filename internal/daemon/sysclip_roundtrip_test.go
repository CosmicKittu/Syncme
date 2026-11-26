package daemon

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"
)

// clipboardRoundTrip exercises a real desktop clipboard: text and image
// round trips and change notification. setExternal simulates another app
// copying text (nil to skip that part).
func clipboardRoundTrip(t *testing.T, sys sysClipboard, setExternal func(string) error) {
	t.Helper()
	c, _ := newClip("text", "text/plain", []byte("héllo 👋 line1\nline2"), false, "test")
	if err := sys.Write(c); err != nil {
		t.Fatal("write text:", err)
	}
	time.Sleep(300 * time.Millisecond)
	got, err := sys.Read("pc")
	if err != nil || got == nil || got.Text != c.Text {
		t.Fatalf("text read back: %+v %v", got, err)
	}
	if got.Hash != c.Hash {
		t.Fatal("hash changed on round trip (would echo back to the phone)")
	}

	img := image.NewNRGBA(image.Rect(0, 0, 16, 9))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 7)
	}
	for y := 0; y < 9; y++ {
		for x := 0; x < 16; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 15), uint8(y * 25), 100, 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	ic, _ := newClip("image", "image/png", buf.Bytes(), false, "test")
	if err := sys.Write(ic); err != nil {
		t.Fatal("write image:", err)
	}
	time.Sleep(300 * time.Millisecond)
	got, err = sys.Read("pc")
	if err != nil || got == nil || got.Kind != "image" {
		t.Fatalf("image read back: %+v %v", got, err)
	}
	back, err := png.Decode(bytes.NewReader(got.Data))
	if err != nil || back.Bounds().Dx() != 16 || back.Bounds().Dy() != 9 {
		t.Fatalf("image decoded wrong: %v %v", back.Bounds(), err)
	}

	if setExternal == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	changed := make(chan struct{}, 10)
	go sys.Watch(ctx, func() { changed <- struct{}{} })
	time.Sleep(2500 * time.Millisecond) // let polling backends take a baseline
	for len(changed) > 0 {
		<-changed
	}
	if err := setExternal("from another app"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(8 * time.Second):
		t.Fatal("watch did not notice the copy")
	}
	got, _ = sys.Read("pc")
	if got == nil || got.Text != "from another app" {
		t.Fatalf("after external copy: %+v", got)
	}
}

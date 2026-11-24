package daemon

import (
	"context"
	"io"
	"log"
	"testing"
)

type fakeClip struct{ written []*Clip }

func (f *fakeClip) Name() string                              { return "fake" }
func (f *fakeClip) Read(string) (*Clip, error)                { return nil, nil }
func (f *fakeClip) Write(c *Clip) error                       { f.written = append(f.written, c); return nil }
func (f *fakeClip) Watch(ctx context.Context, _ func()) error { <-ctx.Done(); return nil }

func drain(p *clipPeer) []clipOut {
	var out []clipOut
	for {
		select {
		case o := <-p.out:
			out = append(out, o)
		default:
			return out
		}
	}
}

func TestClipHub(t *testing.T) {
	sys := &fakeClip{}
	h := newClipHub(log.New(io.Discard, "", 0), "pc")
	h.sys = sys
	phone, tablet := newClipPeer(&Device{Name: "phone"}), newClipPeer(&Device{Name: "tablet"})
	h.add(phone)
	h.add(tablet)

	c, _ := newClip("text", "text/plain", []byte("hello"), false, "phone")
	h.FromDevice(c, phone)
	if len(sys.written) != 1 {
		t.Fatal("clip from phone not written to PC clipboard")
	}
	if got := drain(tablet); len(got) != 1 || got[0].clip.Text != "hello" {
		t.Fatalf("tablet should get the phone's clip, got %v", got)
	}
	if got := drain(phone); len(got) != 0 {
		t.Fatal("phone must not get its own clip back")
	}
	// The PC clipboard watcher then sees our own write: ignored.
	echo, _ := newClip("text", "text/plain", []byte("hello"), false, "pc")
	h.FromPC(echo)
	if len(drain(phone))+len(drain(tablet)) != 0 {
		t.Fatal("echo of our own write was broadcast")
	}

	// A PC copy goes to everyone with auto on.
	tablet.setAuto(false)
	pc, _ := newClip("image", "image/png", []byte{1, 2, 3}, false, "pc")
	h.FromPC(pc)
	if len(drain(phone)) != 1 || len(drain(tablet)) != 0 {
		t.Fatal("auto setting not respected")
	}
	// ...but an explicit request is always answered.
	tablet.offer(h.Get(), true)
	if got := drain(tablet); len(got) != 1 || !got[0].reply {
		t.Fatal("explicit get not answered")
	}

	// Sensitive clips are never pushed automatically.
	sec, _ := newClip("text", "text/plain", []byte("hunter2"), true, "pc")
	h.FromPC(sec)
	if len(drain(phone)) != 0 {
		t.Fatal("sensitive clip was pushed")
	}
}

func TestChooseClipType(t *testing.T) {
	cases := []struct {
		types      []string
		kind, mime string
		sens       bool
	}{
		{[]string{"text/html", "text/plain;charset=utf-8", "UTF8_STRING"}, "text", "text/plain;charset=utf-8", false},
		{[]string{"text/html", "image/png"}, "image", "image/png", false},
		{[]string{"text/uri-list", "text/plain"}, "", "", false},
		{[]string{"text/plain", "x-kde-passwordManagerHint"}, "text", "text/plain", true},
		{[]string{"application/x-foo"}, "", "", false},
	}
	for _, c := range cases {
		k, m, s := chooseClipType(c.types)
		if k != c.kind || m != c.mime || s != c.sens {
			t.Errorf("%v: got %s %s %v", c.types, k, m, s)
		}
	}
}

package daemon

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

// Windows clipboard through the Win32 API (no cgo).

var (
	user32                         = syscall.NewLazyDLL("user32.dll")
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procEmptyClipboard             = user32.NewProc("EmptyClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procSetClipboardData           = user32.NewProc("SetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")
	procGlobalAlloc                = kernel32.NewProc("GlobalAlloc")
	procGlobalFree                 = kernel32.NewProc("GlobalFree")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
)

const (
	cfUnicodeText = 13
	cfDIB         = 8
	cfDIBV5       = 17
	cfHDROP       = 15 // copied files
	gmemMoveable  = 0x0002
)

func registerFormat(name string) uintptr {
	p, _ := syscall.UTF16PtrFromString(name)
	r, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(p)))
	return r
}

var (
	cfPNG = registerFormat("PNG") // used by browsers, Office, Snipping Tool
	// Set by password managers (KeePass, 1Password, Bitwarden...) for secrets.
	cfExclude = registerFormat("ExcludeClipboardContentFromMonitorProcessing")
)

func available(f uintptr) bool {
	if f == 0 {
		return false
	}
	r, _, _ := procIsClipboardFormatAvailable.Call(f)
	return r != 0
}

// withClipboard opens the clipboard (retrying while another app holds it).
func withClipboard(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var err error
	for i := 0; i < 20; i++ {
		var r uintptr
		r, _, err = procOpenClipboard.Call(0)
		if r != 0 {
			defer procCloseClipboard.Call()
			return fn()
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("clipboard busy: %v", err)
}

// winPtr turns an address of Windows-owned (non-Go) memory into a pointer.
func winPtr(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }

func readGlobal(f uintptr) ([]byte, bool) {
	h, _, _ := procGetClipboardData.Call(f)
	if h == 0 {
		return nil, false
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return nil, false
	}
	defer procGlobalUnlock.Call(h)
	n, _, _ := procGlobalSize.Call(h)
	if n == 0 || n > maxClipImage*2 {
		return nil, false
	}
	out := make([]byte, n)
	copy(out, unsafe.Slice((*byte)(winPtr(p)), n))
	return out, true
}

func setGlobal(f uintptr, data []byte) error {
	h, _, err := procGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if h == 0 {
		return fmt.Errorf("GlobalAlloc: %v", err)
	}
	p, _, err := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("GlobalLock: %v", err)
	}
	copy(unsafe.Slice((*byte)(winPtr(p)), len(data)), data)
	procGlobalUnlock.Call(h)
	if r, _, err := procSetClipboardData.Call(f, h); r == 0 {
		procGlobalFree.Call(h) // ownership only passes to Windows on success
		return fmt.Errorf("SetClipboardData: %v", err)
	}
	return nil
}

type winClipboard struct{}

func (winClipboard) Name() string { return "Windows clipboard" }

func (winClipboard) Read(from string) (*Clip, error) {
	if available(cfHDROP) {
		return nil, nil // copied files: use file sync instead
	}
	sens := available(cfExclude)
	var kind, mime string
	var data []byte
	err := withClipboard(func() error {
		if available(cfPNG) {
			if b, ok := readGlobal(cfPNG); ok {
				kind, mime, data = "image", "image/png", trimPNG(b)
				return nil
			}
		}
		for _, f := range []uintptr{cfDIBV5, cfDIB} {
			if available(f) {
				if b, ok := readGlobal(f); ok {
					png, err := dibToPNG(b)
					if err != nil {
						return err
					}
					kind, mime, data = "image", "image/png", png
					return nil
				}
			}
		}
		if available(cfUnicodeText) {
			if b, ok := readGlobal(cfUnicodeText); ok {
				u := make([]uint16, len(b)/2)
				for i := range u {
					u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
				}
				for i, c := range u {
					if c == 0 {
						u = u[:i]
						break
					}
				}
				// Windows uses CRLF; the phone and Linux use LF.
				text := strings.ReplaceAll(string(utf16.Decode(u)), "\r\n", "\n")
				kind, mime, data = "text", "text/plain", []byte(text)
			}
		}
		return nil
	})
	if err != nil || kind == "" || len(data) == 0 {
		return nil, err
	}
	return newClip(kind, mime, data, sens, from)
}

func (winClipboard) Write(c *Clip) error {
	var dib, png []byte
	if c.Kind == "image" {
		png = c.Data
		if c.Mime != "image/png" {
			var err error
			if png, err = toPNG(c.Data); err != nil {
				return err
			}
		}
		var err error
		if dib, err = pngToDIB(png); err != nil {
			return err
		}
	}
	return withClipboard(func() error {
		if r, _, err := procEmptyClipboard.Call(); r == 0 {
			return fmt.Errorf("EmptyClipboard: %v", err)
		}
		if c.Kind == "text" {
			text := strings.ReplaceAll(strings.ReplaceAll(c.Text, "\r\n", "\n"), "\n", "\r\n")
			u := utf16.Encode([]rune(text))
			b := make([]byte, 2*len(u)+2)
			for i, v := range u {
				b[2*i], b[2*i+1] = byte(v), byte(v>>8)
			}
			return setGlobal(cfUnicodeText, b)
		}
		// Both forms: apps like Paint read CF_DIB, browsers/chat apps prefer PNG.
		if err := setGlobal(cfDIB, dib); err != nil {
			return err
		}
		if cfPNG != 0 {
			return setGlobal(cfPNG, png)
		}
		return nil
	})
}

// Watch polls the clipboard sequence number, which changes on every copy.
func (winClipboard) Watch(ctx context.Context, changed func()) error {
	last, _, _ := procGetClipboardSequenceNumber.Call()
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		n, _, _ := procGetClipboardSequenceNumber.Call()
		if n != last {
			last = n
			changed()
		}
	}
}

func detectClipboard() (sysClipboard, string) {
	if err := user32.Load(); err != nil {
		return nil, "user32.dll not available"
	}
	return winClipboard{}, ""
}

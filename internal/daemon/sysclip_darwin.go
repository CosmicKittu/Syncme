package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// macOS clipboard via NSPasteboard, driven through osascript's JavaScript
// for Automation (built into every Mac, no extra install). Data goes through
// temp files so binary images and any text encoding survive untouched.

const macScript = `
ObjC.import('AppKit');
ObjC.import('Foundation');
function run(argv) {
  var mode = argv[0], path = argv[1];
  var pb = $.NSPasteboard.generalPasteboard;
  if (mode === 'count') return String(pb.changeCount);
  if (mode === 'types') return JSON.stringify(ObjC.deepUnwrap(pb.types) || []);
  if (mode === 'text') {
    var s = pb.stringForType($.NSPasteboardTypeString);
    if (s.isNil()) return 'none';
    s.writeToFileAtomicallyEncodingError(path, true, $.NSUTF8StringEncoding, null);
    return 'ok';
  }
  if (mode === 'png') {
    var d = pb.dataForType('public.png');
    if (d.isNil()) {
      var t = pb.dataForType('public.tiff');
      if (t.isNil()) return 'none';
      var rep = $.NSBitmapImageRep.imageRepWithData(t);
      if (rep.isNil()) return 'none';
      d = rep.representationUsingTypeProperties(4 /* PNG */, $.NSDictionary.dictionary);
    }
    d.writeToFileAtomically(path, true);
    return 'ok';
  }
  if (mode === 'settext') {
    var s2 = $.NSString.stringWithContentsOfFileEncodingError(path, $.NSUTF8StringEncoding, null);
    pb.clearContents;
    pb.setStringForType(s2, $.NSPasteboardTypeString);
    return 'ok';
  }
  if (mode === 'setpng') {
    var p = $.NSData.dataWithContentsOfFile(path);
    pb.clearContents;
    pb.setDataForType(p, 'public.png');
    var r = $.NSBitmapImageRep.imageRepWithData(p);
    if (!r.isNil()) pb.setDataForType(r.TIFFRepresentation, 'public.tiff');
    return 'ok';
  }
  return 'bad mode';
}`

type macClipboard struct{}

func (macClipboard) Name() string { return "macOS pasteboard" }

func macRun(ctx context.Context, mode, path string) (string, error) {
	out, err := run(ctx, nil, "osascript", "-l", "JavaScript", "-e", macScript, mode, path)
	return strings.TrimSpace(string(out)), err
}

func macTemp() (string, func(), error) {
	dir, err := os.MkdirTemp("", "syncme-clip-")
	if err != nil {
		return "", nil, err
	}
	return filepath.Join(dir, "data"), func() { os.RemoveAll(dir) }, nil
}

// macClipKind maps pasteboard types (UTIs) to what we share.
func macClipKind(types []string) (kind string, sensitive bool) {
	set := map[string]bool{}
	for _, t := range types {
		set[t] = true
	}
	// nspasteboard.org convention used by password managers
	sensitive = set["org.nspasteboard.ConcealedType"]
	if set["public.file-url"] || set["NSFilenamesPboardType"] {
		return "", sensitive // copied files in Finder: use file sync instead
	}
	if set["public.png"] || set["public.tiff"] {
		return "image", sensitive
	}
	if set["public.utf8-plain-text"] || set["NSStringPboardType"] || set["public.plain-text"] {
		return "text", sensitive
	}
	return "", sensitive
}

func (macClipboard) Read(from string) (*Clip, error) {
	ctx := context.Background()
	out, err := macRun(ctx, "types", "")
	if err != nil {
		return nil, err
	}
	var types []string
	if err := json.Unmarshal([]byte(out), &types); err != nil {
		return nil, fmt.Errorf("pasteboard types: %v", err)
	}
	kind, sens := macClipKind(types)
	if kind == "" {
		return nil, nil
	}
	path, cleanup, err := macTemp()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	mode, mime := "text", "text/plain"
	if kind == "image" {
		mode, mime = "png", "image/png"
	}
	res, err := macRun(ctx, mode, path)
	if err != nil || res != "ok" {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if kind == "text" && len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	return newClip(kind, mime, data, sens, from)
}

func (macClipboard) Write(c *Clip) error {
	path, cleanup, err := macTemp()
	if err != nil {
		return err
	}
	defer cleanup()
	mode := "settext"
	data := []byte(c.Text)
	if c.Kind == "image" {
		mode = "setpng"
		data = c.Data
		if c.Mime != "image/png" {
			png, err := toPNG(c.Data)
			if err != nil {
				return err
			}
			data = png
		}
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	res, err := macRun(context.Background(), mode, path)
	if err == nil && res != "ok" {
		err = fmt.Errorf("osascript: %s", res)
	}
	return err
}

// Watch polls the pasteboard's change counter (macOS has no change event).
func (macClipboard) Watch(ctx context.Context, changed func()) error {
	last := ""
	fails := 0
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		n, err := macRun(ctx, "count", "")
		if err != nil {
			if fails++; fails > 10 {
				return err
			}
			continue
		}
		fails = 0
		if _, err := strconv.Atoi(n); err != nil {
			continue
		}
		if last != "" && n != last {
			changed()
		}
		last = n
	}
}

func detectClipboard() (sysClipboard, string) {
	if _, err := macRun(context.Background(), "count", ""); err != nil {
		return nil, "cannot access the pasteboard (" + err.Error() + ")"
	}
	return macClipboard{}, ""
}
